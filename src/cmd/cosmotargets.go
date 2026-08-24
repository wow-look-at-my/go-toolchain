package cmd

import (
	"fmt"
	"slices"
	"strings"

	"github.com/wow-look-at-my/go-containers/set"
)

// DefaultCosmoPlatforms is the host platform set the fat APE covers by default; platforms left out carry no code.
var DefaultCosmoPlatforms = []string{"linux/amd64", "darwin/arm64", "windows/amd64"}

// cosmoPlatformsAll asks for every platform the fork can emit, leaving GOCOSMOPLATFORMS unset.
const cosmoPlatformsAll = "all"

// cosmoPlatformsEnv is gosmopolitan's variable naming a fat APE's covered host platforms; the fork rejects unemittable tokens.
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
	seen := set.New[buildPlatform](len(entries))
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
		if seen.Contains(p) {
			return nil, fmt.Errorf("duplicate --cosmo-platforms entry %q", entry)
		}
		seen.Add(p)
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

// apeCoverage returns the platforms a fat APE built with the given --cosmo-platforms selection
// actually runs on. An empty selection means "all": coverage is then every platform whose
// runtime is verified, never an unverified one the fork can also emit -- a published set names
// where the binary RUNS.
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
