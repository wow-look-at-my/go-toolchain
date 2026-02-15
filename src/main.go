package main

import (
	"os"

	"github.com/wow-look-at-my/go-toolchain/src/cmd"
	"github.com/wow-look-at-my/go-toolchain/src/vet"
	"golang.org/x/tools/go/analysis/multichecker"
)

func main() {
	if os.Getenv("GO_TOOLCHAIN_VETTOOL") == "1" {
		multichecker.Main(vet.AssertLintAnalyzer)
		return
	}

	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}
