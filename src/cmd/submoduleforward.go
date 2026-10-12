package cmd

import (
	"fmt"
	"os"
	"path"
	"strings"
	"sync"

	"github.com/wow-look-at-my/go-toolchain/src/logger"
	"github.com/wow-look-at-my/go-toolchain/src/runner"
)

// A submodule pin only moves forward. A gitlink on HEAD must be the pin on the
// default branch of origin, or a commit that descends from it.

var submodulesForwardOnce struct {
	sync.Once
	err error
}

// checkSubmodulesForward runs checkSubmodulesForwardIn once per process, on
// the repository that holds root.
func checkSubmodulesForward(r runner.CommandRunner, root string) error {
	submodulesForwardOnce.Do(func() {
		submodulesForwardOnce.err = checkSubmodulesForwardIn(r, root)
	})
	return submodulesForwardOnce.err
}

// checkSubmodulesForwardIn fails when a gitlink on HEAD does not descend from
// the gitlink at the same path on the default branch of origin.
func checkSubmodulesForwardIn(r runner.CommandRunner, root string) error {
	out, err := gitOutput(r, "git", "-C", root, "rev-parse", "--show-toplevel")
	if err != nil {
		logger.Output("⇒ submodule pins: %s is not in a git repository, so there are no pins", root)
		return nil
	}
	top := strings.TrimSpace(string(out))
	if _, err := gitOutput(r, "git", "-C", top, "rev-parse", "--verify", "--quiet", "HEAD"); err != nil {
		logger.Output("⇒ submodule pins: HEAD has no commit yet, so there are no pins")
		return nil
	}
	head, err := gitlinks(r, top, "HEAD")
	if err != nil {
		return fmt.Errorf("submodule pins: read HEAD: %w", err)
	}
	if len(head) == 0 {
		return nil
	}
	originURL, err := gitOutput(r, "git", "-C", top, "remote", "get-url", "origin")
	if err != nil {
		logger.Output("⇒ submodule pins: no origin remote, so no default branch to compare with")
		return nil
	}
	origin := strings.TrimSpace(string(originURL))
	lsOut, err := gitOutput(r, "git", "-C", top, "ls-remote", "--symref", "origin", "HEAD")
	if err != nil {
		return fmt.Errorf("submodule pins: read the default branch of %s: %w", origin, err)
	}
	refs, branch := parseLsRemoteRefs(lsOut)
	baseCommit := refs["HEAD"]
	if baseCommit == "" {
		return fmt.Errorf("submodule pins: %s names no HEAD", origin)
	}
	auth, err := gitAuthArgs(r, top)
	if err != nil {
		return err
	}
	base, err := baseGitlinks(r, top, origin, baseCommit, auth)
	if err != nil {
		return fmt.Errorf("submodule pins: read %s at %s: %w", branch, baseCommit, err)
	}
	var problems []string
	for p, commit := range head {
		was, ok := base[p]
		if !ok || was == commit {
			continue
		}
		url, err := submoduleURL(r, top, p, origin)
		if err != nil {
			return err
		}
		if err := requireDescendant(r, url, was, commit, auth); err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", p, err))
		}
	}
	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("submodule pins move backward:\n  %s\n\n"+
		"A submodule pin only moves forward. Point each one at the pin on %s, or at a commit that descends from it",
		strings.Join(problems, "\n  "), branch)
}

// gitlinks maps each submodule path in the tree of commit to its pinned commit.
func gitlinks(r runner.CommandRunner, dir, commit string) (map[string]string, error) {
	out, err := gitOutput(r, "git", "-C", dir, "ls-tree", "-r", "-z", commit)
	if err != nil {
		return nil, err
	}
	links := map[string]string{}
	for _, entry := range strings.Split(string(out), "\x00") {
		meta, p, ok := strings.Cut(entry, "\t")
		if !ok {
			continue
		}
		if fields := strings.Fields(meta); len(fields) == 3 && fields[1] == "commit" {
			links[p] = fields[2]
		}
	}
	return links, nil
}

