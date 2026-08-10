package cmd

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/spf13/pflag"
	"github.com/wow-look-at-my/go-toolchain/src/build"
	"github.com/wow-look-at-my/go-toolchain/src/hostos"
	"github.com/wow-look-at-my/go-toolchain/src/logger"
	"github.com/wow-look-at-my/go-toolchain/src/runner"
)

// fingerprintFile returns the path where the last-successful-run fingerprint is stored.
func fingerprintFile() string {
	dir := filepath.Join(os.TempDir(), "go-toolchain-fingerprint")
	os.MkdirAll(dir, 0o755)
	wd, _ := os.Getwd()
	h := sha256.Sum256([]byte(wd))
	return filepath.Join(dir, hex.EncodeToString(h[:])+".sha256")
}

// runEnv is the process environment as it stood when the run started. The
// pipeline sets variables of its own as it goes — the cacheprog's socket paths
// carry the PID — so hashing os.Environ() at save time would stamp a
// fingerprint no later run could ever match, silently disabling the skip.
// captureRunEnv is called once, at the top of the root PersistentPreRunE,
// ahead of both isUpToDate and saveFingerprint.
var runEnv []string

func captureRunEnv() { runEnv = os.Environ() }

// fingerprintEnv returns the captured environment, falling back to the live one
// for direct callers (tests) that never went through PersistentPreRunE.
func fingerprintEnv() []string {
	if runEnv != nil {
		return runEnv
	}
	return os.Environ()
}

// volatileEnv names variables the shell rewrites on every command line, which
// nothing in a build can read as configuration: `_` holds the previous
// command's last argument, OLDPWD the previous directory, SHLVL the shell
// nesting depth. Without this every invocation would look different.
var volatileEnv = map[string]bool{"_": true, "OLDPWD": true, "SHLVL": true}

