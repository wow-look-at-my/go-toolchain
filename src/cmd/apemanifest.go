package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/wow-look-at-my/go-toolchain/src/build"
)

// buildhostManifestName is the sidecar buildhost-publish reads to list multi-platform artifacts; see docs/BUILDHOST-MANIFEST.md.
const buildhostManifestName = "buildhost-artifacts.json"

// buildhostManifestSchema is the only version buildhost accepts; other values fail the publish.
const buildhostManifestSchema = 1

type buildhostManifest struct {
	Schema    int                      `json:"schema"`
	Artifacts []buildhostManifestEntry `json:"artifacts"`
}

// buildhostManifestEntry describes a multi-platform artifact.
// Kind is deliberately absent: it selects buildhost's repackaging vocabulary (binary/library/
// assets/...) and defaults to binary, while APE-ness is a property buildhost detects from the
// bytes. There is no display-label field either -- the badge renders from the stored set, so a
// label could only ever disagree with it.
type buildhostManifestEntry struct {
	// File is the artifact's path relative to the published directory.
	File string `json:"file"`
	// Platforms are the os/arch pairs this file runs on; the leading pair is the row's canonical slot.
	Platforms []string `json:"platforms"`
	// Filename is the name the download is served under: the plain binary name, not the on-disk <name>.
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

// apeMagic is what an APE starts with.
const apeMagic = "MZqFpD"

// isAPE reports whether path begins with the APE magic. That is the safe
// answer: it stays out of the manifest, and the build publishes nothing
// instead of a rejected upload.
func isAPE(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	head := make([]byte, len(apeMagic))
	if _, err := io.ReadFull(f, head); err != nil {
		return false
	}
	return string(head) == apeMagic
}

// apeManifestEntries describes each target's fat APE for the manifest.
// platforms is the coverage the APE was built for; outDir is checked so an
// entry never names a file that is not there (buildhost fails the publish on
// a missing file, and a manifest is only worth writing when it is true).
//
// A file that is not an APE is left out. A module with no main package still
// writes a build/<name>, and naming it here claims it runs on every platform
// in the set. buildhost then rejects it: "upload declares N platforms but is
// not an APE". Every library module in the org failed its
// publish that way. The skipped files are returned so the caller can say
// which, because a build that publishes nothing must say why.
func apeManifestEntries(targets []build.Target, outDir string, platforms []buildPlatform) ([]buildhostManifestEntry, []string, error) {
	if len(platforms) == 0 {
		return nil, nil, fmt.Errorf("refusing to write %s with an empty platform set: the set is what tells a consumer where the binary runs", buildhostManifestName)
	}
	list := make([]string, 0, len(platforms))
	for _, p := range platforms {
		list = append(list, p.OS+"/"+p.Arch)
	}
	entries := make([]buildhostManifestEntry, 0, len(targets))
	var skipped []string
	for _, t := range targets {
		file := build.BinaryName(t.OutputName, cosmoOS, cosmoFatArch)
		path := filepath.Join(outDir, file)
		if _, err := os.Stat(path); err != nil {
			return nil, nil, fmt.Errorf("%s would name a missing artifact %s: %w", buildhostManifestName, file, err)
		}
		if !isAPE(path) {
			skipped = append(skipped, file)
			continue
		}
		entries = append(entries, buildhostManifestEntry{
			File:      file,
			Platforms: list,
			Filename:  t.OutputName,
		})
	}
	return entries, skipped, nil
}
