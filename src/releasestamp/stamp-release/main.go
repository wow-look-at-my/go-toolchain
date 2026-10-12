// stamp-release writes the buildhost release number into each build output in
// a directory that reserves a slot for it, and reads each back.
//
//	go run ./src/releasestamp/stamp-release <release number> <build dir>
package main

import (
	"fmt"
	"os"

	"github.com/wow-look-at-my/go-toolchain/src/releasestamp"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: stamp-release <release number> <build dir>")
		os.Exit(2)
	}
	version, dir := os.Args[1], os.Args[2]
	stamped, err := releasestamp.StampDir(dir, version)
	if err != nil {
		fmt.Fprintln(os.Stderr, "stamp-release:", err)
		os.Exit(1)
	}
	if len(stamped) == 0 {
		fmt.Printf("::warning::stamp-release: no build output in %s reserves a release stamp slot, so none reports release %s. docs/RELEASE-STAMP.md shows the declaration.\n", dir, version)
		return
	}
	for _, name := range stamped {
		fmt.Printf("stamped release %s into %s\n", version, name)
	}
}
