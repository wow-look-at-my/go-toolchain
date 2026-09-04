package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/wow-look-at-my/go-containers/set"
	"github.com/wow-look-at-my/go-toolchain/src/gomod"
	"github.com/wow-look-at-my/go-toolchain/src/logger"
)

// RevisionEnv names the commit for a tree that carries no git history.
const RevisionEnv = "GO_TOOLCHAIN_VCS_REVISION"

// stampVarNames are the main-package variables a build fills with the revision.
var stampVarNames = []string{"gitHash", "GitHash", "gitCommit", "GitCommit", "commit", "Commit", "revision", "Revision"}

// resolveRevision reports the commit this build is of, or "" when nothing here
// knows it. The explicit variable wins because it answers the case the others
// cannot: a container build whose context excluded .git, where the go command's
// own stamping finds nothing either.
func resolveRevision() string {
	if rev := usableRevision(os.Getenv(RevisionEnv)); rev != "" {
		return rev
	}
	out, err := exec.Command("git", "rev-parse", "HEAD").Output()
	if err == nil {
		if rev := usableRevision(string(out)); rev != "" {
			return rev
		}
	}
	return usableRevision(os.Getenv("GITHUB_SHA"))
}

// usableRevision rejects what -ldflags cannot carry: the go
// command re-splits it on whitespace and quotes.
func usableRevision(rev string) string {
	rev = strings.TrimSpace(rev)
	if strings.ContainsAny(rev, " \t\n\r\"'") {
		return ""
	}
	return rev
}

// stampLDFlags is the -X list filling importPath's revision variables, empty
// when the package declares none. Depth: docs/VCS-STAMP.md.
func stampLDFlags(importPath string) string {
	dir := packageDir(gomod.ReadModulePath(), importPath)
	if dir == "" {
		return ""
	}
	return revisionStamp(importPath, gomod.PackageStringVars(dir), resolveRevision())
}

// revisionStamp writes the -X flags for the stamp variables declared holds,
// and warns instead when the build knows no revision to put in them.
func revisionStamp(importPath string, declared set.Set[string], rev string) string {
	var names []string
	for _, name := range stampVarNames {
		if declared.Contains(name) {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return ""
	}
	if rev == "" {
		logger.Warn("⇒ Warning: %s declares %s and this build knows no commit to fill them with, so the binary ships its placeholder; the tree carries no git history and neither %s nor GITHUB_SHA is set",
			importPath, strings.Join(names, ", "), RevisionEnv)
		return ""
	}
	flags := make([]string, len(names))
	for i, name := range names {
		flags[i] = "-X " + importPath + "." + name + "=" + rev
	}
	return strings.Join(flags, " ")
}

// packageDir is importPath's directory inside module mod, or "" when the path
// belongs elsewhere and this build cannot read its source.
func packageDir(mod, importPath string) string {
	if mod == "" || importPath == mod || importPath == "." {
		return "."
	}
	rel := strings.TrimPrefix(importPath, mod+"/")
	if rel == importPath {
		return ""
	}
	return filepath.FromSlash(rel)
}
