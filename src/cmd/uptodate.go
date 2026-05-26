package cmd

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/wow-look-at-my/go-toolchain/src/build"
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
// all .go files (including tests), go.mod, go.sum, Go version, CGO flag.
func computeFingerprint() (string, error) {
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

	return hex.EncodeToString(h.Sum(nil)), nil
}

// isUpToDate returns true if the project fingerprint matches the last successful run
// and all build outputs still exist.
func isUpToDate() bool {
	fp := fingerprintFile()
	stored, err := os.ReadFile(fp)
	if err != nil {
		return false
	}

	current, err := computeFingerprint()
	if err != nil {
		return false
	}

	if strings.TrimSpace(string(stored)) != current {
		return false
	}

	r := runner.New()
	targets, err := build.ResolveBuildTargets(r)
	if err != nil {
		return false
	}

	inDocker := build.InDocker()
	for _, t := range targets {
		outputName := t.OutputName
		if inDocker {
			outputName = build.BinaryName(outputName, runtime.GOOS, runtime.GOARCH)
		}
		outPath := filepath.Join(outputDir, outputName)
		if _, err := os.Stat(outPath); err != nil {
			return false
		}
	}

	return true
}

// saveFingerprint writes the current fingerprint to disk.
func saveFingerprint() {
	current, err := computeFingerprint()
	if err != nil {
		fmt.Fprintf(os.Stderr, "⇒ Warning: failed to compute fingerprint: %v\n", err)
		return
	}
	fp := fingerprintFile()
	if err := os.WriteFile(fp, []byte(current), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "⇒ Warning: failed to save fingerprint: %v\n", err)
	}
}