// baseGitlinks reads the gitlinks of commit from top when top holds it, and
// from a scratch fetch of origin otherwise. The scratch fetch leaves top as it was.
func baseGitlinks(r runner.CommandRunner, top, origin, commit string, auth []string) (map[string]string, error) {
	if _, err := gitOutput(r, "git", "-C", top, "cat-file", "-e", commit+"^{commit}"); err == nil {
		return gitlinks(r, top, commit)
	}
	scratch, err := scratchRepo(r, origin, "blob:none")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(scratch)
	if _, err := gitOutput(r, "git", append(auth, "-C", scratch, "fetch", "--quiet", "--no-tags", "--depth=1", "origin", commit)...); err != nil {
		return nil, err
	}
	return gitlinks(r, scratch, commit)
}

// requireDescendant fails unless commit descends from base in the repository at url.
func requireDescendant(r runner.CommandRunner, url, base, commit string, auth []string) error {
	scratch, err := scratchRepo(r, url, "tree:0")
	if err != nil {
		return err
	}
	defer os.RemoveAll(scratch)
	if _, err := gitOutput(r, "git", append(auth, "-C", scratch, "fetch", "--quiet", "--no-tags", "origin", base, commit)...); err != nil {
		return fmt.Errorf("fetch %s and %s from %s: %w", base, commit, url, err)
	}
	if _, err := gitOutput(r, "git", "-C", scratch, "merge-base", "--is-ancestor", base, commit); err == nil {
		return nil
	}
	if _, err := gitOutput(r, "git", "-C", scratch, "merge-base", "--is-ancestor", commit, base); err == nil {
		return fmt.Errorf("%s is older than %s, the pin on the default branch", commit, base)
	}
	return fmt.Errorf("%s does not descend from %s, the pin on the default branch", commit, base)
}

// scratchRepo makes a bare repository in a temporary directory whose origin is
// url, set to fetch with the given object filter.
func scratchRepo(r runner.CommandRunner, url, filter string) (string, error) {
	dir, err := os.MkdirTemp("", "go-toolchain-pins-")
	if err != nil {
		return "", err
	}
	steps := [][]string{
		{"init", "--quiet", "--bare"},
		{"config", "core.repositoryformatversion", "1"},
		{"config", "extensions.partialClone", "origin"},
		{"config", "remote.origin.url", url},
		{"config", "remote.origin.promisor", "true"},
		{"config", "remote.origin.partialclonefilter", filter},
	}
	for _, args := range steps {
		if _, err := gitOutput(r, "git", append([]string{"-C", dir}, args...)...); err != nil {
			os.RemoveAll(dir)
			return "", err
		}
	}
	return dir, nil
}

// gitAuthArgs carries the http.*.extraheader entries of top to a scratch fetch.
// actions/checkout writes its token there, in the local config of the checkout.
func gitAuthArgs(r runner.CommandRunner, top string) ([]string, error) {
	out, err := gitOutput(r, "git", "-C", top, "config", "--local", "--get-regexp", `^http\..*\.extraheader$`)
	if err != nil {
		return nil, nil
	}
	var args []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		key, value, ok := strings.Cut(line, " ")
		if ok {
			args = append(args, "-c", key+"="+value)
		}
	}
	return args, nil
}

// submoduleURL is the url .gitmodules gives the submodule at p. A relative url
// is resolved against origin, as git does.
func submoduleURL(r runner.CommandRunner, top, p, origin string) (string, error) {
	out, err := gitOutput(r, "git", "-C", top, "config", "-f", ".gitmodules", "--get-regexp", `^submodule\..*\.path$`)
	if err != nil {
		return "", fmt.Errorf("submodule pins: %s has no entry in .gitmodules: %w", p, err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		key, value, ok := strings.Cut(line, " ")
		if !ok || value != p {
			continue
		}
		name := strings.TrimSuffix(strings.TrimPrefix(key, "submodule."), ".path")
		url, err := gitOutput(r, "git", "-C", top, "config", "-f", ".gitmodules", "--get", "submodule."+name+".url")
		if err != nil {
			return "", fmt.Errorf("submodule pins: %s has no url in .gitmodules: %w", p, err)
		}
		return resolveSubmoduleURL(strings.TrimSpace(string(url)), origin), nil
	}
	return "", fmt.Errorf("submodule pins: %s has no entry in .gitmodules", p)
}

// resolveSubmoduleURL resolves a ./ or ../ url against the origin url.
func resolveSubmoduleURL(url, origin string) string {
	if !strings.HasPrefix(url, "./") && !strings.HasPrefix(url, "../") {
		return url
	}
	scheme, rest, ok := strings.Cut(origin, "://")
	if !ok {
		return path.Join(origin, url)
	}
	return scheme + "://" + path.Join(rest, url)
}
