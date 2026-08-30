package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/wow-look-at-my/go-toolchain/src/logger"
	gotest "github.com/wow-look-at-my/go-toolchain/src/test"
)

var unignoreCmd = &cobra.Command{
	Use:   "unignore",
	Short: "Remove build-check exemptions",
	// PersistentPreRunE is set in init(): referencing unignoreCmd here would be an initialization cycle.
}

// unignorePreRun confirms interactively, then chains to the root PersistentPreRunE via unignoreCmd's OWN
// parent, not cmd.Parent(): cobra passes cmd as the subcommand, whose parent is unignoreCmd, so
// cmd.Parent() recursed forever.
func unignorePreRun(cmd *cobra.Command, args []string) error {
	if parent := unignoreCmd.Parent(); parent != nil && parent.PersistentPreRunE != nil {
		if err := parent.PersistentPreRunE(cmd, args); err != nil {
			return err
		}
	}
	return confirmUnignore()
}

// confirmUnignore prompts for interactive confirmation on stdin. Split from
// unignorePreRun so tests can exercise the prompt without triggering the
// root hook's side effects (output guard, cacheprog).
func confirmUnignore() error {
	// Prompt awaits input mid-line (no trailing newline), so it bypasses the logger via rawStdout.
	fmt.Fprint(rawStdout, "Remove exemption — are you sure? [y/N] ")
	reader := bufio.NewReader(os.Stdin)
	line, _ := reader.ReadString('\n')
	line = strings.TrimSpace(strings.ToLower(line))
	if line != "y" && line != "yes" {
		return fmt.Errorf("aborted")
	}
	return nil
}

var unignoreCoverageCmd = &cobra.Command{
	Use:          "coverage",
	Short:        "Remove coverage watermark",
	SilenceUsage: true,
	RunE:         runUnignoreCoverage,
}

func init() {
	unignoreCmd.PersistentPreRunE = unignorePreRun
	unignoreCmd.AddCommand(unignoreCoverageCmd)
	rootCmd.AddCommand(unignoreCmd)
}

func runUnignoreCoverage(cmd *cobra.Command, args []string) error {
	_, exists, err := gotest.GetWatermark(".")
	if err != nil {
		logger.Warn("Warning: %v", err)
		return nil
	}
	if !exists {
		logger.Output("No watermark is set.")
		return nil
	}

	if err := gotest.RemoveWatermark("."); err != nil {
		return fmt.Errorf("failed to remove watermark: %w", err)
	}
	logger.Output("Coverage watermark removed.")
	return nil
}
