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

// The cosmo pseudo-target: one GOOS=cosmo fat APE built with the gosmopolitan
// toolchain. A fat APE covers linux/amd64, linux/arm64, darwin/arm64 and
// windows/amd64 in a single binary, so it has no per-arch matrix entries; its
// artifact is named <name>_cosmo_fat.
const (
	cosmoOS      = "cosmo"
	cosmoFatArch = "fat"
)

// The WebAssembly targets: GOARCH=wasm paired with GOOS=js (browser/Node.js)
// or GOOS=wasip1 (WASI runtimes). Like cosmo, wasm targets are built with the
// gosmopolitan fork toolchain, which carries this org's wasm runtime fixes
// (preemptible loops, Node.js fetch networking, DWARF debug info, ...).
// Artifacts get a .wasm suffix (see build.BinaryName).
const wasmArch = "wasm"

// isWasmGOOS reports whether goos only exists as a GOARCH=wasm pairing.
func isWasmGOOS(goos string) bool { return goos == "js" || goos == "wasip1" }

// wasmPublishEnv is the opt-out knob for buildhost publishing of wasm
// artifacts. By default wasm artifacts use buildhost's publishable naming
// (<name>_wasm_js / <name>_wasm_wasip1 — os=wasm with arch=js/wasip1, see
// wow-look-at-my/buildhost#166). Uploading those requires a buildhost with
// wasm artifact support; on an older server the upload 400s (`invalid os
// "wasm"`) and one rejected artifact aborts the whole publish. Setting
// GO_TOOLCHAIN_WASM_PUBLISH=0 falls back to the excluded naming
// (<name>_<goos>_wasm.wasm), which never reaches the publish upload set but
// still ships in build/, checksums.txt, and the CI artifact.
const wasmPublishEnv = "GO_TOOLCHAIN_WASM_PUBLISH"

// wasmPublishOptOut reports whether GO_TOOLCHAIN_WASM_PUBLISH=0 disabled
// buildhost publishing of wasm artifacts.
func wasmPublishOptOut() bool { return os.Getenv(wasmPublishEnv) == "0" }

// wasmArtifactName returns the wasm platform's artifact name: buildhost's
// publishable convention by default, the excluded .wasm-suffixed shape under
// GO_TOOLCHAIN_WASM_PUBLISH=0.
func wasmArtifactName(name string, p buildPlatform) string {
	if wasmPublishOptOut() {
		return build.UnpublishableWasmName(name, p.OS)
	}
	return build.BinaryName(name, p.OS, p.Arch)
}

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

// IsWasm reports whether the platform is a WebAssembly target (js/wasm or
// wasip1/wasm).
func (p buildPlatform) IsWasm() bool { return p.Arch == wasmArch }

// NeedsForkToolchain reports whether the platform is built with the
// gosmopolitan fork toolchain instead of the go on PATH: the cosmo fat APE
// (the fork is the only compiler for GOOS=cosmo) and the wasm targets (the
// fork carries the org's wasm runtime fixes).
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
		return buildPlatform{}, fmt.Errorf("invalid %s entry %q: slots name the native platforms the fat APE is copied to, so %q itself is not a slot", flagName, entry, cosmoOS)
	}
	// Canonical wasm spelling: wasm/js and wasm/wasip1, matching buildhost's
	// artifact scheme (os=wasm with arch=js/wasip1) and the published
	// <name>_wasm_js naming. Normalized here to the internal GOOS/GOARCH form
	// ({js, wasm} / {wasip1, wasm}); the GOOS-order spellings js/wasm and
	// wasip1/wasm stay accepted below as quiet compatibility aliases (Go's
	// own GOOS/GOARCH order, already shipped in released consumers) and
	// normalize to the SAME target, so mixing spellings dedupes.
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

