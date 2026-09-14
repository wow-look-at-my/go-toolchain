package cmd

import (
	"os"
	"path/filepath"
	"sync"

	"github.com/wow-look-at-my/go-toolchain/src/logger"
	"github.com/wow-look-at-my/go-toolchain/src/summary"
)

var (
	artifactSizesMu sync.Mutex
	artifactSizes   []summary.ArtifactSize
)

// recordArtifactSize measures a built artifact for the run summary, and logs
// it: an APE carries its whole standard library, so its size is worth a line.
func recordArtifactSize(path string) {
	info, err := os.Stat(path)
	if err != nil {
		return
	}
	logger.Info("  %s is %s", filepath.Base(path), sizeText(info.Size()))
	artifactSizesMu.Lock()
	defer artifactSizesMu.Unlock()
	artifactSizes = append(artifactSizes, summary.ArtifactSize{Name: filepath.Base(path), Bytes: info.Size()})
}

// builtArtifactSizes answers every artifact recorded so far and forgets them.
func builtArtifactSizes() []summary.ArtifactSize {
	artifactSizesMu.Lock()
	defer artifactSizesMu.Unlock()
	sizes := artifactSizes
	artifactSizes = nil
	return sizes
}
