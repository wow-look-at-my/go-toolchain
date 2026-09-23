//go:build cosmo

package hostos

// cosmoTarget reports the build target, which runtime.GOOS on this fork
// does not: it names the host at run time.
const cosmoTarget = true
