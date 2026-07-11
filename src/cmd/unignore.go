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
	// PersistentPreRunE is assigned in init(): its body must reference
	// unignoreCmd, which inside this literal would be an initialization cycle.
}

// unignorePreRun confirms the removal interactively, after chaining to the
// root command's PersistentPreRunE (Claude output guard + cacheprog setup,
// which defining a hook here would otherwise shadow). The chain must go
// through unignoreCmd's OWN parent, not cmd.Parent(): cobra invokes the
// nearest hook with cmd = the executed SUBcommand (e.g. "coverage"), whose
// parent is unignoreCmd itself — following cmd.Parent() made this hook call
// itself until the stack overflowed on every `unignore coverage` run.
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
	fmt.Print("Remove exemption — are you sure? [y/N] ")
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
