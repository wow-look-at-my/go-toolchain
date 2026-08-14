package hostos

import (
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
)

// The answer and the evidence must both be reported. "linux because uname said
// so" and "linux because nothing answered" are the same string and different
// facts; only Method separates them.
func TestDetectReportsItsEvidence(t *testing.T) {
	d := Detect()

	assert.Equal(t, GOOS(), d.OS, "Detect and GOOS must agree")
	assert.NotEmpty(t, d.Method, "an answer with no recorded method cannot be audited")

	if runtime.GOOS != "cosmo" {
		assert.Equal(t, "compiled", d.Method, "a non-cosmo build never probes")
		assert.False(t, d.Guessed())
		return
	}
	assert.Contains(t, []string{"uname", "coreservices", "procfs", "default"}, d.Method)
}

func TestDetectionGuessed(t *testing.T) {
	assert.True(t, Detection{OS: "linux", Method: "default"}.Guessed())
	for _, m := range []string{"uname", "coreservices", "procfs", "compiled"} {
		assert.False(t, Detection{OS: "linux", Method: m}.Guessed(), m)
	}
}

// The rendering is what CI greps, so its shape is a contract: the host, the
// method, and a loud marker when the answer is a fallback rather than a
// measurement.
func TestDetectionString(t *testing.T) {
	assert.Equal(t, "host: darwin (via coreservices)",
		Detection{OS: "darwin", Method: "coreservices"}.String())

	assert.Equal(t, "host: linux (via uname), uname: Linux",
		Detection{OS: "linux", Method: "uname", Uname: "Linux"}.String())

	guessed := Detection{OS: "linux", Method: "default"}.String()
	assert.Contains(t, guessed, "GUESSED")
	assert.Contains(t, guessed, "no probe answered")
}
