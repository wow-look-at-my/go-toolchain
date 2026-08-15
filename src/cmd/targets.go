package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/wow-look-at-my/go-containers/set"
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

// DefaultCosmoSlots are the per-platform artifact names that receive a copy
// of the cosmo fat APE (see copyCosmoSlots). darwin/arm64 is deliberately
// absent even though the fat APE boots and builds fine on ARM64 macs: the
// pipeline WEDGES at exit there (CI runs 28739021382/28739520377; SIGQUIT
// dumps in run 28742069477), root-caused to the gosmopolitan runtime running
// unix-socket fds in blocking mode with no netpoller on darwin hosts, so the
// cache daemon's net.Listener.Close deadlocks against its own blocked Accept
// — tracked in https://github.com/wow-look-at-my/go-toolchain/issues/276.
// Macs keep getting a native binary by default until that runtime bug is
// fixed. Also absent: darwin/amd64 (the cosmo darwin-Intel runtime is not
// verified yet) and windows/arm64 (the APE's embedded PE payload is
// amd64-only).
var DefaultCosmoSlots = []string{"linux/amd64", "linux/arm64", "windows/amd64"}

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

// parseCosmoSlots parses the --cosmo-slots flag: the os/arch artifact names
// that receive a copy of the cosmo fat APE. The single value "none" disables
// slot mapping (returns an empty list).
func parseCosmoSlots(entries []string) ([]buildPlatform, error) {
	if len(entries) == 1 && strings.TrimSpace(entries[0]) == "none" {
		return nil, nil
	}
	seen := set.New[buildPlatform](len(entries))
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
		if p.IsWasm() {
			return nil, fmt.Errorf("invalid --cosmo-slots entry %q: slots name native platforms the fat APE is copied to, and an APE is not a wasm binary", entry)
		}
		if seen.Contains(p) {
			return nil, fmt.Errorf("duplicate --cosmo-slots entry %q", entry)
		}
		seen.Add(p)
		out = append(out, p)
	}
	return out, nil
}

