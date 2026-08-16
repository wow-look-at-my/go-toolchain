//go:build cosmo

package hostos

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// The authoritative signal outranks every probe. This is the seam
// runtime.CosmoHostOS() plugs into: the fork's rt0 sets __hostos from the APE
// boot path and dispatches every syscall on it, so unlike uname (ENOSYS on
// darwin) and the filesystem probes (deniable by a sandbox) it cannot be wrong
// or unavailable. Until it lands, the probes are all there is.
func TestHostSignalOutranksTheProbes(t *testing.T) {
	old := hostSignalFunc
	hostSignalFunc = func() string { return "darwin" }
	t.Cleanup(func() { hostSignalFunc = old })

	d := detectHostGOOS()
	assert.Equal(t, "darwin", d.OS)
	assert.Equal(t, "runtime", d.Method)
	assert.False(t, d.Guessed())
}

// An empty answer means "no authoritative signal here" and must fall through
// to the probes rather than being taken as the host.
func TestEmptyHostSignalFallsThroughToTheProbes(t *testing.T) {
	old := hostSignalFunc
	hostSignalFunc = func() string { return "" }
	t.Cleanup(func() { hostSignalFunc = old })

	assert.NotEqual(t, "runtime", detectHostGOOS().Method)
}
