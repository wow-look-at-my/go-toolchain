# The release stamp

A published binary reports the buildhost release number it shipped as. The number is an incrementing integer, so releases order by it. A commit hash does not order.

## How the number gets in

The autorelease in `action.yml` does this, in order, for a build with a publishable output:

1. `buildhost-create-release` creates the release, and buildhost assigns the number.
2. `src/releasestamp/stamp-release` writes the number into every build output that reserves a slot, then reads each one back. It rewrites each stamped output's `checksums.txt` line.
3. The hand-off upload and the buildhost publish both carry the stamped bytes. The publish names the same release number.

The action runs the stamper with `go run` from its own checkout, so the stamper always matches the action. It never waits for a newly published go-toolchain binary.

## The slot

The slot is many bytes: a 16-byte magic, one length byte, then a bounded number of bytes of the number. The magic is the bytes of `"\x00GOTOOLCHAIN-REL"`. A zero length means nothing stamped the binary.

A program reserves the slot by declaring it as a package-level array whose initializer spells out the magic:

```go
var releaseSlot = [16 + 1 + 64]byte{0, 'G', 'O', 'T', 'O', 'O', 'L', 'C', 'H', 'A', 'I', 'N', '-', 'R', 'E', 'L'}

func releaseNumber() (string, bool) {
	n := int(releaseSlot[16])
	if n == 0 || n > 64 {
		return "", false
	}
	return string(releaseSlot[17 : 17+n]), true
}
```

The magic must occur once in the binary. Nothing else in the program may spell it out as one run of bytes. The stamper refuses a binary with multiple slots, because the running program reads one of them and nothing can tell which. A binary with no slot is published as it is, with a warning.

Pair the number with the commit from the revision stamp ([VCS-STAMP.md](VCS-STAMP.md)) to print both.
