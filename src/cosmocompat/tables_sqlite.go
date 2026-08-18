package cosmocompat

// sqliteGap closes modernc.org/sqlite's cosmo build gap: the whole
// sqlite3.c-to-Go translation lives in one file per platform, so each
// cosmo copy is just that file for the matching architecture.
var sqliteGap = gap{
	module:          "modernc.org/sqlite",
	verifiedVersion: "v1.48.0",
	copies: []copySpec{
		{"lib/sqlite_linux_amd64.go", "lib/sqlite_cosmo_amd64.go", ""},
		{"lib/sqlite_linux_arm64.go", "lib/sqlite_cosmo_arm64.go", ""},
	},
}
