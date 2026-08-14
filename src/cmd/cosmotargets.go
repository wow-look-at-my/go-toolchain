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
