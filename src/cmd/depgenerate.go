package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"runtime"

	"github.com/wow-look-at-my/go-containers/set"
	"github.com/wow-look-at-my/go-toolchain/src/gomod"
	"github.com/wow-look-at-my/go-toolchain/src/hostos"
	"github.com/wow-look-at-my/go-toolchain/src/logger"
)

// Go has no build step for a dependency, so a package whose data a generate
// step writes ships without it: the module carries the directive and the
// tracked half, and the generated half is absent. The package compiles and
// panics naming the step. Nothing else in the toolchain runs it, which leaves
// the consumer with a dependency it can build and cannot call.

// depGenerateDirectives returns the directives of every dependency package
// this module builds against, read from the module cache.
func depGenerateDirectives() ([]generateDirective, error) {
	cache, err := goModCache()
	if err != nil || cache == "" {
		return nil, err
	}
	var out []generateDirective
	for _, dir := range depPackageDirs(cache) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
				continue
			}
			path := dir + "/" + e.Name()
			found, err := parseDirectives(path)
			if err != nil {
				continue
			}
			for _, d := range found {
				// The label is what the hash reads, and it carries no version: a dependency bump whose directives did not change
				d.Label = cacheLabel(cache, path)
				out = append(out, d)
			}
		}
	}
	return out, nil
}

// depPackageDirs reads every package directory of every dependency MODULE, and
// not just the ones an import reaches. Generating is what changes which ones an
// import reaches, and a set that grows as it is satisfied cannot be approved.
func depPackageDirs(cache string) []string {
	seen := set.New[string]()
	var dirs []string
	for _, root := range depModuleDirs(cache) {
		filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil || !d.IsDir() {
				return nil
			}
			if path != root && skipDepDir(d.Name()) {
				return filepath.SkipDir
			}
			if path != root && gomod.IsNestedModule(path) {
				return filepath.SkipDir
			}
			if !seen.Contains(path) && hasGoFile(path) {
				seen.Add(path)
				dirs = append(dirs, path)
			}
			return nil
		})
	}
	return dirs
}

// depDirSkips name directories holding no package of the module.
var depDirSkips = set.Of("testdata", "vendor", "node_modules")

// skipDepDir reports whether the walk stops at a directory of this name.
func skipDepDir(name string) bool {
	return strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") || depDirSkips.Contains(name)
}

// hasGoFile reports whether a directory holds Go source.
func hasGoFile(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".go") {
			return true
		}
	}
	return false
}

// depModuleDirs names the cached root of every module this one builds against.
func depModuleDirs(cache string) []string {
	out, err := goOutput("list", "-deps", "-f", "{{with .Module}}{{.Dir}}{{end}}", "./...")
	if err != nil {
		return nil
	}
	seen := set.New[string]()
	var dirs []string
	for _, line := range strings.Split(out, "\n") {
		dir := slashPath(strings.TrimSpace(line))
		if dir == "" || !strings.HasPrefix(dir, cache) || seen.Contains(dir) {
			continue
		}
		seen.Add(dir)
		dirs = append(dirs, dir)
	}
	return dirs
}

// cacheLabel names a cached file by its module path and the file inside it,
// with the version dropped.
func cacheLabel(cache, path string) string {
	rel, err := filepath.Rel(cache, path)
	if err != nil {
		return path
	}
	rel = slashPath(rel)
	mod, rest, found := strings.Cut(rel, "@")
	if !found {
		return rel
	}
	_, inside, found := strings.Cut(rest, "/")
	if !found {
		return mod
	}
	return mod + "/" + inside
}

// slashPathFor reads the HOST, not the compile target. Depth: docs/PIPELINE.md
func slashPathFor(hostGOOS, p string) string {
	if hostGOOS == "windows" {
		return strings.ReplaceAll(p, "\\", "/")
	}
	// Not filepath.ToSlash: that reads the compile target too.
	return p
}

// slashPath spells p with forward slashes, for this host.
func slashPath(p string) string { return slashPathFor(hostos.GOOS(), p) }

// goModCache answers the cache directory, SLASH-SPELLED. Depth: docs/PIPELINE.md
func goModCache() (string, error) {
	out, err := goOutput("env", "GOMODCACHE")
	if err != nil {
		return "", err
	}
	return slashPath(strings.TrimSpace(out)), nil
}

// goOutput runs the go command for its stdout, under this host target: what it
// reports has to describe the build that runs here.
func goOutput(args ...string) (string, error) {
	cmd := exec.Command("go", args...)
	cmd.Env = append(os.Environ(), "GOOS="+hostos.GOOS(), "GOARCH="+runtime.GOARCH)
	out, err := cmd.Output()
	return string(out), err
}

// withWritableDir makes dir writable for the length of fn and puts its mode
// back. The module cache is read only by design, and a directive writes its
// output beside the file that declares it.
func withWritableDir(dir string, fn func() error) error {
	info, err := os.Stat(dir)
	if err != nil {
		return fn()
	}
	mode := info.Mode().Perm()
	if mode&0o200 != 0 {
		return fn()
	}
	if err := os.Chmod(dir, mode|0o200); err != nil {
		return fn()
	}
	defer func() { _ = os.Chmod(dir, mode) }()
	return fn()
}

