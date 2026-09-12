package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
)

// A cached directive has no input: a module zip carries a gitlink, not the
// submodule under it. So the clone is where it runs.

// moduleSource is a dependency to generate from, and where its cache sits.
type moduleSource struct {
	// Path is the module path, without a version.
	Path string
	// Commit is what the pseudo-version names.
	Commit string
	// Root is the module's directory in the cache.
	Root string
}

// splitVersion separates a cache directory name into its module path and its
// version.
func splitVersion(dir string) (path, version string) {
	base := filepath.ToSlash(dir)
	at := strings.LastIndex(base, "@")
	if at < 0 {
		return "", ""
	}
	return base[:at], base[at+1:]
}

// originHash asks the go command for the module version's full commit.
//
// The module metadata holds the whole hash, so the build generates from the
// commit it resolved rather than from whatever a branch points at now.
func originHash(path, version string) string {
	out, err := goOutput("list", "-m", "-json", path+"@"+version)
	if err != nil {
		return ""
	}
	var mod struct {
		Origin struct {
			Hash string
			URL  string
		}
	}
	if err := json.Unmarshal([]byte(out), &mod); err != nil {
		return ""
	}
	return mod.Origin.Hash
}

// generateInClone clones src at its commit, runs the directives there, and
// copies what they wrote into the cache.
func generateInClone(src moduleSource, directives []generateDirective, quiet bool) error {
	work, err := os.MkdirTemp("", "go-toolchain-depgen-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(work) }()

	clone := filepath.Join(work, "src")
	if err := os.MkdirAll(clone, 0o755); err != nil {
		return err
	}
	// A single commit and a single level of submodule, fetched by SHA.
	url := "https://" + strings.TrimSuffix(src.Path, "/")
	ref := src.Commit
	if ref == "" {
		ref = "HEAD"
	}
	steps := [][]string{
		{"init", "--quiet"},
		{"remote", "add", "origin", url},
		{"fetch", "--quiet", "--depth", "1", "origin", ref},
		{"checkout", "--quiet", "FETCH_HEAD"},
		{"submodule", "update", "--init", "--recursive", "--depth", "1", "--quiet"},
	}
	for _, step := range steps {
		if err := runGit(clone, step...); err != nil {
			return fmt.Errorf("%s at %s: %w", src.Path, ref, err)
		}
	}
	for _, d := range directives {
		// Slash-spelled, so the relative part is a prefix trim. See goModCache.
		rel := strings.TrimPrefix(path.Dir(filepath.ToSlash(d.File)), src.Root+"/")
		local := generateDirective{
			File:    filepath.Join(clone, filepath.FromSlash(rel), path.Base(d.File)),
			Line:    d.Line,
			Command: d.Command,
			Label:   d.Label,
		}
		if err := executeDirective(local, quiet); err != nil {
			return err
		}
		out := generatedOutput(d.Command)
		if out == "" {
			continue
		}
		if err := copyGenerated(filepath.Join(clone, filepath.FromSlash(rel)), path.Dir(filepath.ToSlash(d.File)), out); err != nil {
			return err
		}
	}
	return nil
}

// copyGenerated puts the named output, and the table blob beside it, into the
// cache directory the compiler reads.
func copyGenerated(from, to, out string) error {
	return withWritableDir(to, func() error {
		for _, name := range generatedSiblings(from, out) {
			body, err := os.ReadFile(filepath.Join(from, name))
			if err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(to, name), body, 0o444); err != nil {
				return err
			}
		}
		return nil
	})
}

// generatedSiblings names what a run produced: the output the directive names,
// and the data file it embeds. An embed of a missing file does not compile, so
// the blob travels with the loader that reads it.
func generatedSiblings(dir, out string) []string {
	names := []string{out}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return names
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".zst") {
			names = append(names, e.Name())
		}
	}
	return names
}

// runGit runs git in dir, quietly.
func runGit(dir string, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// byModule groups directives by the cached module they belong to.
func byModule(cache string, directives []generateDirective) []moduleSource {
	order := []string{}
	roots := map[string][]generateDirective{}
	for _, d := range directives {
		root := moduleRootOf(cache, d.File)
		if root == "" {
			continue
		}
		if _, seen := roots[root]; !seen {
			order = append(order, root)
		}
		roots[root] = append(roots[root], d)
	}
	var out []moduleSource
	for _, root := range order {
		modPath, version := splitVersion(root)
		if modPath == "" {
			continue
		}
		mod := strings.TrimPrefix(modPath, cache+"/")
		out = append(out, moduleSource{
			Path:   mod,
			Commit: originHash(mod, version),
			Root:   root,
		})
	}
	return out
}

// moduleRootOf finds the module directory a cached file sits under: the a
// single whose name carries the version.
func moduleRootOf(cache, file string) string {
	// path, not filepath: every path here is slash-spelled. See goModCache.
	dir := path.Dir(filepath.ToSlash(file))
	for strings.HasPrefix(dir, cache) && dir != cache {
		if strings.Contains(path.Base(dir), "@") {
			return dir
		}
		dir = path.Dir(dir)
	}
	return ""
}
