package cmd

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/wow-look-at-my/go-toolchain/src/logger"
	"github.com/wow-look-at-my/go-toolchain/src/runner"
	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
)

// FixBogusDepsVersions detects dependencies with v0.0.0 versions in go.mod and
// resolves them to actual pseudo-versions. This happens when someone adds a
// git-based dependency without a proper version tag.
func FixBogusDepsVersions(r runner.CommandRunner) error {
	data, err := os.ReadFile("go.mod")
	if err != nil {
		return nil // Let go mod tidy handle missing go.mod
	}

	f, err := modfile.Parse("go.mod", data, nil)
	if err != nil {
		return nil // Let go mod tidy handle parse errors
	}

	var toFix []string
	for _, req := range f.Require {
		if req.Mod.Version == "v0.0.0" {
			toFix = append(toFix, req.Mod.Path)
		}
	}

	if len(toFix) == 0 {
		return nil
	}

	// Resolve each module to its actual latest version
	for _, mod := range toFix {
		if !jsonOutput {
			logger.Info("⇒ Resolving %s (v0.0.0 is not a valid version)", mod)
		}

		version, err := resolveLatestVersionViaGit(r, mod)
		if err != nil {
			return fmt.Errorf("failed to resolve %s: %w", mod, err)
		}

		// Update the require in the parsed file
		if err := f.AddRequire(mod, version); err != nil {
			return fmt.Errorf("failed to update %s: %w", mod, err)
		}
	}

	// Write the updated go.mod
	newData, err := f.Format()
	if err != nil {
		return fmt.Errorf("failed to format go.mod: %w", err)
	}
	if err := os.WriteFile("go.mod", newData, 0644); err != nil {
		return fmt.Errorf("failed to write go.mod: %w", err)
	}

	return nil
}

// resolveLatestVersionViaGit fetches the latest commit from a git repo's
// default branch and constructs a proper pseudo-version with the correct
// timestamp.
func resolveLatestVersionViaGit(r runner.CommandRunner, mod string) (string, error) {
	return resolveVersionViaGit(r, mod, "HEAD")
}

// resolveGitURLAndRef discovers mod's git repository and the ref's ls-remote
// output in one pass. It tries the full module path as the git URL first --
// the common case, a module at its repo's root -- and, only if THAT URL is
// not a reachable repository at all, backs off one path segment at a time
// and retries. A module living in a subdirectory of its repository
// (agentic-loop's go/ submodule is exactly this shape -- the repo also has a
// ts/ directory) needs this: its import path is longer than its actual repo
// root, and everything past the root is a path INSIDE the repo, not part of
// the git URL. Segment-by-segment backoff works for any host -- GitHub and
// Bitbucket's fixed owner/repo shape, GitLab's arbitrarily nested subgroups,
// a self-hosted server -- without a hardcoded table of which hosts have
// which shape, and without ever having to update that table for a host it
// didn't know about.
//
// Backoff triggers ONLY on a git-level failure (the URL isn't a repository:
// git ls-remote exits non-zero). A URL that IS a repository but where ref
// simply doesn't exist there is git ls-remote's normal, successful, empty
// result -- treated as final, not as a reason to keep guessing shorter
// prefixes that could accidentally match an unrelated repository that
// happens to have a branch of the same name.
func resolveGitURLAndRef(r runner.CommandRunner, mod, ref string) (gitURL string, output []byte, err error) {
	parts := strings.Split(mod, "/")
	var lastErr error
	for i := len(parts); i >= 2; i-- {
		url := "https://" + strings.Join(parts[:i], "/")
		proc, runErr := runner.Cmd("git", "ls-remote", url, ref).WithQuiet().Run(r)
		if runErr != nil {
			lastErr = fmt.Errorf("git ls-remote %s failed: %w", url, runErr)
			continue
		}
		out, _ := io.ReadAll(proc.Stdout())
		if waitErr := proc.Wait(); waitErr != nil {
			lastErr = fmt.Errorf("git ls-remote %s failed: %w", url, waitErr)
			continue
		}
		return url, out, nil
	}
	return "", nil, lastErr
}

// gitCommit is one module's repository, the commit a ref points at, and where
// the module sits inside it. The commit is fetched into a temporary bare
// repository, so anything else committed alongside the module -- a sibling
// module's go.mod, say -- can be read at the same commit without a checkout.
type gitCommit struct {
	// URL is the repository, e.g. https://github.com/org/repo.
	URL string
	// RepoRoot is URL without its scheme, which is the module-path prefix
	// every module in the repository shares.
	RepoRoot string
	// Subdir is the module's directory inside the repository, empty when the
	// module is the repository root.
	Subdir string

	Hash      string
	ShortHash string
	Time      time.Time

	dir string
}

// fetchCommit resolves ref (a branch, "HEAD", or any other ref git ls-remote
// accepts) to a commit and fetches it. The returned cleanup removes the
// temporary repository and must be called; the gitCommit's tree is unreadable
// afterward.
func fetchCommit(r runner.CommandRunner, mod, ref string) (*gitCommit, func(), error) {
	gitURL, output, err := resolveGitURLAndRef(r, mod, ref)
	if err != nil {
		return nil, nil, fmt.Errorf("git ls-remote failed: %w", err)
	}

	fields := strings.Fields(string(output))
	if len(fields) < 1 {
		return nil, nil, fmt.Errorf("no ref %q found for %s", ref, mod)
	}
	return fetchAt(r, mod, gitURL, fields[0])
}

