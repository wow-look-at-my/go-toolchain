package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/wow-look-at-my/go-toolchain/src/build"
)

// buildhostManifestName is the sidecar buildhost-publish reads from the root
// of the directory it publishes. It names the artifacts whose identity is a
// platform SET rather than one os/arch pair — the fat APE — so each publishes
// as ONE artifact row with one download link. A file listed here is removed
// from the publish action's <binary>_<os>_<arch> filename scan, which is why
// the APE keeps its <name> name without tripping buildhost's
// os=cosmo rejection.
//
// See docs/BUILDHOST-MANIFEST.md for the wire contract.
const buildhostManifestName = "buildhost-artifacts.json"

// buildhostManifestSchema is the only schema version buildhost accepts. It
// fails the publish on any other value rather than ignoring the file.
const buildhostManifestSchema = 1

type buildhostManifest struct {
	Schema    int                      `json:"schema"`
	Artifacts []buildhostManifestEntry `json:"artifacts"`
}

// buildhostManifestEntry describes one multi-platform artifact.
//
// Kind is deliberately absent: it selects buildhost's repackaging vocabulary
// (binary/library/assets/...) and defaults to binary, while APE-ness is a
// property buildhost detects from the bytes. Sending a format there would be
// wrong. There is no display-label field either — the badge renders from the
// stored set, so a label could only ever disagree with it.
type buildhostManifestEntry struct {
	// File is the artifact's path relative to the published directory.
	File string `json:"file"`
	// Platforms are the os/arch pairs this one file runs on. The first is the
	// row's canonical slot.
	Platforms []string `json:"platforms"`
	// Filename is the name the download is served under: the plain binary
	// name, not the on-disk <name>.
	Filename string `json:"filename"`
}

// writeBuildhostManifest writes the manifest into outDir and returns its path.
func writeBuildhostManifest(outDir string, entries []buildhostManifestEntry) (string, error) {
	m := buildhostManifest{Schema: buildhostManifestSchema, Artifacts: entries}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return "", err
	}
	path := filepath.Join(outDir, buildhostManifestName)
	if err := os.WriteFile(path, append(data, '\n'), 0644); err != nil {
		return "", fmt.Errorf("writing %s: %w", buildhostManifestName, err)
	}
	return path, nil
}

// apeManifestEntries describes each target's fat APE for the manifest.
// platforms is the coverage the APE was built for; outDir is checked so an
// entry never names a file that is not there (buildhost fails the publish on a
// missing file, and a manifest is only worth writing when it is true).
func apeManifestEntries(targets []build.Target, outDir string, platforms []buildPlatform) ([]buildhostManifestEntry, error) {
	if len(platforms) == 0 {
		return nil, fmt.Errorf("refusing to write %s with an empty platform set: the set is what tells a consumer where the binary runs", buildhostManifestName)
	}
	list := make([]string, 0, len(platforms))
	for _, p := range platforms {
		list = append(list, p.OS+"/"+p.Arch)
	}
	entries := make([]buildhostManifestEntry, 0, len(targets))
	for _, t := range targets {
		file := build.BinaryName(t.OutputName, cosmoOS, cosmoFatArch)
		if _, err := os.Stat(filepath.Join(outDir, file)); err != nil {
			return nil, fmt.Errorf("%s would name a missing artifact %s: %w", buildhostManifestName, file, err)
		}
		entries = append(entries, buildhostManifestEntry{
			File:      file,
			Platforms: list,
			Filename:  t.OutputName,
		})
	}
	return entries, nil
}
