package cosmocompat

import (
	"fmt"
	"go/build"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/mod/modfile"
)

// downloadModule fetches the exact requested version into GOMODCACHE,
// running with dir as the working directory so it resolves through the
// consumer's own GOPROXY/auth configuration, and returns the module's
// source directory. It is independent of any go.work the caller may build
// afterward -- this always runs before GOWORK is set for this process tree.
func downloadModule(dir, module, version string) (string, error) {
	ref := module + "@" + version
	cmd := exec.Command("go", "mod", "download", "-json", ref)
	cmd.Dir = dir
	cmd.Env = os.Environ()
	out, err := cmd.Output()
	if err != nil {
		var stderr string
		if ee, ok := err.(*exec.ExitError); ok {
			stderr = string(ee.Stderr)
		}
		return "", fmt.Errorf("cosmocompat: go mod download %s: %w\n%s", ref, err, stderr)
	}
	// The JSON output's "Dir" field is on its own line; avoid a JSON
	// dependency for one field by scanning for it directly (matches every
	// "go mod download -json" output format seen across Go versions).
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, `"Dir":`) {
			v := strings.TrimPrefix(line, `"Dir":`)
			v = strings.TrimSpace(v)
			v = strings.Trim(v, `",`)
			return v, nil
		}
	}
	return "", fmt.Errorf("cosmocompat: go mod download %s: no Dir in output:\n%s", ref, out)
}

