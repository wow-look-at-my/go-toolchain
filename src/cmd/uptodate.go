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
	"github.com/wow-look-at-my/go-containers/set"
	"github.com/wow-look-at-my/go-toolchain/src/build"
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

// runEnv is captured once at run start, before the pipeline sets its own PID-derived vars.
var runEnv []string

func captureRunEnv() { runEnv = os.Environ() }

// fingerprintEnv returns the captured environment, or the live one for callers
// that skipped PersistentPreRunE.
func fingerprintEnv() []string {
	if runEnv != nil {
		return runEnv
	}
	return os.Environ()
}

// fingerprintFlags is the root command's flag set, wired up in root.go's init.
var fingerprintFlags *pflag.FlagSet

// volatileEnv holds shell-rewritten vars excluded from the fingerprint.
var volatileEnv = set.Of("_", "OLDPWD", "SHLVL")

// flagFingerprint renders every root flag as name=value, sorted, so a flag
// added later needs no update here. Reads fingerprintFlags rather than
// rootCmd directly, since rootCmd's init reaches this via saveFingerprint.
func flagFingerprint() string {
	if fingerprintFlags == nil {
		return ""
	}
	var lines []string
	fingerprintFlags.VisitAll(func(f *pflag.Flag) {
		lines = append(lines, f.Name+"="+f.Value.String())
	})
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

// isOutputDir reports whether path, relative to the walk root, is the output
// directory. outputDir can also be absolute, so both spellings are compared.
func isOutputDir(path string) bool {
	if filepath.Clean(path) == filepath.Clean(outputDir) {
		return true
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	outAbs, err := filepath.Abs(outputDir)
	return err == nil && abs == outAbs
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

	// Folds in every var except shell noise, so flipping an env-gated test counts as a different run.
	env := append([]string(nil), fingerprintEnv()...)
	sort.Strings(env)
	for _, kv := range env {
		if name, _, ok := strings.Cut(kv, "="); ok && volatileEnv.Contains(name) {
			continue
		}
		fmt.Fprintf(h, "env:%s\n", kv)
	}

	var files []string
	// The walk must skip the run's own product. Matching the NAME "build"
	// instead hid src/build, a real package, so an edit there left the
	// fingerprint unchanged and the fast exit served a stale binary.
	err := filepath.WalkDir(".", func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if isOutputDir(path) || name == "vendor" || name == "node_modules" {
				return filepath.SkipDir
			}
			if name != "." && strings.HasPrefix(name, ".") {
				return filepath.SkipDir
			}
			return nil
		}
		name := d.Name()
		// .dats/.golden files, action.yml (read as test data, not embed-reachable),
		// and testdata/ (convention-ignored by go, so no embed covers it) are
		// pipeline inputs the fast-exit would otherwise miss.
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

	// //go:embed files are compile inputs too; an embed-only change must still bust the fingerprint.
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
// It shells out to `go list -test -json ./...`, letting go list resolve the
// embed patterns (globs, directory trees, the all: prefix) instead of parsing
// //go:embed comments by hand. -test is required, or TestEmbedFiles and
// XTestEmbedFiles stay unresolved. ./... without -deps keeps the scope to the
// main module. GOCACHEPROG is cleared so go list doesn't spawn a cacheprog
// child that inherits stdout and stalls the io.ReadAll below.
//
// Note: files read at run time from a testdata directory are covered by the
// walk above; one living elsewhere with no embed directive stays untracked.
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

	embedded := set.New[string]()
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
				embedded.Add(filepath.Join(pkg.Dir, rel))
			}
		}
	}

	embeds := embedded.Values()
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

	// A branch-tracked dep's HEAD lives on a remote; an unchanged tree can still be stale if that branch moved.
	if trackedBranchDepsMoved(r) {
		return false
	}

	// An unchanged tree can predate branch-tracking; skipping here would skip the run that adds the markers.
	if len(untrackedOrgDeps()) > 0 {
		return false
	}

	targets, err := build.ResolveBuildTargets(r)
	if err != nil {
		return false
	}

	for _, t := range targets {
		// Must mirror the naming in root.go's runBuildPhase.
		outPath := filepath.Join(outputDir, build.BinaryName(t.OutputName, cosmoOS, cosmoFatArch))
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
