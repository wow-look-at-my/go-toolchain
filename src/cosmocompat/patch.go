package cosmocompat

import (
	"fmt"
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
	for _, c := range g.copies {
		if err := addCosmoFile(moduleOut, c); err != nil {
			return err
		}
	}
	for _, cg := range g.copyGlobs {
		if err := applyCopyGlob(moduleOut, cg); err != nil {
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