// flagFingerprint renders every root-command flag as name=value, sorted. A run
// invoked differently is a different run: --generate executes go:generate
// directives, --cgo changes what gets built, --count-generated changes what the
// file-length check fails on, --benchtime changes what the benchmarks measure.
// Every flag is folded in rather than a hand-picked subset, so a flag added
// later is covered without anyone remembering to add it here.
func flagFingerprint() string {
	var lines []string
	rootCmd.Flags().VisitAll(func(f *pflag.Flag) {
		lines = append(lines, f.Name+"="+f.Value.String())
	})
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

// computeFingerprint hashes all inputs that affect a go-toolchain run: all .go
// files (including tests), go.mod, go.sum, .dats suites and their .golden
// snapshots, everything under a testdata directory, Go version, the flags the
// run was invoked with, the environment it was invoked in, and every file
// pulled in by a //go:embed directive (resolved via go list).
func computeFingerprint(r runner.CommandRunner) (string, error) {
	h := sha256.New()

	fmt.Fprintf(h, "go:%s\n", runtime.Version())
	fmt.Fprintf(h, "toolchain:%s\n", buildVersion)
	fmt.Fprintf(h, "output:%s\n", outputDir)
	fmt.Fprintf(h, "flags:%s\n", flagFingerprint())

	// The environment decides what a run does as surely as the sources do: an
	// env-gated test or benchmark you just switched on is a different pipeline,
	// and skipping it reports a green run that never executed the thing you
	// turned on. Which variables a project's tests read is unknowable from
	// here, so everything is folded in except what the shell churns.
	env := append([]string(nil), fingerprintEnv()...)
	sort.Strings(env)
	for _, kv := range env {
		if name, _, ok := strings.Cut(kv, "="); ok && volatileEnv[name] {
			continue
		}
		fmt.Fprintf(h, "env:%s\n", kv)
	}

	var files []string
	err := filepath.WalkDir(".", func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if name == "build" || name == "vendor" || name == "node_modules" {
				return filepath.SkipDir
			}
			if name != "." && strings.HasPrefix(name, ".") {
				return filepath.SkipDir
			}
			return nil
		}
		name := d.Name()
		// .dats suites and their .golden snapshot files are pipeline inputs
		// (the dats phase runs them), so editing one must bust the
		// fingerprint or the "Up to date" fast-exit would skip the re-run.
		// action.yml is one too: tests read it as data (handoffname_test.go
		// asserts the hand-off name templates), and it is not reachable by
		// //go:embed from a package two directories down, so without this an
		// action.yml edit fast-exits "Up to date" and its assertions never
		// re-run locally -- a false green until CI catches it.
		// Everything under a testdata directory is a test input by convention —
		// the go command ignores those directories precisely so tests can read
		// them at run time. No //go:embed covers them, so without this a
		// changed golden or fixture leaves the run reporting "Up to date" and
		// the test that would now fail never runs.
		if strings.HasSuffix(name, ".go") || strings.HasSuffix(name, ".dats") ||
			strings.HasSuffix(name, ".golden") || name == "go.mod" || name == "go.sum" ||
			name == "action.yml" || name == "action.yaml" || underTestdata(path) {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return "", err
	}

	sort.Strings(files)

	for _, path := range files {
		fmt.Fprintf(h, "file:%s\n", path)
		f, err := os.Open(path)
		if err != nil {
			return "", err
		}
		if _, err := io.Copy(h, f); err != nil {
			f.Close()
			return "", err
		}
		f.Close()
	}

	// Fold in files pulled in by //go:embed. They are compile inputs to the
	// embedding package (and to that package's test binary), so an embed-only
	// change must bust the fingerprint even though no .go file changed —
	// otherwise the top-level "Up to date" skip fires and the rebuilt embedded
	// bytes (and the affected package's tests) are never re-run. An error here
	// (e.g. a broken build) is propagated so the caller declines to short-circuit.
	embeds, err := embeddedFiles(r)
	if err != nil {
		return "", err
	}
	for _, path := range embeds {
		fmt.Fprintf(h, "embed:%s\n", path)
		f, err := os.Open(path)
		if err != nil {
			return "", err
		}
		if _, err := io.Copy(h, f); err != nil {
			f.Close()
			return "", err
		}
		f.Close()
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}

// underTestdata reports whether any directory component of path is "testdata".
func underTestdata(path string) bool {
	for dir := filepath.Dir(path); dir != "." && dir != string(filepath.Separator); dir = filepath.Dir(dir) {
		if filepath.Base(dir) == "testdata" {
			return true
		}
	}
	return false
}

// embeddedFiles returns the absolute paths of every file referenced by a
// //go:embed directive in the main module's packages, de-duplicated and sorted.
//
// It shells out to `go list -test -json ./...` and lets go list resolve the
// embed patterns the way the compiler does (globs, directory trees, the all:
// prefix, quoted names, multiple patterns/lines), rather than parsing //go:embed
// comments by hand. The -test flag is required: without it go list leaves
// TestEmbedFiles and XTestEmbedFiles unresolved (null). ./... without -deps
// keeps the scope to the main module — dependency embeds are already pinned
// through go.mod/go.sum. GOCACHEPROG is cleared so go list doesn't spawn a
// cacheprog child that inherits stdout and stalls the io.ReadAll below (the
// same precaution the benchmark runner takes).
//
// Note: this covers only files a //go:embed directive names. A file a test
// reads at run time is picked up by the walk above when it lives under a
// testdata directory; one that lives anywhere else, under no embed directive,
// is still untracked.
func embeddedFiles(r runner.CommandRunner) ([]string, error) {
	proc, err := runner.Cmd("go", "list", "-test", "-json", "./...").
		WithQuiet().WithEnv("GOCACHEPROG", "").Run(r)
	if err != nil {
		return nil, err
	}
	out, _ := io.ReadAll(proc.Stdout())
	if err := proc.Wait(); err != nil {
		return nil, err
	}

	set := make(map[string]struct{})
	dec := json.NewDecoder(bytes.NewReader(out))
	for {
		var pkg struct {
			Dir             string
			EmbedFiles      []string
			TestEmbedFiles  []string
			XTestEmbedFiles []string
		}
		if err := dec.Decode(&pkg); err != nil {
			if err == io.EOF {
				break
			}
			return nil, err
		}
		if pkg.Dir == "" {
			continue
		}
		for _, group := range [][]string{pkg.EmbedFiles, pkg.TestEmbedFiles, pkg.XTestEmbedFiles} {
			for _, rel := range group {
				set[filepath.Join(pkg.Dir, rel)] = struct{}{}
			}
		}
	}

	embeds := make([]string, 0, len(set))
	for f := range set {
		embeds = append(embeds, f)
	}
	sort.Strings(embeds)
	return embeds, nil
}

// isUpToDate returns true if the project fingerprint matches the last successful run
// and all build outputs still exist.
func isUpToDate(r runner.CommandRunner) bool {
	fp := fingerprintFile()
	stored, err := os.ReadFile(fp)
	if err != nil {
		return false
	}

	current, err := computeFingerprint(r)
	if err != nil {
		return false
	}

	if strings.TrimSpace(string(stored)) != current {
		return false
	}

	targets, err := build.ResolveBuildTargets(r)
	if err != nil {
		return false
	}

	inDocker := build.InDocker()
	for _, t := range targets {
		outputName := t.OutputName
		if inDocker {
			// hostos: must mirror the naming in root.go's runBuildPhase.
			outputName = build.BinaryName(outputName, hostos.GOOS(), runtime.GOARCH)
		}
		outPath := filepath.Join(outputDir, outputName)
		if _, err := os.Stat(outPath); err != nil {
			return false
		}
	}

	return true
}

// saveFingerprint writes the current fingerprint to disk.
func saveFingerprint(r runner.CommandRunner) {
	current, err := computeFingerprint(r)
	if err != nil {
		logger.Warn("⇒ Warning: failed to compute fingerprint: %v", err)
		return
	}
	fp := fingerprintFile()
	if err := os.WriteFile(fp, []byte(current), 0o644); err != nil {
		logger.Warn("⇒ Warning: failed to save fingerprint: %v", err)
	}
}
