package cosmocompat

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/wow-look-at-my/go-containers/set"
	"golang.org/x/mod/modfile"
)

// resolvedGap is a knownGaps entry the consumer's go.mod actually needs
// patched, at the version the consumer itself pins.
type resolvedGap struct {
	gap
	version string
}

// moduleSlot derives a short, filesystem-safe scratch-directory name from a
// module path -- just its last path element ("modernc.org/libc" -> "libc"),
// which is unique across knownGaps today and reads clearly in logs/paths.
func moduleSlot(modulePath string) string {
	i := strings.LastIndexByte(modulePath, '/')
	if i < 0 {
		return modulePath
	}
	return modulePath[i+1:]
}

// neededGaps reads dir's go.mod and returns every knownGaps entry the
// consumer's build graph actually depends on, at the consumer's own pinned
// version. A module the consumer already replaces itself is left alone --
// never override a consumer's own intentional replace.
func neededGaps(dir string) ([]resolvedGap, error) {
	data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return nil, err
	}
	f, err := modfile.Parse("go.mod", data, nil)
	if err != nil {
		return nil, fmt.Errorf("cosmocompat: parsing go.mod: %w", err)
	}

	alreadyReplaced := set.New[string](len(f.Replace))
	for _, r := range f.Replace {
		alreadyReplaced.Add(r.Old.Path)
	}
	versions := make(map[string]string, len(f.Require))
	for _, r := range f.Require {
		versions[r.Mod.Path] = r.Mod.Version
	}

	var out []resolvedGap
	needsLibc := false
	for _, g := range knownGaps {
		if alreadyReplaced.Contains(g.module) {
			continue
		}
		v, ok := versions[g.module]
		if !ok {
			continue
		}
		if g.module == libcGap.module {
			needsLibc = true
		}
		out = append(out, resolvedGap{gap: g, version: v})
	}
	// golang.org/x/sys's own cosmo gap is only reached through
	// modernc.org/libc's cosmo files, which pull in x/sys/unix symbols the
	// rest of x/sys's callers never touch (go-toolchain's own cosmo
	// self-build depends on x/sys directly and built fine for a long time
	// with none of these patches applied). Patching x/sys for a consumer
	// that isn't also getting libc patched changes files nothing actually
	// needs changed, at whatever version the consumer happens to pin --
	// version drift risk with no corresponding benefit. Skip it there.
	if !needsLibc {
		filtered := out[:0]
		for _, rg := range out {
			if rg.gap.module == xSysGap.module {
				continue
			}
			filtered = append(filtered, rg)
		}
		out = filtered
	}
	return out, nil
}

// NeedsPatch reports whether dir's go.mod depends on any module cosmocompat
// knows how to patch. Cheap: only parses go.mod, no network, no scratch
// directory -- callers use this to skip the whole mechanism entirely for a
// consumer that doesn't need it.
func NeedsPatch(dir string) (bool, error) {
	gaps, err := neededGaps(dir)
	if err != nil {
		return false, err
	}
	return len(gaps) > 0, nil
}

// Prepare downloads, patches, and stages every cosmo-compat gap module dir
// actually depends on, and returns a go.work file whose replace directives
// redirect just those modules to the patched copies. dir's own go.mod and
// go.sum are never modified. Callers set GOWORK to the returned path only
// for the cosmo build invocation(s); every other phase (tests, vet, non-cosmo
// targets) is unaffected.
//
// Returns ("", nil, nil) when dir needs no patching at all -- the common
// case for a repo that doesn't depend on any known-gap module.
func Prepare(dir string) (goWorkPath string, cleanup func(), err error) {
	gaps, err := neededGaps(dir)
	if err != nil {
		return "", nil, err
	}
	if len(gaps) == 0 {
		return "", func() {}, nil
	}

	scratchDir, err := os.MkdirTemp("", "go-toolchain-cosmocompat-")
	if err != nil {
		return "", nil, err
	}
	cleanup = func() { os.RemoveAll(scratchDir) }

	absDir, err := filepath.Abs(dir)
	if err != nil {
		cleanup()
		return "", nil, err
	}

	var sb strings.Builder
	sb.WriteString(goDirectiveFor(absDir))
	sb.WriteString("\nuse " + absDir + "\n\n")
	for _, rg := range gaps {
		slot := moduleSlot(rg.gap.module)
		if err := applyGap(absDir, scratchDir, slot, rg.gap, rg.version); err != nil {
			cleanup()
			return "", nil, err
		}
		sb.WriteString(fmt.Sprintf("replace %s => %s\n", rg.gap.module, filepath.Join(scratchDir, slot)))
	}

	workPath := filepath.Join(scratchDir, "go.work")
	if err := os.WriteFile(workPath, []byte(sb.String()), 0o644); err != nil {
		cleanup()
		return "", nil, err
	}
	return workPath, cleanup, nil
}

// goDirectiveFor reads the consumer's own go.mod "go" directive so the
// generated go.work declares a compatible language version -- go.work
// requires one, same as go.mod. Falls back to a conservative floor (Go
// workspace support itself starts at 1.18) if the consumer's go.mod is
// somehow unreadable at this point, which Prepare's own earlier read
// already ruled out in practice.
func goDirectiveFor(dir string) string {
	data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return "go 1.18"
	}
	f, err := modfile.Parse("go.mod", data, nil)
	if err != nil || f.Go == nil || f.Go.Version == "" {
		return "go 1.18"
	}
	return "go " + f.Go.Version
}
