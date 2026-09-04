package cmd

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCallerLDFlagsSurvivesGOFLAGS(t *testing.T) {
	// The failure this guards: the go command applies GOFLAGS before parsing
	// argv, so this pipeline's own -ldflags used to replace the caller's.
	assert.Equal(t, "-X=main.gitHash=abc123",
		callerLDFlags("-trimpath -ldflags=-X=main.gitHash=abc123 -mod=mod"))
	assert.Equal(t, "-s", callerLDFlags("--ldflags=-s -w"),
		"a doubled dash names the same flag, and -w past the space is its own GOFLAG")
	assert.Equal(t, "", callerLDFlags(""))
	assert.Equal(t, "", callerLDFlags("-trimpath -mod=mod"))
	assert.Equal(t, "", callerLDFlags("-ldflags"), "a bare flag names no value")
}

func TestCallerLDFlagsJoinsEveryOccurrence(t *testing.T) {
	assert.Equal(t, "-X=main.a=1 -X=main.b=2",
		callerLDFlags("-ldflags=-X=main.a=1 -ldflags=-X=main.b=2"))
}

func TestSplitGOFLAGSQuotesAWholeFieldOrNothing(t *testing.T) {
	assert.Equal(t, []string{"-a", "-b=c"}, splitGOFLAGS("  -a\t-b=c \n"))
	// A quote opening mid-field is ordinary text to the go command, so this
	// spelling breaks into pieces rather than carrying spaces through.
	assert.Equal(t, []string{`-ldflags="-X`, `a=b"`}, splitGOFLAGS(`-ldflags="-X a=b"`))
	assert.Equal(t, []string{"-ldflags=-X a=b"}, splitGOFLAGS(`'-ldflags=-X a=b'`))
	assert.Nil(t, splitGOFLAGS(`'unterminated`), "the go command refuses the whole value")
}

func TestQuotedGOFLAGSFieldReachesTheLinker(t *testing.T) {
	assert.Equal(t, "-X main.gitHash=abc -s",
		callerLDFlags(`-trimpath '-ldflags=-X main.gitHash=abc -s'`))
}
