package cmd

import (
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/wow-look-at-my/go-toolchain/src/logger"
	"github.com/wow-look-at-my/go-toolchain/src/runner"
	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
)

// FixBogusDepsVersions detects dependencies with a placeholder version in go.mod and
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

// resolveLatestVersionViaGit builds a pseudo-version from the repo's
// default-branch HEAD commit and its timestamp.
func resolveLatestVersionViaGit(r runner.CommandRunner, mod string) (string, error) {
	return resolveVersionViaGit(r, mod, "HEAD")
}

// resolveGitURLAndRef discovers mod's git repository and the refs' ls-remote
// output in a single pass, trying the full module path as the git URL, then
// backing off a path segment at a time -- handling a module in a
// subdirectory of its repository, for any host, with no hardcoded table.
// Backoff triggers only on a git-level failure, not an empty (but reachable)
// ls-remote result. Asking for HEAD adds --symref, which also reports the
// branch it points at. Every ref is asked in the same question, which is how a
// bare marker learns the default branch and whether a matching branch exists
// without paying for a further round trip. On total failure, the EARLIEST
// error (the full module path) is reported.
func resolveGitURLAndRef(r runner.CommandRunner, mod string, refs ...string) (gitURL string, output []byte, err error) {
	parts := strings.Split(mod, "/")
	var firstErr error
	keep := func(e error) {
		if firstErr == nil {
			firstErr = e
		}
	}
	for i := len(parts); i >= 2; i-- {
		url := "https://" + strings.Join(parts[:i], "/")
		args := []string{"ls-remote"}
		if slices.Contains(refs, "HEAD") {
			args = append(args, "--symref")
		}
		args = append(append(args, url), refs...)
		proc, runErr := runner.Cmd("git", args...).WithQuiet().Run(r)
		if runErr != nil {
			keep(fmt.Errorf("git ls-remote %s failed: %w", url, runErr))
			continue
		}
		out, _ := io.ReadAll(proc.Stdout())
		if waitErr := proc.Wait(); waitErr != nil {
			keep(fmt.Errorf("git ls-remote %s failed: %w", url, waitErr))
			continue
		}
		return url, out, nil
	}
	return "", nil, firstErr
}

// gitCommit is a module's repository, the commit a ref points at, and where
// the module sits inside it. The commit is fetched into a temporary bare
// repository, so anything else committed alongside the module -- a sibling
// module's go.mod, say -- can be read at the same commit without a checkout.
type gitCommit struct {
	// URL is the repository, e.g. https://github.com/org/repo.
	URL string
	// RepoRoot is URL without its scheme, the shared module-path prefix.
	RepoRoot string
	// Subdir is the module's directory inside the repository; empty at the repo root.
	Subdir string

	Hash      string
	ShortHash string
	Time      time.Time

	dir string
}

// parseLsRemote reads an answer covering a SINGLE ref: its commit, and the branch a
// symbolic HEAD resolves to (`ref: refs/heads/master\tHEAD`).
func parseLsRemote(out []byte) (hash, branch string) {
	branch = eachLsRemoteRef(out, func(h, _ string) {
		if hash == "" {
			hash = h
		}
	})
	return hash, branch
}

// parseLsRemoteRefs reads an answer covering several refs: each ref's commit
// by its full name, and the branch a symbolic HEAD resolves to.
func parseLsRemoteRefs(out []byte) (refs map[string]string, branch string) {
	refs = map[string]string{}
	branch = eachLsRemoteRef(out, func(h, ref string) { refs[ref] = h })
	return refs, branch
}

// eachLsRemoteRef walks the answer's ref lines and reports the symbolic HEAD's branch.
func eachLsRemoteRef(out []byte, fn func(hash, ref string)) (branch string) {
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if rest, ok := strings.CutPrefix(line, "ref: "); ok {
			if fields := strings.Fields(rest); len(fields) > 0 {
				branch = strings.TrimPrefix(fields[0], "refs/heads/")
			}
			continue
		}
		switch fields := strings.Fields(line); len(fields) {
		case 0:
		case 1:
			fn(fields[0], "")
		default:
			fn(fields[0], fields[1])
		}
	}
	return branch
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

	hash, _ := parseLsRemote(output)
	if hash == "" {
		return nil, nil, fmt.Errorf("no ref %q found for %s", ref, mod)
	}
	return fetchAt(r, mod, gitURL, hash)
}

// defaultBranchOf reports the branch a module's repository HEAD points at.
func defaultBranchOf(r runner.CommandRunner, mod string) (string, error) {
	_, output, err := resolveGitURLAndRef(r, mod, "HEAD")
	if err != nil {
		return "", err
	}
	_, branch := parseLsRemote(output)
	if branch == "" {
		return "", fmt.Errorf("%s reported no symbolic HEAD", mod)
	}
	return branch, nil
}

// fetchCommitAt fetches the named commit of a module's repository, for a
// version that already names a commit. The ls-remote is still what finds where the
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

	// The shallowest fetch of the commit still carries its whole tree.
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

// moduleSubdir returns the directory mod occupies inside the repository
// whose module-path prefix is root. A major-version suffix is trimmed, since
// it names no directory.
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
	stderr, _ := io.ReadAll(proc.Stderr())
	return withGitStderr(proc.Wait(), stderr)
}

// withGitStderr attaches what git said to a failure. WithQuiet() sends stderr
// nowhere, so a bare exit status was the whole report.
func withGitStderr(err error, stderr []byte) error {
	if err == nil {
		return nil
	}
	if msg := strings.TrimSpace(string(stderr)); msg != "" {
		return fmt.Errorf("%w: %s", err, msg)
	}
	return err
}

// gitOutput runs a command and returns its stdout.
func gitOutput(r runner.CommandRunner, name string, args ...string) ([]byte, error) {
	proc, err := runner.Cmd(name, args...).WithQuiet().Run(r)
	if err != nil {
		return nil, err
	}
	out, _ := io.ReadAll(proc.Stdout())
	stderr, _ := io.ReadAll(proc.Stderr())
	if err := withGitStderr(proc.Wait(), stderr); err != nil {
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

// pseudoVersionFor builds the pseudo-version for mod at a commit. A "/vN" (or
// gopkg.in ".vN") path suffix demands a matching vN pseudo-version, or go mod
// rejects it with "post-v0 module path ... at revision". No suffix means v0.
func pseudoVersionFor(mod string, commitTime time.Time, shortHash string) string {
	_, pathMajor, _ := module.SplitPathVersion(mod)
	return module.PseudoVersion(module.PathMajorPrefix(pathMajor), "", commitTime, shortHash)
}
