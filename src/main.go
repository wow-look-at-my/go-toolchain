package main

import (
	"os"

	"github.com/wow-look-at-my/go-toolchain/src/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}
