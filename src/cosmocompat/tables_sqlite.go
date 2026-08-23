package cosmocompat

// sqliteGap closes modernc.org/sqlite's cosmo build gap.
//
// The per-architecture sqlite_linux_<arch>.go is only part of the
// translation. Since v1.5x the rest lives in sqlite_g_<hex>.go, 258 of them
// at v1.57.0, each carrying its own combination of platforms -- so the types
// a build needs (TBitvec, TBtCursor, TBtreePayload, ...) are spread across
// the 127 of those that build for linux/amd64, and copying only the
// architecture file leaves every one of them undefined.
//
// Those names are content hashes and are regenerated on each upstream
// release, so the set is selected by evaluating each file's own build tag
// rather than listed here.
var sqliteGap = gap{
	module:          "modernc.org/sqlite",
	verifiedVersion: "v1.57.0",
	copies: []copySpec{
		{"lib/sqlite_linux_amd64.go", "lib/sqlite_cosmo_amd64.go", ""},
		{"lib/sqlite_linux_arm64.go", "lib/sqlite_cosmo_arm64.go", ""},
	},
	copyGlobs: []copyGlob{
		{dir: "lib", pattern: "sqlite_g_*.go", goos: "linux", goarch: "amd64"},
	},
}
