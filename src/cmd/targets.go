package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/wow-look-at-my/go-toolchain/src/build"
)

// The cosmo pseudo-target: one GOOS=cosmo fat APE built with the gosmopolitan
// toolchain. A fat APE covers linux/amd64, linux/arm64, darwin/arm64 and
// windows/amd64 in a single binary, so it has no per-arch matrix entries; its
// artifact is named <name>_cosmo_fat.
const (
	cosmoOS      = "cosmo"
	cosmoFatArch = "fat"
)

// DefaultCosmoSlots are the per-platform artifact names that receive a copy
// of the cosmo fat APE (see copyCosmoSlots). darwin/amd64 is deliberately
// absent (the cosmo darwin-Intel runtime is not verified yet), as is
// windows/arm64 (the APE's embedded PE payload is amd64-only).
var DefaultCosmoSlots = []string{"linux/amd64", "linux/arm64", "darwin/arm64", "windows/amd64"}

// validGOOS / validGOARCH mirror the target lists of the Go distribution
// (`go tool dist list`), plus cosmo which is handled specially. Used only to
// validate --targets / --cosmo-slots entries; the legacy --os/--arch flags
// stay unvalidated for backward compatibility.
var (
	validGOOS = []string{
		"aix", "android", "darwin", "dragonfly", "freebsd", "illumos", "ios",
		"js", "linux", "netbsd", "openbsd", "plan9", "solaris", "wasip1",
		"windows",
	}
	validGOARCH = []string{
		"386", "amd64", "arm", "arm64", "loong64", "mips", "mips64",
		"mips64le", "mipsle", "ppc64", "ppc64le", "riscv64", "s390x", "wasm",
	}
)

// buildPlatform is one resolved build target: a (GOOS, GOARCH) pair, or the
// cosmo fat-APE pseudo-target represented as {cosmoOS, cosmoFatArch}.
type buildPlatform struct {
	OS   string
	Arch string
}

// IsCosmo reports whether the platform is the cosmo fat-APE pseudo-target.
func (p buildPlatform) IsCosmo() bool { return p.OS == cosmoOS }

// parsePlatformPair validates a single "os/arch" entry of the given flag.
// The cosmo pseudo-target is NOT accepted here; callers that allow it
// (parseTargetList) handle the bare "cosmo" spelling before calling this.
func parsePlatformPair(entry, flagName string) (buildPlatform, error) {
	goos, goarch, ok := strings.Cut(entry, "/")
	if !ok {
		return buildPlatform{}, fmt.Errorf("invalid %s entry %q: entries are os/arch pairs like linux/amd64", flagName, entry)
	}
	if goos == cosmoOS {
		if flagName == "--targets" {
			return buildPlatform{}, fmt.Errorf("invalid target %q: a cosmo build is always one fat APE (multi-OS, multi-arch), so it takes no architecture; use the plain %q entry", entry, cosmoOS)
		}
		return buildPlatform{}, fmt.Errorf("invalid %s entry %q: slots name the native platforms the fat APE is copied to, so %q itself is not a slot", flagName, entry, cosmoOS)
	}
	if !slices.Contains(validGOOS, goos) {
		return buildPlatform{}, fmt.Errorf("unknown OS %q in %s entry %q (valid: %s)", goos, flagName, entry, strings.Join(validGOOS, ", "))
	}
	if !slices.Contains(validGOARCH, goarch) {
		return buildPlatform{}, fmt.Errorf("unknown architecture %q in %s entry %q (valid: %s)", goarch, flagName, entry, strings.Join(validGOARCH, ", "))
	}
	return buildPlatform{OS: goos, Arch: goarch}, nil
}

// parseTargetList parses the --targets flag: a list of os/arch pairs plus the
// special value "cosmo" (one gosmopolitan fat APE). Duplicates are rejected.
func parseTargetList(entries []string) ([]buildPlatform, error) {
	if len(entries) == 0 {
		return nil, fmt.Errorf("--targets requires at least one entry")
	}
	seen := make(map[buildPlatform]bool, len(entries))
	out := make([]buildPlatform, 0, len(entries))
	for _, raw := range entries {
		entry := strings.TrimSpace(raw)
		var p buildPlatform
		switch {
		case entry == "":
			return nil, fmt.Errorf("--targets contains an empty entry")
		case entry == cosmoOS:
			p = buildPlatform{OS: cosmoOS, Arch: cosmoFatArch}
		default:
			var err error
			if p, err = parsePlatformPair(entry, "--targets"); err != nil {
				return nil, err
			}
		}
		if seen[p] {
			return nil, fmt.Errorf("duplicate target %q", entry)
		}
		seen[p] = true
		out = append(out, p)
	}
	return out, nil
}

