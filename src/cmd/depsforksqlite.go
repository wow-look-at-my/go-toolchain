package cmd

import (
	"fmt"
	"os"

	"github.com/wow-look-at-my/go-toolchain/src/logger"
	"github.com/wow-look-at-my/go-toolchain/src/runner"
	"golang.org/x/mod/modfile"
)

// modernSqliteModule is the upstream path a consumer's go.mod names.
// sqliteForkModule is the org's own drop-in fork -- see
// cosmocompat/tables_sqlite.go's nativeFork field, which must name the same
// path: that field tells cosmocompat this fork already ships real GOOS=cosmo
// support, so a consumer redirected here needs no further runtime patching.
const (
	modernSqliteModule = "modernc.org/sqlite"
	sqliteForkModule   = "github.com/wow-look-at-my/go-sqlite"
)

// EnforceSqliteFork redirects a bare modernc.org/sqlite dependency onto the
// org's own fork. modernc.org/sqlite has no upstream GOOS=cosmo port (see
// cosmocompat/COSMOCOMPAT.md) and is not built to be cross-platform in the
// first place, so every direct consumer of it hits that gap on its own; the
// fork exists so nobody has to.
//
// A require with no existing replace for the module gets one, onto the fork
// at its current default-branch HEAD. This only ever writes the bare
// replace line: EnforceOrgBranchTracking, run right after this in the same
// phase, recognizes the new line as an org module replacement and marks it
// // go-toolchain:auto-branch, and UpdateTrackedBranchDeps keeps it resolved
// on every run after that.
//
// A require ALREADY behind a replace is a deliberate choice, onto the fork
// or onto anything else, and is left alone. Only a DIRECT require is
// redirected -- an indirect one is a transitive dependency of some other
// module, not a driver a consumer chose to import, and rewriting it here
// would not change what that other module itself builds against.
//
// The rewrite is the enforcement -- locally the developer sees the diff and
// commits it, and in CI the resulting dirty tree fails the build
// (checkDirtyInCI), the same contract as every other go.mod mutation in
// this pipeline.
//
// Returns whether go.mod changed.
func EnforceSqliteFork(r runner.CommandRunner) (bool, error) {
	data, err := os.ReadFile("go.mod")
	if err != nil {
		return false, nil // Let go mod tidy handle a missing go.mod
	}
	f, err := modfile.Parse("go.mod", data, nil)
	if err != nil {
		return false, nil // Let go mod tidy handle parse errors
	}

	var sqliteReq *modfile.Require
	for _, req := range f.Require {
		if req.Mod.Path == modernSqliteModule && !req.Indirect {
			sqliteReq = req
			break
		}
	}
	if sqliteReq == nil {
		return false, nil
	}
	for _, rep := range f.Replace {
		if rep.Old.Path == modernSqliteModule {
			return false, nil // already replaced -- a deliberate choice, onto the fork or anything else
		}
	}

	version, err := resolveLatestVersionViaGit(r, sqliteForkModule)
	if err != nil {
		return false, fmt.Errorf("resolving %s: %w", sqliteForkModule, err)
	}
	if err := f.AddReplace(modernSqliteModule, "", sqliteForkModule, version); err != nil {
		return false, fmt.Errorf("adding replace for %s: %w", modernSqliteModule, err)
	}
	if !jsonOutput {
		logger.Info("⇒ %s has no cross-platform support upstream; redirecting to %s", modernSqliteModule, sqliteForkModule)
	}

	f.Cleanup()
	newData, err := f.Format()
	if err != nil {
		return false, fmt.Errorf("failed to format go.mod: %w", err)
	}
	if err := os.WriteFile("go.mod", newData, 0644); err != nil {
		return false, fmt.Errorf("failed to write go.mod: %w", err)
	}
	return true, nil
}

// needsSqliteFork reports whether go.mod has a direct modernc.org/sqlite
// require with no replace for it yet. It reads only go.mod, so the
// up-to-date fast exit can consult it without a network call: a tree that
// has not changed since the last green run can still predate this
// requirement, and the fast exit would otherwise skip the run that adds it.
func needsSqliteFork() bool {
	data, err := os.ReadFile("go.mod")
	if err != nil {
		return false
	}
	f, err := modfile.Parse("go.mod", data, nil)
	if err != nil {
		return false
	}

	hasDirectRequire := false
	for _, req := range f.Require {
		if req.Mod.Path == modernSqliteModule && !req.Indirect {
			hasDirectRequire = true
			break
		}
	}
	if !hasDirectRequire {
		return false
	}
	for _, rep := range f.Replace {
		if rep.Old.Path == modernSqliteModule {
			return false
		}
	}
	return true
}
