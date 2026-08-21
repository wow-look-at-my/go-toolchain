package integration

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/wow-look-at-my/dats/runner"
	"github.com/wow-look-at-my/go-toolchain/src/logger"
)

// Run discovers and runs all .dats test files in the given directory.
// Returns nil if no .dats files are found (graceful no-op).
func Run(ctx context.Context, testsDir string) error {
	files, err := filepath.Glob(filepath.Join(testsDir, "*.dats"))
	if err != nil {
		return fmt.Errorf("scanning for .dats files: %w", err)
	}

	if len(files) == 0 {
		return nil
	}

	logger.Info("==> Running integration tests (%d file(s))", len(files))

	r := runner.NewRunner(os.Stdout, false, false, "")

	totalPassed := 0
	totalFailed := 0

	for _, path := range files {
		result, err := r.RunFile(ctx, path)
		if err != nil {
			return fmt.Errorf("running %s: %w", path, err)
		}
		totalPassed += result.Passed
		totalFailed += result.Failed
	}

	if len(files) > 1 {
		line := fmt.Sprintf("Integration total: %d/%d passed", totalPassed, totalPassed+totalFailed)
		if totalFailed > 0 {
			line += fmt.Sprintf(", %d failed", totalFailed)
		}
		logger.Info("%s", line)
	}

	if totalFailed > 0 {
		return fmt.Errorf("integration tests: %d test(s) failed", totalFailed)
	}

	return nil
}
