package cosmocompat

// sqliteGap closes modernc.org/sqlite's cosmo build gap. Through v1.48.0 the
// whole sqlite3.c-to-Go translation lived in one file per platform, so a
// straight copy of that file sufficed. Since then the generator
// (modernc.org/undup) factors declarations shared by several platforms out
// of the per-platform file into many small "sqlite_g_<hash>.go" siblings,
// each gated to whichever real platform combination happens to share that
// code -- never cosmo. Listing those by name breaks on every regeneration
// (the hashes and the split points both move), so dirMatches copies every
// file under lib/ whose EXISTING tag already covers linux/amd64 or
// linux/arm64 -- covering the per-platform file and every "g" sibling it
// now depends on, with no file list to maintain.
var sqliteGap = gap{
	module:          "modernc.org/sqlite",
	verifiedVersion: "v1.57.0",
	dirMatches: []dirMatch{
		{dir: "lib", goos: "linux", goarch: "amd64", archTag: "amd64"},
		{dir: "lib", goos: "linux", goarch: "arm64", archTag: "arm64"},
	},
	nativeFork: "github.com/wow-look-at-my/go-sqlite",
}