// resolveMatrixPlatforms turns the matrix flags into the list of platforms to
// build: the validated --targets list when set, otherwise the historic
// --os x --arch cartesian product.
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
		// non-default --os/--arch values that are being ignored.
		if !slices.Equal(matrixOS, DefaultOS) || !slices.Equal(matrixArch, DefaultArch) {
			logger.Warn("⇒ Warning: --targets is set; ignoring --os/--arch")
		}
		return parseTargetList(matrixTargets)
	}
	if len(matrixOS) == 0 || len(matrixArch) == 0 {
		return nil, fmt.Errorf("no platforms specified (need at least one --os and one --arch, or --targets)")
	}
	hasWasmOS := slices.Contains(matrixOS, wasmArch)
	for _, goarch := range matrixArch {
		if goarch == wasmArch {
			return nil, fmt.Errorf("GOARCH %q is spelled as the OS in the wasm pairing (buildhost's os=wasm model); use --os wasm --arch js or --os wasm --arch wasip1 (or --targets %s/js, --targets %s/wasip1)", wasmArch, wasmArch, wasmArch)
		}
		if isWasmGOOS(goarch) && !hasWasmOS {
			return nil, fmt.Errorf("arch %q is a wasm flavor and needs os %q in the --os list (--os wasm --arch %s), or use --targets %s/%s", goarch, wasmArch, goarch, wasmArch, goarch)
		}
	}
	var out []buildPlatform
	var skipped []string
	for _, goos := range matrixOS {
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

// copyCosmoSlots copies each build target's cosmo fat APE onto the
// conventional per-platform artifact names (the "slots" buildhost serves),
// e.g. name_cosmo_fat -> name_linux_amd64, name_windows_amd64.exe. The APE is
// a genuine PE, so the windows slot's .exe name is correct. Copies are real
// files, never symlinks: the publish pipeline skips symlinks. A slot whose
// filename was already produced by an explicit native build in this run is
// skipped with a warning — an explicit target beats a mapped copy.
//
// Once a target has at least one slot copy, its <name>_cosmo_fat artifact is
// REPLACED: buildhost validates os on artifact upload and rejects os=cosmo
// (400 "invalid os", observed on go-regex-compiler run 28738513866), and one
// rejected artifact aborts that whole publish — so the fat name must never
// reach the publish pipeline as a regular file. With dropFat=false (local
// builds) it becomes a relative symlink to the target's first slot copy, so
// the canonical name keeps working on disk while the publish action skips it.
// With dropFat=true (CI) it is removed outright: upload-artifact DEREFERENCES
// symlinks (see the host-symlink note in matrix.go), which would re-materialize
// a publish-breaking regular file inside the downloaded artifact. A target
// with no surviving slot copy (every slot lost to a native collision) keeps
// its real fat file — it is the only APE artifact then, and such a layout
// cannot be published to buildhost until the server accepts os=cosmo.
//
// Returns the created copy paths (for checksums) and the fat artifact paths
// that were replaced — the caller must exclude those from checksums, which
// cover real files only (every slot copy is byte-identical to the APE, so no
// coverage is lost).
func copyCosmoSlots(targets []build.Target, outDir string, slots []buildPlatform, nativeBuilt set.Set[string], dropFat bool) (created, replacedFat []string, err error) {
	for _, target := range targets {
		srcName := build.BinaryName(target.OutputName, cosmoOS, cosmoFatArch)
		srcPath := filepath.Join(outDir, srcName)
		if _, err := os.Stat(srcPath); err != nil {
			return nil, nil, fmt.Errorf("cosmo slot mapping: fat APE %s not found: %w", srcPath, err)
		}
		var targetCopies []string
		for _, slot := range slots {
			dstName := build.BinaryName(target.OutputName, slot.OS, slot.Arch)
			if nativeBuilt.Contains(dstName) {
				logger.Warn("  SKIP %s (explicit native %s/%s build wins over the cosmo slot copy)", dstName, slot.OS, slot.Arch)
				continue
			}
			dstPath := filepath.Join(outDir, dstName)
			// Remove any stale artifact first so a leftover symlink is
			// replaced by a real file instead of being written through.
			if err := os.Remove(dstPath); err != nil && !os.IsNotExist(err) {
				return nil, nil, fmt.Errorf("cosmo slot mapping: %w", err)
			}
			if err := copyFile(srcPath, dstPath); err != nil {
				return nil, nil, fmt.Errorf("cosmo slot mapping: copying %s to %s: %w", srcPath, dstPath, err)
			}
			logger.Info("  COPY %s <- %s", dstName, srcName)
			targetCopies = append(targetCopies, dstPath)
		}
		created = append(created, targetCopies...)
		if len(targetCopies) == 0 {
			if len(slots) > 0 {
				logger.Warn("  KEEP %s (no slot copy survived; note buildhost rejects os=cosmo uploads)", srcName)
			}
			continue
		}
		if err := os.Remove(srcPath); err != nil {
			return nil, nil, fmt.Errorf("cosmo slot mapping: replacing %s: %w", srcName, err)
		}
		if dropFat {
			logger.Info("  DROP %s (buildhost rejects os=cosmo uploads; the slot copies carry the APE)", srcName)
		} else {
			linkTarget := filepath.Base(targetCopies[0])
			if err := os.Symlink(linkTarget, srcPath); err != nil {
				return nil, nil, fmt.Errorf("cosmo slot mapping: linking %s -> %s: %w", srcName, linkTarget, err)
			}
			logger.Info("  LINK %s -> %s (kept as a symlink: publish skips symlinks; buildhost rejects os=cosmo)", srcName, linkTarget)
		}
		replacedFat = append(replacedFat, srcPath)
	}
	return created, replacedFat, nil
}
