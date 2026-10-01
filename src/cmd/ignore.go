package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/wow-look-at-my/go-toolchain/src/logger"
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

func init() {
	ignoreCmd.AddCommand(ignoreCoverageCmd)
	rootCmd.AddCommand(ignoreCmd)
}

func runIgnoreCoverage(cmd *cobra.Command, args []string) error {
	if os.Getenv("CI") != "" || os.Getenv("CLAUDE_CODE_REMOTE") != "" {
		return fmt.Errorf("can't use ignore coverage on CI! Stop being lazy and write those tests!")
	}

	existing, exists, err := gotest.GetWatermark(".")
	if err == nil && exists {
		logger.Output("Watermark already set (%.1f%%).", existing)
		return nil
	}

	if err := gotest.SetWatermark(".", 0); err != nil {
		return fmt.Errorf("failed to set watermark: %w", err)
	}
	logger.Output("Coverage watermark enabled. Next build will set it to actual coverage.")
	return nil
}
