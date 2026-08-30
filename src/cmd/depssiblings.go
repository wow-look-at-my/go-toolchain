package cmd

import (
	"fmt"
	"path"
	"strings"

	"golang.org/x/mod/modfile"

	"github.com/wow-look-at-my/go-containers/set"
	"github.com/wow-look-at-my/go-toolchain/src/runner"
)

// siblingRequires returns every module in the same repository as c, reachable
// through its requirements, mapped to its pseudo-version at c's commit.
//
// A multi-module repo can't pin itself: a sibling require is written before
// the commit it names exists, so it points at an earlier commit -- at a
// maiden publish, a commit lacking the module entirely ("missing go.mod at revision").
// A replace fixes this only inside the repo. This resolves it for a consumer
// instead: the consumer requires the siblings directly at the resolved
// commit, and minimal version selection picks that over the stale pin.
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

// inRepo: mod is root itself, or root plus a "/" boundary, not just a name prefix.
func inRepo(mod, root string) bool {
	return mod == root || strings.HasPrefix(mod, root+"/")
}