// parseTargetList parses the --targets flag: a list of os/arch pairs plus the
// special value "cosmo" (one gosmopolitan fat APE). Duplicates are rejected.
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
		}
		if first, dup := seen[p]; dup {
			// The same entry twice is a mistake worth flagging; two different
			// SPELLINGS of the same target (the canonical wasm/js and its
			// js/wasm compatibility alias) are deliberate synonyms and dedupe
			// silently to one target.
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

// resolveMatrixPlatforms turns the matrix flags into the list of platforms to
// build. With no target flags at all the answer is ONE cosmo fat APE: a single
// binary covering --cosmo-platforms, rather than a per-platform binary each.
// Naming --os or --arch selects the cartesian product of native binaries
// instead, and --targets replaces both with an exact list.
//
// The cartesian product accepts the wasm pairing in buildhost's model:
// --os wasm combines ONLY with --arch js / --arch wasip1 (the wasm flavors),
// producing the same normalized targets as --targets wasm/js — identical
// artifacts, naming, and per-target main discovery. In a MIXED list the
// impossible cross combinations (wasm with a native arch, a native os with a
// wasm flavor arch) are skipped with one aggregate warning; if the whole
// product is impossible (e.g. --os wasm --arch amd64 alone) it fails fast
// with the exact-pairing error, and a wasm flavor arch with no "wasm" in
// --os at all is an error pointing at the fix.
func resolveMatrixPlatforms() ([]buildPlatform, error) {
	if len(matrixTargets) > 0 {
		// --targets replaces the cartesian product entirely; call out
		// --os/--arch values that are being ignored.
		if len(matrixOS) > 0 || len(matrixArch) > 0 {
			logger.Warn("⇒ Warning: --targets is set; ignoring --os/--arch")
		}
		return parseTargetList(matrixTargets)
	}
	// No target flags: one APE, not a product. The cartesian product is opt-in
	// because it costs one binary per platform for a binary that already runs
	// on all of them.
	if len(matrixOS) == 0 && len(matrixArch) == 0 {
		return []buildPlatform{{OS: cosmoOS, Arch: cosmoFatArch}}, nil
	}
	// Half a product is the other half's default: --arch arm64 alone still
	// means "these arches, every OS I would have built anyway".
	oses, arches := matrixOS, matrixArch
	if len(oses) == 0 {
		oses = DefaultOS
	}
	if len(arches) == 0 {
		arches = DefaultArch
	}
	hasWasmOS := slices.Contains(oses, wasmArch)
	for _, goarch := range arches {
		if goarch == wasmArch {
			return nil, fmt.Errorf("GOARCH %q is spelled as the OS in the wasm pairing (buildhost's os=wasm model); use --os wasm --arch js or --os wasm --arch wasip1 (or --targets %s/js, --targets %s/wasip1)", wasmArch, wasmArch, wasmArch)
		}
		if isWasmGOOS(goarch) && !hasWasmOS {
			return nil, fmt.Errorf("arch %q is a wasm flavor and needs os %q in the --os list (--os wasm --arch %s), or use --targets %s/%s", goarch, wasmArch, goarch, wasmArch, goarch)
		}
	}
	var out []buildPlatform
	var skipped []string
	for _, goos := range oses {
		if goos == cosmoOS {
			return nil, fmt.Errorf("GOOS %q cannot be built through --os/--arch: a cosmo build is one fat APE, not a per-arch matrix entry; use --targets %s instead", cosmoOS, cosmoOS)
		}
		if isWasmGOOS(goos) {
			return nil, fmt.Errorf("GOOS %q is the wasm FLAVOR in buildhost's model, not the os; use --os wasm --arch %s (or --targets %s/%s)", goos, goos, wasmArch, goos)
		}
		for _, goarch := range matrixArch {
			switch {
			case goos == wasmArch && isWasmGOOS(goarch):
				// os=wasm, arch=js|wasip1: normalize to the internal
				// GOOS/GOARCH form, same as --targets wasm/<flavor>.
				out = append(out, buildPlatform{OS: goarch, Arch: wasmArch})
			case goos == wasmArch || isWasmGOOS(goarch):
				// Impossible cross combination in a mixed list (wasm with a
				// native arch, or a native os with a wasm flavor arch).
				skipped = append(skipped, goos+"/"+goarch)
			default:
				out = append(out, buildPlatform{OS: goos, Arch: goarch})
			}
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no buildable os/arch combinations: os %q pairs only with arch js or wasip1 (--os wasm --arch js, or --targets %s/js)", wasmArch, wasmArch)
	}
	if len(skipped) > 0 {
		logger.Warn("⇒ Warning: skipping impossible os/arch combinations: %s (os %q pairs only with arch js/wasip1; js/wasip1 arches pair only with os %q)", strings.Join(skipped, ", "), wasmArch, wasmArch)
	}
	return out, nil
}

// resolvePlatformTargets returns the main packages to build for each
// platform, plus whether ANY platform has at least one.
//
// The legacy --os x --arch product keeps today's behavior for native
// platforms: one host-context set (hostTargets, from
// build.ResolveBuildTargets, including its library-only fallback) shared by
// every non-wasm platform. Wasm platforms — reachable from BOTH paths since
// --os wasm --arch js|wasip1 joined the cartesian product — always get
// per-target discovery, and with an explicit --targets list every entry gets
// discovery under its OWN GOOS/GOARCH build context: a main package guarded
// "//go:build js && wasm" is built
// for js/wasm targets and never attempted for native ones (it has zero files
// there, so building it would fail), while a "//go:build linux" main builds
// for linux entries even from a non-linux host. An unconstrained main is in
// every set. The cosmo pseudo-target keeps the host set — the fat APE embeds
// several native platforms, so no single GOOS/GOARCH context describes it
// (unchanged semantics). A platform whose context has no main packages is
// skipped with a warning rather than failing the whole matrix.
//
// The memlimit guard never distorts these sets even though injection happens
// earlier: discovery skips gomod.MemLimitGuardFileName by name, so the
// unconstrained guard injected into HOST-context main dirs cannot make a
// host-only main dir (e.g. //go:build linux) look like a main package under
// another target's context.
func resolvePlatformTargets(platforms []buildPlatform, hostTargets []build.Target) (map[buildPlatform][]build.Target, bool, error) {
	perPlatform := make(map[buildPlatform][]build.Target, len(platforms))
	anyMains := false
	explicit := len(matrixTargets) > 0
	cache := make(map[string][]build.Target)
	for _, p := range platforms {
		// Host-context set: every legacy-cartesian platform except wasm
		// (unchanged behavior), and always the cosmo pseudo-target. Wasm
		// platforms get per-target discovery on BOTH paths, so
		// `--os wasm --arch js` behaves exactly like `--targets wasm/js`.
		if p.IsCosmo() || (!explicit && !p.IsWasm()) {
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
