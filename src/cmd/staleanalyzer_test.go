package cmd

import (
	"errors"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The real failure, from running a Go 1.25-built go-toolchain against a Go
// 1.27 toolchain: math/rand/v2 gained a generic method (func (r *Rand)
// N[Int intType](n Int) Int), and a go/types that predates generic methods
// panics on that signature. x/tools recovers the panic and reports it as an
// internal importer error, which names neither version.
const realStaleAnalyzerErr = `package load errors:
-: reading /root/.cache/go-toolchain/buildcache/b0/v1b07e6a95a49881dd53e1c0ee932b070480007b217a3fb484a3ab9f537e2d7eab: internal error in importing "math/rand/v2" (function with type parameters cannot have a receiver); please report an issue
/home/user/go-toolchain/src/cache/web_resilience.go:6:2: could not import math/rand/v2 (reading /root/.cache/go-toolchain/buildcache/b0/v1b07e6a95a49881dd53e1c0ee932b070480007b217a3fb484a3ab9f537e2d7eab: internal error in importing "math/rand/v2" (function with type parameters cannot have a receiver); please report an issue)
/home/user/go-toolchain/src/cache/web_resilience.go:220:20: undefined: rand`

func TestIsStaleAnalyzer(t *testing.T) {
	assert.True(t, isStaleAnalyzer(errors.New(realStaleAnalyzerErr)))
	assert.False(t, isStaleAnalyzer(nil))

	// A genuine source error must never be reported as a version skew: the
	// message tells the reader to update their tools instead of fixing code
	// that really is broken.
	assert.False(t, isStaleAnalyzer(errors.New(`package load errors:
src/cmd/foo.go:12:3: undefined: Bar`)))

	// The corrupt-cache signature is a different failure with a different
	// cure (drop the shared cache and rebuild), so it must not match here.
	assert.False(t, isStaleAnalyzer(errors.New(realCorruptExportDataErr)),
		"corrupt export data is a cache problem, not a version skew")
}

// The message names what could not be read, so the reader can tell one
// unreadable package from a whole tree of them.
func TestStaleAnalyzerPackages(t *testing.T) {
	assert.Equal(t, []string{"math/rand/v2"},
		staleAnalyzerPackages(errors.New(realStaleAnalyzerErr)))
	assert.Empty(t, staleAnalyzerPackages(nil))
}

// Both halves of the skew have to appear, or the reader cannot tell which one
// to move.
func TestStaleAnalyzerError(t *testing.T) {
	err := staleAnalyzerError(errors.New(realStaleAnalyzerErr), "go1.27.0")
	require.Error(t, err)
	msg := err.Error()

	assert.Contains(t, msg, "TOOLCHAIN VERSION SKEW")
	assert.Contains(t, msg, "not an error in your source")
	assert.Contains(t, msg, "math/rand/v2", "names the package that could not be read")
	assert.Contains(t, msg, runtime.Version(), "names the Go that built this binary")
	assert.Contains(t, msg, "go1.27.0", "names the Go running the build")
	assert.ErrorContains(t, err, "function with type parameters cannot have a receiver",
		"the original error stays wrapped")
}

// A version probe can fail; the message must still be usable without it.
func TestStaleAnalyzerErrorWithoutRunningVersion(t *testing.T) {
	msg := staleAnalyzerError(errors.New(realStaleAnalyzerErr), "").Error()
	assert.Contains(t, msg, "the toolchain in use")
	assert.Contains(t, msg, runtime.Version())
}