// inModCache reports whether dir sits under the module cache.
func inModCache(dir string) bool {
	cache, err := goModCache()
	if err != nil || cache == "" {
		return false
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return false
	}
	return strings.HasPrefix(slashPath(abs), cache)
}

// generatedOutput names the file a directive writes, for the -out flag the
// translate tools take. It is empty when the command names no output.
func generatedOutput(command string) string {
	args, err := splitGenerateCommand(command)
	if err != nil {
		return ""
	}
	for i, a := range args {
		if a == "-out" && i+1 < len(args) {
			return args[i+1]
		}
		if rest, ok := strings.CutPrefix(a, "-out="); ok {
			return rest
		}
	}
	return ""
}

// owesOutput reports that a directive names a file it writes and that the file
// is not there.
func owesOutput(d generateDirective) bool {
	out := generatedOutput(d.Command)
	if out == "" {
		return false
	}
	_, err := os.Stat(filepath.Join(filepath.Dir(d.File), out))
	return os.IsNotExist(err)
}

// pendingDepDirectives keeps the dependency directives still owed their output.
func pendingDepDirectives(all []generateDirective) []generateDirective {
	var out []generateDirective
	for _, d := range all {
		if owesOutput(d) {
			out = append(out, d)
		}
	}
	return out
}

// generateForDeps runs the directives the dependencies still owe, ahead of every
// phase that reads what they produce.
//
// The comment scan leads the pipeline on purpose: it reads bytes, so it answers
// on a tree no compiler accepts. Its rule is an imported package whose extractor
// decodes a parse table, though, and a dependency ships the directive that
// writes that table rather than the table. So this runs and it runs before go
// mod tidy: `go list` resolves an import without type-checking it, which is what
// lets a package that does not compile yet name its own directory.
func generateForDeps(expectedHash string) error {
	deps, err := depGenerateDirectives()
	if err != nil {
		return nil
	}
	pending := pendingDepDirectives(deps)
	if len(pending) == 0 {
		return nil
	}
	own, err := findGenerateDirectives(".")
	if err != nil {
		return nil
	}
	hash := approvalHash(own, deps)
	if expectedHash == "skip" {
		logger.Warn("⇒ Warning: a dependency still owes its generated output, and generate is skipped")
		return nil
	}
	if expectedHash == "" || expectedHash != hash {
		logger.Info("%s", colorYellow+"    A dependency owes its generated output (not executed):"+colorReset)
		for _, d := range pending {
			logger.Info("\t%s:%d: %s%s%s", d.Label, d.Line, colorYellow, d.Command, colorReset)
		}
		logger.Info("\n%sTo run these commands, record the approval: echo %s > %s%s", colorYellow, hash, generateApprovalFile, colorReset)
		return fmt.Errorf("a dependency's generate commands require approval: record %s in %s", hash, generateApprovalFile)
	}
	return satisfyDepDirectives(pending)
}

// satisfyDepDirectives writes what the pending dependency directives owe, by
// generating in a clone of each module and copying the result into its cache
// directory. It is where every caller ends up: the directive cannot run in the
// cache, whichever pass noticed the gap.
func satisfyDepDirectives(pending []generateDirective) error {
	if len(pending) == 0 {
		return nil
	}
	cache, err := goModCache()
	if err != nil {
		return err
	}
	mods := byModule(cache, pending)
	if len(mods) == 0 {
		// Silence leaves the output owed and the run green.
		return fmt.Errorf("%d dependency directive(s) owe output and none resolved to a module under %s: first is %s", len(pending), cache, pending[0].File)
	}
	st := logStep("go generate (dependencies)")
	for _, mod := range mods {
		if err := generateInClone(mod, directivesUnder(mod.Root, pending), false); err != nil {
			st.done()
			return fmt.Errorf("dependency generate failed for %s: %w", mod.Path, err)
		}
	}
	// A cached module is indexed as immutable: a later write stays invisible.
	disableGoModuleIndex()
	st.noteOutput()
	st.done()
	return nil
}

// directivesUnder keeps the directives belonging to a single cached module.
func directivesUnder(root string, all []generateDirective) []generateDirective {
	var out []generateDirective
	for _, d := range all {
		if strings.HasPrefix(d.File, root) {
			out = append(out, d)
		}
	}
	return out
}

// approvalHash is the value --generate takes, over this tree's directives and
// every dependency directive that names an output.
func approvalHash(own, deps []generateDirective) string {
	return computeDirectivesHash(append(append([]generateDirective(nil), own...), declaresOutput(deps)...))
}

// declaresOutput keeps the directives that name a file they write.
func declaresOutput(all []generateDirective) []generateDirective {
	var out []generateDirective
	for _, d := range all {
		if generatedOutput(d.Command) != "" {
			out = append(out, d)
		}
	}
	return out
}
