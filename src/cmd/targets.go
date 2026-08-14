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

// DefaultCosmoSlots is empty: one APE is one artifact. A slot copy is a
// byte-identical duplicate of the APE under a per-platform name, so a slot
// list of length N publishes the same binary N times. Slots stay available
// (--cosmo-slots) for a consumer that still resolves a <name>_<os>_<arch>
// download and cannot yet ask for the APE itself.
var DefaultCosmoSlots []string

// DefaultCosmoPlatforms is the host platform set the fat APE covers by
// default. The fork emits a payload per architecture and a boot path per host
// OS, so every platform left out of this set is code the APE does not carry.
var DefaultCosmoPlatforms = []string{"linux/amd64", "darwin/arm64", "windows/amd64"}

// cosmoPlatformsAll is the --cosmo-platforms value that asks for every
// platform the fork can emit: GOCOSMOPLATFORMS is then left unset, which is
// the fork's own everything-default.
const cosmoPlatformsAll = "all"

// cosmoPlatformsEnv is the gosmopolitan variable naming the host platforms a
// fat APE must cover. The fork is the authority on the value; it rejects a
// token it cannot emit, which backstops the check below.
const cosmoPlatformsEnv = "GOCOSMOPLATFORMS"

// cosmoRuntimeStatus maps a platform the fork can name to why it is not
// coverable, or to "" when the APE genuinely runs there. A platform whose
// runtime is unverified is refused rather than quietly claimed: the published
// artifact's platform set is what tells a consumer where the binary runs, and
// a set that names an untested host is a promise the APE does not keep.
var cosmoRuntimeStatus = map[buildPlatform]string{
	{OS: "linux", Arch: "amd64"}:   "",
	{OS: "linux", Arch: "arm64"}:   "",
	{OS: "darwin", Arch: "arm64"}:  "",
	{OS: "windows", Arch: "amd64"}: "",
	{OS: "darwin", Arch: "amd64"}:  "the cosmo darwin-Intel runtime is unverified: the Mach-O image is structurally correct but its runtime bring-up has never executed on real hardware",
	{OS: "windows", Arch: "arm64"}: "the APE's PE payload is amd64-only, and Windows-on-ARM x86-64 emulation fails to boot it",
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
		if p.IsWasm() {
			return nil, fmt.Errorf("invalid --cosmo-slots entry %q: slots name native platforms the fat APE is copied to, and an APE is not a wasm binary", entry)
		}
		if seen[p] {
			return nil, fmt.Errorf("duplicate --cosmo-slots entry %q", entry)
		}
		seen[p] = true
		out = append(out, p)
	}
	return out, nil
}

// parseCosmoPlatforms parses --cosmo-platforms: the host platforms the fat APE
// must cover, as os/arch pairs. The single value "all" asks for every platform
// the fork can emit and returns a nil list, which leaves GOCOSMOPLATFORMS
// unset.
func parseCosmoPlatforms(entries []string) ([]buildPlatform, error) {
	if len(entries) == 1 && strings.TrimSpace(entries[0]) == cosmoPlatformsAll {
		return nil, nil
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("--cosmo-platforms requires at least one os/arch pair, or %q for every platform the fork can emit: an APE with no platforms would run nowhere", cosmoPlatformsAll)
	}
	seen := make(map[buildPlatform]bool, len(entries))
	out := make([]buildPlatform, 0, len(entries))
	for _, raw := range entries {
		entry := strings.TrimSpace(raw)
		if entry == "" || entry == cosmoPlatformsAll {
			return nil, fmt.Errorf("invalid --cosmo-platforms entry %q: %q must be the only value", raw, cosmoPlatformsAll)
		}
		p, err := parsePlatformPair(entry, "--cosmo-platforms")
		if err != nil {
			return nil, err
		}
		if p.IsWasm() {
			return nil, fmt.Errorf("invalid --cosmo-platforms entry %q: an APE covers native hosts, and wasm is not one", entry)
		}
		reason, known := cosmoRuntimeStatus[p]
		if !known {
			return nil, fmt.Errorf("invalid --cosmo-platforms entry %q: the fat APE covers %s", entry, strings.Join(coverableCosmoPlatforms(), ", "))
		}
		if reason != "" {
			return nil, fmt.Errorf("invalid --cosmo-platforms entry %q: %s", entry, reason)
		}
		if seen[p] {
			return nil, fmt.Errorf("duplicate --cosmo-platforms entry %q", entry)
		}
		seen[p] = true
		out = append(out, p)
	}
	return out, nil
}

// coverableCosmoPlatforms lists the os/arch pairs a fat APE actually runs on,
// sorted, for error messages.
func coverableCosmoPlatforms() []string {
	var out []string
	for p, reason := range cosmoRuntimeStatus {
		if reason == "" {
			out = append(out, p.OS+"/"+p.Arch)
		}
	}
	slices.Sort(out)
	return out
}

// apeCoverage returns the platforms a fat APE built with the given
// --cosmo-platforms selection actually runs on. An empty selection is
// "all", where the fork emits every payload it has: the coverage is then
// every platform whose runtime is verified, never the unverified ones the
// fork can also emit — a published set names where the binary RUNS.
func apeCoverage(platforms []buildPlatform) []buildPlatform {
	if len(platforms) > 0 {
		return platforms
	}
	out := make([]buildPlatform, 0, len(cosmoRuntimeStatus))
	for _, entry := range coverableCosmoPlatforms() {
		goos, goarch, _ := strings.Cut(entry, "/")
		out = append(out, buildPlatform{OS: goos, Arch: goarch})
	}
	return out
}

// platformList renders platforms as comma-separated os/arch pairs — the
// GOCOSMOPLATFORMS wire form, and the form the publish manifest records.
func platformList(platforms []buildPlatform) string {
	parts := make([]string, 0, len(platforms))
	for _, p := range platforms {
		parts = append(parts, p.OS+"/"+p.Arch)
	}
	return strings.Join(parts, ",")
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
func copyCosmoSlots(targets []build.Target, outDir string, slots []buildPlatform, nativeBuilt map[string]bool, dropFat bool) (created, replacedFat []string, err error) {
	for _, target := range targets {
		srcName := build.BinaryName(target.OutputName, cosmoOS, cosmoFatArch)
		srcPath := filepath.Join(outDir, srcName)
		if _, err := os.Stat(srcPath); err != nil {
			return nil, nil, fmt.Errorf("cosmo slot mapping: fat APE %s not found: %w", srcPath, err)
		}
		var targetCopies []string
		for _, slot := range slots {
			dstName := build.BinaryName(target.OutputName, slot.OS, slot.Arch)
			if nativeBuilt[dstName] {
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
