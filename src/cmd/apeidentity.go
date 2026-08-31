package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/wow-look-at-my/go-toolchain/src/logger"
)

func init() {
	verifyIdenticalCmd := &cobra.Command{
		Use:   "verify-identical <name>=<path> <name>=<path>...",
		Short: "Fail unless every named file is byte-identical to the first",
		Long: `Hashes each file and compares every one after the first against it, by
name, printing sha256sum-style lines for visibility.

Built for CI's cross-host APE identity check: one compiler builds the fat
APE on several hosts, and this proves they produced the same bytes
(docs/MATRIX.md). The first argument is the reference; every later one that
differs is reported by name, and the command exits non-zero.`,
		Args: cobra.MinimumNArgs(2),
		RunE: runVerifyIdentical,
	}
	rootCmd.AddCommand(verifyIdenticalCmd)
}

// namedFile is a single <name>=<path> argument to verify-identical.
type namedFile struct {
	name string
	path string
}

func parseNamedFiles(args []string) ([]namedFile, error) {
	files := make([]namedFile, 0, len(args))
	for _, a := range args {
		name, path, ok := strings.Cut(a, "=")
		if !ok || name == "" || path == "" {
			return nil, fmt.Errorf("invalid argument %q: want <name>=<path>", a)
		}
		files = append(files, namedFile{name: name, path: path})
	}
	return files, nil
}

func runVerifyIdentical(cmd *cobra.Command, args []string) error {
	files, err := parseNamedFiles(args)
	if err != nil {
		return err
	}

	hashes := make([]string, len(files))
	for i, f := range files {
		h, err := fileHash(f.path)
		if err != nil {
			logger.Error("%s handed off no APE, so identity across hosts is untested, not proven", f.name)
			return fmt.Errorf("hashing %s (%s): %w", f.name, f.path, err)
		}
		hashes[i] = h
		logger.Output("%s  %s", h, f.path)
	}

	ref := files[0]
	failed := false
	for i := 1; i < len(files); i++ {
		if hashes[i] == hashes[0] {
			continue
		}
		failed = true
		logger.Error("the %s build differs from the %s build: the APE is one binary for every host, so every host has to produce it. Compare with cmp -l; the build-ID notes are what -trimpath and -ldflags=-buildid= exist to close (docs/MATRIX.md)", files[i].name, ref.name)
	}
	if failed {
		return fmt.Errorf("cross-host APE identity check failed")
	}
	return nil
}
