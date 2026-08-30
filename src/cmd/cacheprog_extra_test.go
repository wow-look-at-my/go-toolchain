package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCacheHome_Default(t *testing.T) {
	old := os.Getenv("XDG_CACHE_HOME")
	os.Unsetenv("XDG_CACHE_HOME")
	defer os.Setenv("XDG_CACHE_HOME", old)

	home, _ := os.UserHomeDir()
	assert.Equal(t, filepath.Join(home, ".cache", "go-toolchain"), cacheHome())
}

func TestCacheHome_XDG(t *testing.T) {
	old := os.Getenv("XDG_CACHE_HOME")
	os.Setenv("XDG_CACHE_HOME", "/custom/cache")
	defer os.Setenv("XDG_CACHE_HOME", old)

	assert.Equal(t, "/custom/cache/go-toolchain", cacheHome())
}

func TestIsVanityHostReachableWithChecker(t *testing.T) {
	old := vanityHostChecker
	defer func() { vanityHostChecker = old }()

	vanityHostChecker = func(host string) bool {
		return host == "reachable.example.com"
	}

	assert.True(t, isVanityHostReachable("reachable.example.com"))
	assert.False(t, isVanityHostReachable("unreachable.example.com"))
}

func TestQuoteExeForGOCACHEPROG(t *testing.T) {
	cases := []struct{ in, want string }{
		{"/usr/local/bin/go-toolchain", "/usr/local/bin/go-toolchain"},
		{"/home/build agent/go-toolchain", `"/home/build agent/go-toolchain"`},
		{`/tmp/has"quote/exe name`, `'/tmp/has"quote/exe name'`},
		{"/tmp/it's here/exe", `"/tmp/it's here/exe"`},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, quoteExeForGOCACHEPROG(c.in))
	}
}

// An APE reports its own path in the /c/-form the runtime rewrites
// GetModuleFileNameW into, and GOCACHEPROG is launched by a native PE go
// command that cannot open that spelling.
func TestCosmoPathToNT(t *testing.T) {
	cases := []struct{ in, want string }{
		{"/d/a/go-toolchain/smoke/gt-ape.exe", `D:\a\go-toolchain\smoke\gt-ape.exe`},
		{"/c/Users/runneradmin/gt.exe", `C:\Users\runneradmin\gt.exe`},
		{"/c", `C:\`},
		{"/c/", `C:\`},
		// Not a drive: a real unix path, and the shapes next to it.
		{"/usr/local/bin/go-toolchain", "/usr/local/bin/go-toolchain"},
		{"/dev/null", "/dev/null"},
		{"relative/gt", "relative/gt"},
		{"", ""},
		{"/", "/"},
		{`D:\already\nt.exe`, `D:\already\nt.exe`},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, cosmoPathToNT(c.in), "input %q", c.in)
	}
}

// The rewrite reaches the launch command, and only on an NT host.
func TestCacheProgCommandRewritesTheAPEPathOnNT(t *testing.T) {
	got, err := cacheProgCommand("cosmo", "windows", "/d/a/smoke/gt-ape.exe")
	require.NoError(t, err)
	assert.Equal(t, `D:\a\smoke\gt-ape.exe cacheprog`, got)

	// The same spelling on a linux host is a real path and stays put.
	got, err = cacheProgCommand("cosmo", "linux", "/d/a/smoke/gt-ape")
	require.NoError(t, err)
	assert.Equal(t, "/d/a/smoke/gt-ape cacheprog", got)
}

// TestCacheProgCommand pins the GOCACHEPROG launch command shapes: the bare
// self-exec everywhere EXCEPT a cosmo APE on a macOS host, where a #!/bin/sh
// wrapper re-execs the APE (the darwin kernel cannot execve the MZ polyglot
// directly — cmd/go died with "exec format error" on every go invocation,
// the visible half of the macOS APE pipeline wedge).
func TestCacheProgCommand(t *testing.T) {
	// Every non-(cosmo-on-darwin) combination keeps today's bare self-exec.
	for _, c := range []struct{ goos, host string }{
		{"linux", "linux"},
		{"darwin", "darwin"},
		{"windows", "windows"},
		{"cosmo", "linux"},   // APE self-assimilates to ELF; direct exec works
		{"cosmo", "windows"}, // the APE's PE payload is a native windows binary
	} {
		got, err := cacheProgCommand(c.goos, c.host, "/usr/local/bin/go-toolchain")
		require.NoError(t, err, "%s on %s", c.goos, c.host)
		assert.Equal(t, "/usr/local/bin/go-toolchain cacheprog", got, "%s on %s", c.goos, c.host)
	}

	// cosmo on a darwin host: a shebang wrapper is written and returned.
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	exe := "/Applications/my tools/gt-ape" // space exercises the quoting
	got, err := cacheProgCommand("cosmo", "darwin", exe)
	require.NoError(t, err)
	// The wrapper path has no quotable chars, so the command IS the wrapper path -- an executable re-execing the APE.
	data, err := os.ReadFile(got)
	require.NoError(t, err)
	assert.Equal(t, "#!/bin/sh\nexec '"+exe+"' cacheprog\n", string(data))
	info, err := os.Stat(got)
	require.NoError(t, err)
	assert.NotZero(t, info.Mode().Perm()&0o111, "wrapper must be executable")

	// A single quote in the executable path cannot be embedded safely: the cache is disabled rather than misquoted.
	_, err = cacheProgCommand("cosmo", "darwin", "/tmp/it's here/gt-ape")
	require.Error(t, err)
}
