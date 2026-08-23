package cosmocompat

// bubbleteaGap closes bubbletea's cosmo build gap.
//
// tty_unix.go names its platforms one at a time -- darwin, dragonfly,
// freebsd, linux, netbsd, openbsd, solaris, aix, zos -- rather than using the
// "unix" tag, so cosmo is not among them and the file drops. It carries
// suspendSupported and suspendProcess, which tea.go and tty.go reference
// unconditionally, so the package fails to compile without it.
//
// The copy is sound because the fork already treats cosmo as a unix: cosmo is
// in internal/syslist's UnixOS, so every "unix"-tagged file in this package
// already builds, and tty_unix.go is written against the same surface.
var bubbleteaGap = gap{
	module:          "github.com/wow-look-at-my/bubbletea/v2",
	verifiedVersion: "v2.0.0-20260812203640-351d2159f8d8",
	copies: []copySpec{
		{"tty_unix.go", "tty_cosmo.go", ""},
	},
}
