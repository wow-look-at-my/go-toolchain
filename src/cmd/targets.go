package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/wow-look-at-my/go-toolchain/src/build"
	"github.com/wow-look-at-my/go-toolchain/src/logger"
)

// cosmoOS/cosmoFatArch: the pseudo-target for the fat APE, not a normal GOOS/GOARCH pair.
const (
	cosmoOS      = "cosmo"
	cosmoFatArch = "fat"
)

// wasmArch: GOARCH=wasm, paired with GOOS=js or wasip1; built with the gosmopolitan fork toolchain.
const wasmArch = "wasm"

// isWasmGOOS reports whether goos only exists as a GOARCH=wasm pairing.
func isWasmGOOS(goos string) bool { return goos == "js" || goos == "wasip1" }

// wasmPublishEnv, set to the off value, opts out of buildhost's publishable wasm naming (for a buildhost too old to accept it).
const wasmPublishEnv = "GO_TOOLCHAIN_WASM_PUBLISH"

// wasmPublishOptOut reports whether wasmPublishEnv disabled buildhost publishing of wasm artifacts.
func wasmPublishOptOut() bool { return os.Getenv(wasmPublishEnv) == "0" }

// wasmArtifactName returns buildhost's publishable name by default, or the
// excluded .wasm-suffixed name under the wasmPublishEnv opt-out.
func wasmArtifactName(name string, p buildPlatform) string {
	if wasmPublishOptOut() {
		return build.UnpublishableWasmName(name, p.OS)
	}
	return build.BinaryName(name, p.OS, p.Arch)
}

// validGOOS / validGOARCH mirror `go tool dist list`, and validate --targets / --cosmo-platforms entries.
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

// buildPlatform is a resolved build target: a GOOS/GOARCH pair, or the cosmo fat-APE pseudo-target.
type buildPlatform struct {
	OS   string
	Arch string
}

// IsCosmo reports whether the platform is the cosmo fat-APE pseudo-target.
func (p buildPlatform) IsCosmo() bool { return p.OS == cosmoOS }

// IsWasm reports whether the platform is a WebAssembly target (js/wasm or wasip1/wasm).
func (p buildPlatform) IsWasm() bool { return p.Arch == wasmArch }

// NeedsForkToolchain reports whether the platform needs the gosmopolitan fork toolchain: cosmo or wasm.
func (p buildPlatform) NeedsForkToolchain() bool { return p.IsCosmo() || p.IsWasm() }

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
		return buildPlatform{}, fmt.Errorf("invalid %s entry %q: this flag names the native platforms the fat APE covers, so %q itself is not one of them", flagName, entry, cosmoOS)
	}
	// Canonical spelling is wasm/js, wasm/wasip1 (buildhost's os=wasm scheme).
	// js/wasm and wasip1/wasm are accepted as aliases and normalize to the
	// same target, so mixing spellings still dedupes.
	if goos == wasmArch {
		if !isWasmGOOS(goarch) {
			return buildPlatform{}, fmt.Errorf("invalid %s entry %q: wasm targets are %s/js or %s/wasip1", flagName, entry, wasmArch, wasmArch)
		}
		goos, goarch = goarch, wasmArch
	}
	if !slices.Contains(validGOOS, goos) {
		return buildPlatform{}, fmt.Errorf("unknown OS %q in %s entry %q (valid: %s)", goos, flagName, entry, strings.Join(validGOOS, ", "))
	}
	if !slices.Contains(validGOARCH, goarch) {
		return buildPlatform{}, fmt.Errorf("unknown architecture %q in %s entry %q (valid: %s)", goarch, flagName, entry, strings.Join(validGOARCH, ", "))
	}
	// GOARCH=wasm only pairs with GOOS=js or GOOS=wasip1 (and vice versa);
	// fail fast on impossible combinations instead of at build time.
	if isWasmGOOS(goos) && goarch != wasmArch {
		return buildPlatform{}, fmt.Errorf("invalid %s entry %q: GOOS %s only builds WebAssembly; use %s/%s", flagName, entry, goos, wasmArch, goos)
	}
	if !isWasmGOOS(goos) && goarch == wasmArch {
		return buildPlatform{}, fmt.Errorf("invalid %s entry %q: GOARCH %s needs GOOS js or wasip1 (%s/js or %s/wasip1)", flagName, entry, wasmArch, wasmArch, wasmArch)
	}
	return buildPlatform{OS: goos, Arch: goarch}, nil
}

