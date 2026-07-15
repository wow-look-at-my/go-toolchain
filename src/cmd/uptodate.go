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

// computeFingerprint hashes all inputs that affect a go-toolchain run:
// all .go files (including tests), go.mod, go.sum, Go version, CGO flag, and
// every file pulled in by a //go:embed directive (resolved via go list).
func computeFingerprint(r runner.CommandRunner) (string, error) {
	h := sha256.New()

	fmt.Fprintf(h, "go:%s\n", runtime.Version())
	fmt.Fprintf(h, "toolchain:%s\n", buildVersion)
	fmt.Fprintf(h, "cgo:%v\n", cgoEnabled)
	fmt.Fprintf(h, "output:%s\n", outputDir)

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
		if strings.HasSuffix(name, ".go") || name == "go.mod" || name == "go.sum" {
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
// Note: only files covered by a //go:embed directive are tracked. A file a test
// reads at runtime via os.ReadFile that no //go:embed covers is still not
// tracked — that is a separate, broader gap.
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