func freshCopy(src, dst string) error {
	if err := os.RemoveAll(dst); err != nil {
		return err
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	return copyTree(src, dst)
}

func copyTree(src, dst string) error {
	entries, err := os.ReadDir(src)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	for _, e := range entries {
		s := filepath.Join(src, e.Name())
		d := filepath.Join(dst, e.Name())
		if e.IsDir() {
			if err := copyTree(s, d); err != nil {
				return err
			}
			continue
		}
		data, err := os.ReadFile(s)
		if err != nil {
			return err
		}
		if err := os.WriteFile(d, data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// addCosmoFile copies an existing platform file within the module to a new
// cosmo-tagged sibling. The copy's //go:build line -- wherever it falls in
// the leading comment block -- is replaced outright with "//go:build
// cosmo" (or "//go:build cosmo && <extraCond>" when extraCond is set), and
// any leftover double //go:build line from a file whose header spans more
// than one such line is removed.
func addCosmoFile(moduleOut string, c copySpec) error {
	data, err := os.ReadFile(filepath.Join(moduleOut, c.src))
	if err != nil {
		return fmt.Errorf("cosmocompat: %s: %w (module layout may have changed upstream -- update src/cosmocompat)", c.src, err)
	}
	lines := strings.Split(string(data), "\n")

	tag := "//go:build cosmo"
	if c.extraCond != "" {
		tag = "//go:build cosmo && " + c.extraCond
	}

	out := make([]string, 0, len(lines)+2)
	replaced := false
	for _, l := range lines {
		if strings.HasPrefix(l, "//go:build ") {
			if replaced {
				// A second //go:build line in the same file (rare, seen in
				// a couple of generated headers): drop it, the first
				// replacement already set the constraint.
				continue
			}
			out = append(out, tag, "")
			replaced = true
			continue
		}
		out = append(out, l)
	}
	if !replaced && strings.HasSuffix(c.dst, ".s") {
		// Assembly has no package clause to anchor on; insert right after
		// the header comment line instead (matches abi0_linux_amd64.s, the
		// only .s file this table adds).
		final := make([]string, 0, len(out)+3)
		final = append(final, out[0], "", tag)
		final = append(final, out[1:]...)
		out = final
		replaced = true
	}
	if !replaced {
		// No existing tag (a filename-only constrained file): add one
		// right before the package clause.
		final := make([]string, 0, len(out)+2)
		for _, l := range out {
			if strings.HasPrefix(l, "package ") && !replaced {
				final = append(final, tag, "")
				replaced = true
			}
			final = append(final, l)
		}
		out = final
	}
	if !replaced {
		return fmt.Errorf("cosmocompat: %s: found neither a //go:build line to replace, nor a package clause to insert one before", c.src)
	}

	dstPath := filepath.Join(moduleOut, c.dst)
	if err := os.MkdirAll(filepath.Dir(dstPath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dstPath, []byte(strings.Join(out, "\n")), 0o644)
}

// matchesContext reports whether file name in dirPath would be included in
// the build under goos/goarch, honoring BOTH an explicit //go:build line and
// Go's implicit _goos_goarch.go filename convention -- the same evaluation
// "go build" itself does, via the standard library rather than a hand-rolled
// re-implementation of either rule.
func matchesContext(dirPath, name, goos, goarch string) (bool, error) {
	ctx := build.Default
	ctx.GOOS = goos
	ctx.GOARCH = goarch
	ctx.CgoEnabled = false
	ok, err := ctx.MatchFile(dirPath, name)
	if err != nil {
		return false, fmt.Errorf("cosmocompat: evaluating build constraints for %s: %w", filepath.Join(dirPath, name), err)
	}
	return ok, nil
}

// dirMatchCopies finds every file directly under m.dir (module-relative)
// that (a) is included in a real m.goos/m.goarch build and (b) is NOT
// already included in a build under GOOS=cosmo/GOARCH=m.goarch -- a "default
// file" carrying a negation tag such as "!(linux && arm64)" already compiles
// for cosmo on its own (GOOS=cosmo makes "linux" false), so copying it too
// would redeclare its symbols rather than fill a real gap. Every survivor
// gets one copySpec: a cosmo-tagged sibling named after the original file
// plus "_cosmo" and m.archTag, so two dirMatch entries against the same dir
// never write the same destination.
//
// A candidate can also be covered INDIRECTLY, by a sibling file rather than
// its own tag: the generator's other half of a "default vs override" pair
// (modernc.org/sqlite's hooks.go / hooks_linux_arm64.go is exactly this --
// hooks.go's "!(linux && arm64)" tag, hooks_linux_arm64.go's implicit
// filename tag) is gated purely by filename, which by construction never
// matches GOOS=cosmo. So its own alreadyCosmo check is always false, even
// when the negated sibling already declares the same symbols under cosmo.
// coveredBySibling checks that: strip the candidate's trailing
// "_<goos>_<goarch>" filename suffix and see whether the resulting base
// file exists and already matches cosmo on its own.
func dirMatchCopies(moduleOut string, m dirMatch) ([]copySpec, error) {
	dirPath := filepath.Join(moduleOut, m.dir)
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return nil, fmt.Errorf("cosmocompat: reading %s: %w (module layout may have changed upstream -- update src/cosmocompat)", dirPath, err)
	}
	names := make(map[string]bool, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			names[e.Name()] = true
		}
	}
	var out []copySpec
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		realMatch, err := matchesContext(dirPath, name, m.goos, m.goarch)
		if err != nil {
			return nil, err
		}
		if !realMatch {
			continue
		}
		alreadyCosmo, err := matchesContext(dirPath, name, "cosmo", m.goarch)
		if err != nil {
			return nil, err
		}
		if alreadyCosmo {
			continue
		}
		covered, err := coveredBySibling(dirPath, name, m.goos, m.goarch, names)
		if err != nil {
			return nil, err
		}
		if covered {
			continue
		}
		relSrc := filepath.Join(m.dir, name)
		base := strings.TrimSuffix(name, ".go")
		dst := filepath.Join(m.dir, base+"_cosmo_"+m.archTag+".go")
		out = append(out, copySpec{src: relSrc, dst: dst, extraCond: m.archTag})
	}
	return out, nil
}

// coveredBySibling reports whether name's declarations are already
// available under GOOS=cosmo/GOARCH=goarch through a DIFFERENT file in the
// same directory: the generator's negated "default" half of a
// filename-gated override pair (modernc.org/sqlite's hooks.go /
// hooks_linux_arm64.go is exactly this -- hooks.go's "!(linux && arm64)" tag
// already covers cosmo on its own; hooks_linux_arm64.go's tag is
// filename-only, so it can never match GOOS=cosmo and its own alreadyCosmo
// check is always false, even though hooks.go already declares the same
// symbols). name's real match against (goos, goarch) is already established
// by the caller, so this strips exactly that suffix -- "_<goos>_<goarch>",
// "_<goos>", or "_<goarch>", the three shapes go/build's implicit filename
// convention recognizes -- and checks whether the resulting base file
// exists and is itself already cosmo-inclusive.
func coveredBySibling(dirPath, name, goos, goarch string, names map[string]bool) (bool, error) {
	stem := strings.TrimSuffix(name, ".go")
	for _, suffix := range []string{"_" + goos + "_" + goarch, "_" + goos, "_" + goarch} {
		if !strings.HasSuffix(stem, suffix) {
			continue
		}
		base := strings.TrimSuffix(stem, suffix) + ".go"
		if base == ".go" || !names[base] {
			continue
		}
		covered, err := matchesContext(dirPath, base, "cosmo", goarch)
		if err != nil {
			return false, err
		}
		if covered {
			return true, nil
		}
	}
	return false, nil
}

// appendCosmoExclusion wraps a file's existing //go:build expression in
// parens and ANDs in "!cosmo". The parens are required, not cosmetic: "&&"
// binds tighter than "||" in a build-tag expression, so appending "&&
// !cosmo" onto an OR'd expression without them (e.g. "!linux || !go1.24")
// would silently parse as "!linux || (!go1.24 && !cosmo)" instead of the
// intended "(!linux || !go1.24) && !cosmo" -- exercised by
// vgetrandom_unsupported.go, whose original tag is exactly this shape.
func appendCosmoExclusion(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("cosmocompat: %s: %w (module layout may have changed upstream -- update src/cosmocompat)", path, err)
	}
	lines := strings.Split(string(data), "\n")
	found := false
	for i, l := range lines {
		if strings.HasPrefix(l, "//go:build ") {
			expr := strings.TrimPrefix(l, "//go:build ")
			lines[i] = "//go:build (" + expr + ") && !cosmo"
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("cosmocompat: %s has no //go:build line to exclude cosmo from", path)
	}
	return os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644)
}

// applyOverlays writes each of g's hand-written/hand-patched files from the
// embedded template into moduleOut, overwriting anything a copy or tagEdit
// produced at the same path (this always runs last within one gap).
func applyOverlays(moduleOut string, g gap) error {
	for dst, templatePath := range g.overlays {
		data, err := overlayFS.ReadFile(templatePath)
		if err != nil {
			return fmt.Errorf("cosmocompat: reading embedded overlay %s: %w", templatePath, err)
		}
		dstPath := filepath.Join(moduleOut, dst)
		if err := os.MkdirAll(filepath.Dir(dstPath), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(dstPath, data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// applyGap downloads the pristine module into scratchDir/<slot> and patches
// it in place. sourceModule/sourceVersion are what to fetch, which is
// g.module at the consumer's pinned version unless the consumer replaces it
// with a mirror -- see neededGaps. dir is the consumer's module root, used so
// the download resolves through the consumer's own proxy/auth configuration.
func applyGap(dir, scratchDir, slot string, g gap, sourceModule, sourceVersion string) error {
	src, err := downloadModule(dir, sourceModule, sourceVersion)
	if err != nil {
		return err
	}
	moduleOut := filepath.Join(scratchDir, slot)
	if err := freshCopy(src, moduleOut); err != nil {
		return err
	}
	// The go.work replaces g.module with this directory, and a directory
	// replacement must declare the path it replaces. A mirror declares its own
	// path (gitlab.com/cznic/libc), so rewrite the module line to the one the
	// build asks for. A no-op when the source IS g.module.
	if err := setModulePath(moduleOut, g.module); err != nil {
		return err
	}
	for _, e := range g.tagEdits {
		if err := appendCosmoExclusion(filepath.Join(moduleOut, e.path)); err != nil {
			return err
		}
	}
	copies := append([]copySpec(nil), g.copies...)
	for _, m := range g.dirMatches {
		matched, err := dirMatchCopies(moduleOut, m)
		if err != nil {
			return err
		}
		copies = append(copies, matched...)
	}
	for _, c := range copies {
		if err := addCosmoFile(moduleOut, c); err != nil {
			return err
		}
	}
	if err := applyOverlays(moduleOut, g); err != nil {
		return err
	}
	if g.postPatch != nil {
		if err := g.postPatch(moduleOut); err != nil {
			return err
		}
	}
	return nil
}

// setModulePath rewrites a staged module's "module" line to path. The staged
// copy is scratch, so this never touches anything the consumer owns.
func setModulePath(moduleDir, path string) error {
	name := filepath.Join(moduleDir, "go.mod")
	data, err := os.ReadFile(name)
	if err != nil {
		return fmt.Errorf("cosmocompat: reading %s: %w", name, err)
	}
	f, err := modfile.Parse("go.mod", data, nil)
	if err != nil {
		return fmt.Errorf("cosmocompat: parsing %s: %w", name, err)
	}
	if f.Module != nil && f.Module.Mod.Path == path {
		return nil
	}
	if err := f.AddModuleStmt(path); err != nil {
		return fmt.Errorf("cosmocompat: setting the module path of %s to %s: %w", name, path, err)
	}
	out, err := f.Format()
	if err != nil {
		return fmt.Errorf("cosmocompat: formatting %s: %w", name, err)
	}
	return os.WriteFile(name, out, 0o644)
}