// parseTargetList parses the --targets flag: the special value "cosmo" (the
// fat APE) and the wasm pairs. A native os/arch pair is rejected: the APE is
// the only native output, and --cosmo-platforms picks the hosts it covers.
func parseTargetList(entries []string) ([]buildPlatform, error) {
	if len(entries) == 0 {
		return nil, fmt.Errorf("--targets requires at least one entry")
	}
	seen := make(map[buildPlatform]string, len(entries))
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
			if !p.IsWasm() {
				return nil, fmt.Errorf("invalid target %q: --targets only accepts wasm targets (wasm/js, wasm/wasip1) and the special value %q; the fat APE is the only native output, and --cosmo-platforms chooses which native hosts it covers", entry, cosmoOS)
			}
		}
		if first, dup := seen[p]; dup {
			// A repeated entry is an error; rival spellings of the same
			// target (wasm/js and its js/wasm alias) dedupe silently instead.
			if first == entry {
				return nil, fmt.Errorf("duplicate target %q", entry)
			}
			continue
		}
		seen[p] = entry
		out = append(out, p)
	}
	return out, nil
}

// resolveMatrixPlatforms turns --targets into the platforms to build. With
// no flag the answer is the cosmo fat APE covering --cosmo-platforms.
func resolveMatrixPlatforms() ([]buildPlatform, error) {
	if len(matrixTargets) > 0 {
		return parseTargetList(matrixTargets)
	}
	return []buildPlatform{{OS: cosmoOS, Arch: cosmoFatArch}}, nil
}

// resolvePlatformTargets returns the main packages to build for each
// platform, plus whether ANY platform has a main package at all.
//
// The cosmo pseudo-target keeps the host set, since the fat APE embeds
// several native platforms and no single context describes it. Wasm
// platforms get per-target discovery under their own GOOS/GOARCH build
// context, so a "//go:build js && wasm" main builds only for js/wasm
// targets. A platform with no main packages is skipped with a warning
// rather than failing the build.
//
// Discovery skips gomod.MemLimitGuardFileName, so the memlimit guard
// injected into host-context main dirs cannot make a host-only dir look
// like a main package under another target's context.
func resolvePlatformTargets(platforms []buildPlatform, hostTargets []build.Target) (map[buildPlatform][]build.Target, bool, error) {
	perPlatform := make(map[buildPlatform][]build.Target, len(platforms))
	anyMains := false
	cache := make(map[string][]build.Target)
	for _, p := range platforms {
		if p.IsCosmo() {
			perPlatform[p] = hostTargets
			if len(hostTargets) > 0 {
				anyMains = true
			}
			continue
		}
		key := p.OS + "/" + p.Arch
		ts, ok := cache[key]
		if !ok {
			var err error
			if ts, err = build.ResolveBuildTargetsForTarget(p.OS, p.Arch); err != nil {
				return nil, false, err
			}
			cache[key] = ts
		}
		if len(ts) == 0 {
			logger.Warn("⇒ Warning: no main packages found under GOOS=%s GOARCH=%s; skipping target %s", p.OS, p.Arch, key)
		} else {
			anyMains = true
		}
		perPlatform[p] = ts
	}
	return perPlatform, anyMains, nil
}

// copyWasmExecJS copies the fork toolchain's lib/wasm/wasm_exec.js (the JS
// harness that loads and runs a GOOS=js wasm binary in a browser or Node)
// into the output directory. The harness MUST byte-match the toolchain that
// built the wasm artifact, which is why the build ships it rather than
// leaving consumers to find a compatible copy.
func copyWasmExecJS(forkGoroot, outDir string) (string, error) {
	src := filepath.Join(forkGoroot, "lib", "wasm", "wasm_exec.js")
	dst := filepath.Join(outDir, "wasm_exec.js")
	// Replace any stale copy (possibly a symlink) with a fresh real file.
	if err := os.Remove(dst); err != nil && !os.IsNotExist(err) {
		return "", err
	}
	if err := copyFile(src, dst); err != nil {
		return "", err
	}
	return dst, nil
}