// parseCosmoSlots parses the --cosmo-slots flag: the os/arch artifact names
// that receive a copy of the cosmo fat APE. The single value "none" disables
// slot mapping (returns an empty list).
func parseCosmoSlots(entries []string) ([]buildPlatform, error) {
	if len(entries) == 1 && strings.TrimSpace(entries[0]) == "none" {
		return nil, nil
	}
	seen := make(map[buildPlatform]bool, len(entries))
	out := make([]buildPlatform, 0, len(entries))
	for _, raw := range entries {
		entry := strings.TrimSpace(raw)
		if entry == "" || entry == "none" {
			return nil, fmt.Errorf("invalid --cosmo-slots entry %q: \"none\" must be the only value when disabling slot mapping", raw)
		}
		p, err := parsePlatformPair(entry, "--cosmo-slots")
		if err != nil {
			return nil, err
		}
		if seen[p] {
			return nil, fmt.Errorf("duplicate --cosmo-slots entry %q", entry)
		}
		seen[p] = true
		out = append(out, p)
	}
	return out, nil
}

// resolveMatrixPlatforms turns the matrix flags into the list of platforms to
// build: the validated --targets list when set, otherwise the historic
// --os x --arch cartesian product (unvalidated, exactly today's behavior).
func resolveMatrixPlatforms() ([]buildPlatform, error) {
	if len(matrixTargets) > 0 {
		// --targets replaces the cartesian product entirely; call out
		// non-default --os/--arch values that are being ignored.
		if !slices.Equal(matrixOS, DefaultOS) || !slices.Equal(matrixArch, DefaultArch) {
			fmt.Fprintf(os.Stderr, "⇒ Warning: --targets is set; ignoring --os/--arch\n")
		}
		return parseTargetList(matrixTargets)
	}
	if len(matrixOS) == 0 || len(matrixArch) == 0 {
		return nil, fmt.Errorf("no platforms specified (need at least one --os and one --arch, or --targets)")
	}
	var out []buildPlatform
	for _, goos := range matrixOS {
		if goos == cosmoOS {
			return nil, fmt.Errorf("GOOS %q cannot be built through --os/--arch: a cosmo build is one fat APE, not a per-arch matrix entry; use --targets %s instead", cosmoOS, cosmoOS)
		}
		for _, goarch := range matrixArch {
			out = append(out, buildPlatform{OS: goos, Arch: goarch})
		}
	}
	return out, nil
}

// copyCosmoSlots copies each build target's cosmo fat APE onto the
// conventional per-platform artifact names (the "slots" buildhost serves),
// e.g. name_cosmo_fat -> name_linux_amd64, name_windows_amd64.exe. The APE is
// a genuine PE, so the windows slot's .exe name is correct. Copies are real
// files, never symlinks: the publish pipeline skips symlinks. A slot whose
// filename was already produced by an explicit native build in this run is
// skipped with a warning — an explicit target beats a mapped copy. Returns
// the paths of the created copies (for checksums).
func copyCosmoSlots(targets []build.Target, outDir string, slots []buildPlatform, nativeBuilt map[string]bool) ([]string, error) {
	var created []string
	for _, target := range targets {
		srcName := build.BinaryName(target.OutputName, cosmoOS, cosmoFatArch)
		srcPath := filepath.Join(outDir, srcName)
		if _, err := os.Stat(srcPath); err != nil {
			return nil, fmt.Errorf("cosmo slot mapping: fat APE %s not found: %w", srcPath, err)
		}
		for _, slot := range slots {
			dstName := build.BinaryName(target.OutputName, slot.OS, slot.Arch)
			if nativeBuilt[dstName] {
				fmt.Printf("  SKIP %s (explicit native %s/%s build wins over the cosmo slot copy)\n", dstName, slot.OS, slot.Arch)
				continue
			}
			dstPath := filepath.Join(outDir, dstName)
			// Remove any stale artifact first so a leftover symlink is
			// replaced by a real file instead of being written through.
			if err := os.Remove(dstPath); err != nil && !os.IsNotExist(err) {
				return nil, fmt.Errorf("cosmo slot mapping: %w", err)
			}
			if err := copyFile(srcPath, dstPath); err != nil {
				return nil, fmt.Errorf("cosmo slot mapping: copying %s to %s: %w", srcPath, dstPath, err)
			}
			fmt.Printf("  COPY %s <- %s\n", dstName, srcName)
			created = append(created, dstPath)
		}
	}
	return created, nil
}
