package cmd

import (
	"fmt"
	"path"
	"strings"

	"golang.org/x/mod/modfile"

	"github.com/wow-look-at-my/go-containers/set"
	"github.com/wow-look-at-my/go-toolchain/src/runner"
)

// siblingRequires returns every module that lives in the same repository as
// the module c describes and is reachable through its requirements, each
// mapped to the pseudo-version it has at c's commit.
//
// A multi-module repository cannot pin itself. Its modules are cut from one
// tree and released together from one commit, but the sibling require inside
// any of them was written BEFORE that commit existed, so it names an earlier
// one -- and at the repository's first publish, one with no such module in it
// at all. That is where "missing go.mod at revision" comes from: not a wrong
// pin, but the only pin a commit can carry about itself.
//
// A replace fixes it inside the repository and nowhere else, since a replace
// is main-module-only. What reaches a consumer is fixed here instead: the
// consumer requires the siblings itself, at the commit the tracked module
// resolved to, and minimal version selection takes the newer of the two. The
// stale pin loses and is never fetched, so it stops mattering what it says.
func siblingRequires(r runner.CommandRunner, c *gitCommit, mainModule string) (map[string]string, error) {
	out := map[string]string{}
	seen := set.Of(c.Subdir)
	queue := []string{c.Subdir}

	for len(queue) > 0 {
		dir := queue[0]
		queue = queue[1:]

		data, err := c.readFile(r, path.Join(dir, "go.mod"))
		if err != nil {
			return nil, err
		}
		f, err := modfile.Parse("go.mod", data, nil)
		if err != nil {
			return nil, fmt.Errorf("parsing %s at %s: %w", path.Join(dir, "go.mod"), c.ShortHash, err)
		}

		for _, req := range f.Require {
			mod := req.Mod.Path
			if !inRepo(mod, c.RepoRoot) || mod == mainModule {
				continue
			}
			sub := moduleSubdir(mod, c.RepoRoot)
			if !seen.Add(sub) {
				continue
			}
			out[mod] = pseudoVersionFor(mod, c.Time, c.ShortHash)
			queue = append(queue, sub)
		}
	}
	return out, nil
}

// inRepo reports whether a module path belongs to the repository whose
// module-path prefix is root. The path is either the root itself or sits
// beneath it; a prefix that stops mid-segment is a different repository whose
// name happens to start the same way.
func inRepo(mod, root string) bool {
	return mod == root || strings.HasPrefix(mod, root+"/")
}
