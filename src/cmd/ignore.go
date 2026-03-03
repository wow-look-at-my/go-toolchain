package cmd

import (
	"bufio"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	gotest "github.com/wow-look-at-my/go-toolchain/src/test"
)

var ignoreCmd = &cobra.Command{
	Use:   "ignore",
	Short: "Manage build-check exemptions",
}

var ignoreCoverageCmd = &cobra.Command{
	Use:          "coverage",
	Short:        "Enable coverage ratchet (watermark)",
	Long:         "Creates a coverage watermark set to 0%%. The next build will\nratchet it up to the actual coverage, and future builds enforce\nthat coverage stays within 2.5%% of the high-water mark.",
	SilenceUsage: true,
	RunE:         runIgnoreCoverage,
}

var ignoreLinesCmd = &cobra.Command{
	Use:          "lines <file> [file...]",
	Short:        "Exempt files from file-length checks",
	Long:         "Marks files as exempt from the 500/750-line length checks.\nThe exemption is stored as an xattr containing the file's size and SHA-256 hash,\nso it auto-invalidates when the file changes.",
	Args:         cobra.MinimumNArgs(1),
	SilenceUsage: true,
	RunE:         runIgnoreLines,
}

func init() {
	ignoreCmd.AddCommand(ignoreCoverageCmd, ignoreLinesCmd)
	rootCmd.AddCommand(ignoreCmd)
}

func runIgnoreCoverage(cmd *cobra.Command, args []string) error {
	existing, exists, err := gotest.GetWatermark(".")
	if err == nil && exists {
		fmt.Printf("Watermark already set (%.1f%%).\n", existing)
		return nil
	}

	if err := gotest.SetWatermark(".", 0); err != nil {
		return fmt.Errorf("failed to set watermark: %w", err)
	}
	fmt.Println("Coverage watermark enabled. Next build will set it to actual coverage.")
	return nil
}

func runIgnoreLines(cmd *cobra.Command, args []string) error {
	for _, path := range args {
		lines, err := countLines(path)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		if lines <= fileLengthError {
			return fmt.Errorf("%s: %d lines — only files exceeding %d lines can be exempted", path, lines, fileLengthError)
		}
		if err := gotest.ExemptFileLength(path); err != nil {
			return err
		}
		fmt.Printf("File-length exemption set for %s\n", path)
	}
	return nil
}

func countLines(path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	n := 0
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		n++
	}
	if err := scanner.Err(); err != nil {
		return 0, err
	}
	if n == 0 {
		// Check if the file had content but no trailing newline
		info, err := os.Stat(path)
		if err == nil && info.Size() > 0 {
			n = 1
		}
	}
	return n, nil
}

