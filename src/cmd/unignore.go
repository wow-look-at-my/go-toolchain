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
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		if cmd != nil {
			if parent := cmd.Parent(); parent != nil && parent.PersistentPreRunE != nil {
				if err := parent.PersistentPreRunE(cmd, args); err != nil {
					return err
				}
			}
		}
		logger.Output("Remove exemption — are you sure? [y/N] ")
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

func init() {
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