// fetchCommitAt fetches one named commit of a module's repository, for a
// version that already names one. The ls-remote is still what finds where the
// repository stops and the module's subdirectory inside it starts.
func fetchCommitAt(r runner.CommandRunner, mod, hash string) (*gitCommit, func(), error) {
	gitURL, _, err := resolveGitURLAndRef(r, mod, "HEAD")
	if err != nil {
		return nil, nil, fmt.Errorf("git ls-remote failed: %w", err)
	}
	return fetchAt(r, mod, gitURL, hash)
}

// fetchAt fetches a known commit into a temporary bare repository.
func fetchAt(r runner.CommandRunner, mod, gitURL, fullHash string) (*gitCommit, func(), error) {
	if len(fullHash) < 12 {
		return nil, nil, fmt.Errorf("invalid commit hash: %s", fullHash)
	}
	root := strings.TrimPrefix(gitURL, "https://")
	c := &gitCommit{
		URL:       gitURL,
		RepoRoot:  root,
		Subdir:    moduleSubdir(mod, root),
		Hash:      fullHash,
		ShortHash: fullHash[:12],
	}

	// Shallow fetch just the commit: --depth=1 still carries its whole tree.
	tmpDir, err := os.MkdirTemp("", "resolve-*")
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() { os.RemoveAll(tmpDir) }
	c.dir = tmpDir

	if err := runGitQuiet(r, "git", "-C", tmpDir, "init", "--bare"); err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("git init failed: %w", err)
	}
	if err := runGitQuiet(r, "git", "-C", tmpDir, "fetch", "--depth=1", gitURL, fullHash); err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("git fetch failed: %w", err)
	}

	// Commit timestamp as a Unix epoch, which is timezone-free.
	tsOutput, err := gitOutput(r, "git", "-C", tmpDir, "log", "-1", "--format=%ct", fullHash)
	if err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("git log failed: %w", err)
	}
	epochStr := strings.TrimSpace(string(tsOutput))
	epoch, err := strconv.ParseInt(epochStr, 10, 64)
	if err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("invalid timestamp: %s", epochStr)
	}
	c.Time = time.Unix(epoch, 0)
	return c, cleanup, nil
}

// moduleSubdir returns the directory mod occupies inside the repository whose
// module-path prefix is root. A "/vN" major-version suffix names no directory
// -- github.com/org/repo/go/core/v2 lives in go/core -- so it is trimmed first.
func moduleSubdir(mod, root string) string {
	prefix, pathMajor, ok := module.SplitPathVersion(mod)
	if ok && pathMajor != "" {
		mod = prefix
	}
	return strings.Trim(strings.TrimPrefix(mod, root), "/")
}

// readFile returns the contents of a path inside the repository at this
// commit. An empty relative path reads the repository root itself, which is
// how a module at the root reads its own go.mod.
func (c *gitCommit) readFile(r runner.CommandRunner, rel string) ([]byte, error) {
	out, err := gitOutput(r, "git", "-C", c.dir, "cat-file", "-p", c.Hash+":"+rel)
	if err != nil {
		return nil, fmt.Errorf("reading %s at %s: %w", rel, c.ShortHash, err)
	}
	return out, nil
}

// runGitQuiet runs a command that produces no output worth keeping.
func runGitQuiet(r runner.CommandRunner, name string, args ...string) error {
	proc, err := runner.Cmd(name, args...).WithQuiet().Run(r)
	if err != nil {
		return err
	}
	return proc.Wait()
}

// gitOutput runs a command and returns its stdout.
func gitOutput(r runner.CommandRunner, name string, args ...string) ([]byte, error) {
	proc, err := runner.Cmd(name, args...).WithQuiet().Run(r)
	if err != nil {
		return nil, err
	}
	out, _ := io.ReadAll(proc.Stdout())
	if err := proc.Wait(); err != nil {
		return nil, err
	}
	return out, nil
}

// resolveVersionViaGit fetches the commit ref points at (a branch, "HEAD",
// or any other ref git ls-remote accepts) and constructs a proper
// pseudo-version with the correct timestamp.
func resolveVersionViaGit(r runner.CommandRunner, mod, ref string) (string, error) {
	c, cleanup, err := fetchCommit(r, mod, ref)
	if err != nil {
		return "", err
	}
	defer cleanup()
	return pseudoVersionFor(mod, c.Time, c.ShortHash), nil
}

// pseudoVersionFor builds the pseudo-version for mod at a commit. The major
// version comes from the module path: a "/vN" (or gopkg.in ".vN") suffix
// demands a matching vN pseudo-version, and the go command rejects anything
// else with `go.mod has post-v0 module path "..." at revision ...`. A path
// with no suffix gets v0.
func pseudoVersionFor(mod string, commitTime time.Time, shortHash string) string {
	_, pathMajor, _ := module.SplitPathVersion(mod)
	return module.PseudoVersion(module.PathMajorPrefix(pathMajor), "", commitTime, shortHash)
}
