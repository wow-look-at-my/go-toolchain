package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	gotest "github.com/wow-look-at-my/go-toolchain/src/test"
)

var unignoreCmd = &cobra.Command{
	Use:   "unignore",
	Short: "Remove build-check exemptions",
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		fmt.Print("Remove exemption — are you sure? [y/N] ")
		reader := bufio.NewReader(os.Stdin)
		line, _ := reader.ReadString('\n')
		line = strings.TrimSpace(strings.ToLower(line))
		if line != "y" && line != "yes" {
			return fmt.Errorf("aborted")
		}
		return nil
	},
}

var unignoreCoverageCmd = &cobra.Command{
	Use:          "coverage",
	Short:        "Remove coverage watermark",
	SilenceUsage: true,
	RunE:         runUnignoreCoverage,
}

var unignoreLinesCmd = &cobra.Command{
	Use:          "lines <file> [file...]",
	Short:        "Remove file-length exemptions",
	Args:         cobra.MinimumNArgs(1),
	SilenceUsage: true,
	RunE:         runUnignoreLines,
}

func init() {
	unignoreCmd.AddCommand(unignoreCoverageCmd, unignoreLinesCmd)
	rootCmd.AddCommand(unignoreCmd)
}

func runUnignoreCoverage(cmd *cobra.Command, args []string) error {
	_, exists, err := gotest.GetWatermark(".")
	if err != nil {
		fmt.Printf("Warning: %v\n", err)
		return nil
	}
	if !exists {
		fmt.Println("No watermark is set.")
		return nil
	}

	if err := gotest.RemoveWatermark("."); err != nil {
		return fmt.Errorf("failed to remove watermark: %w", err)
	}
	fmt.Println("Coverage watermark removed.")
	return nil
}

func runUnignoreLines(cmd *cobra.Command, args []string) error {
	for _, path := range args {
		if err := gotest.RemoveFileLengthExemption(path); err != nil {
			return err
		}
		fmt.Printf("File-length exemption removed for %s\n", path)
	}
	return nil
}
