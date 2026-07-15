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
	if !slices.Contains(validGOOS, goos) {
		return buildPlatform{}, fmt.Errorf("unknown OS %q in %s entry %q (valid: %s)", goos, flagName, entry, strings.Join(validGOOS, ", "))
	}
	if !slices.Contains(validGOARCH, goarch) {
		return buildPlatform{}, fmt.Errorf("unknown architecture %q in %s entry %q (valid: %s)", goarch, flagName, entry, strings.Join(validGOARCH, ", "))
	}
	// GOARCH=wasm only pairs with GOOS=js or GOOS=wasip1 (and vice versa);
	// fail fast on impossible combinations instead of at build time.
	if isWasmGOOS(goos) && goarch != wasmArch {
		return buildPlatform{}, fmt.Errorf("invalid %s entry %q: GOOS %s only builds WebAssembly; use %s/%s", flagName, entry, goos, goos, wasmArch)
	}
	if !isWasmGOOS(goos) && goarch == wasmArch {
		return buildPlatform{}, fmt.Errorf("invalid %s entry %q: GOARCH %s needs GOOS js or wasip1 (js/%s or wasip1/%s)", flagName, entry, wasmArch, wasmArch, wasmArch)
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

// resolveMatrixPlatforms turns the matrix flags into the list of platforms to
// build: the validated --targets list when set, otherwise the historic
// --os x --arch cartesian product (unvalidated, exactly today's behavior).
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
	var out []buildPlatform
	for _, goarch := range matrixArch {
		if goarch == wasmArch {
			return nil, fmt.Errorf("GOARCH %q cannot be built through --os/--arch: wasm targets are exact GOOS pairings built with the gosmopolitan toolchain, not cartesian-product entries; use --targets js/%s or --targets wasip1/%s instead", wasmArch, wasmArch, wasmArch)
		}
	}
	for _, goos := range matrixOS {
		if goos == cosmoOS {
			return nil, fmt.Errorf("GOOS %q cannot be built through --os/--arch: a cosmo build is one fat APE, not a per-arch matrix entry; use --targets %s instead", cosmoOS, cosmoOS)
		}
		if isWasmGOOS(goos) {
			return nil, fmt.Errorf("GOOS %q cannot be built through --os/--arch: wasm targets are exact GOOS pairings built with the gosmopolitan toolchain, not cartesian-product entries; use --targets %s/%s instead", goos, goos, wasmArch)
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
