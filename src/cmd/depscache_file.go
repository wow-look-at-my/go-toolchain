package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"github.com/wow-look-at-my/go-toolchain/src/logger"
)

// Persistent dependency-check cache path.
const (
	cacheSubdir = "go-toolchain"
	cacheFile   = "deps.json"
)

// depsCacheEntry is what a check recorded for a dependency. An empty Update
// means the version was current when it was checked.
type depsCacheEntry struct {
	Update    string `json:"u,omitempty"`
	CheckedAt int64  `json:"t"`
}

// fileDepsCache implements depsCache over a JSON file.
type fileDepsCache struct {
	path string

	mu      sync.Mutex
	entries map[string]depsCacheEntry
	dirty   bool
}

// openDepsCache loads the stored check results. Keep this backend free of
// outside packages: the APE carries a payload per platform, and a package
// init that fails on any of them kills that platform's binary before main
// runs. The store maps a dependency and its version to a result, which needs
// no engine. Depth: docs/CMD.md
func openDepsCache() (depsCache, error) {
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		cacheDir = filepath.Join(os.Getenv("HOME"), ".cache")
	}

	dir := filepath.Join(cacheDir, cacheSubdir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}

	path := filepath.Join(dir, cacheFile)
	return &fileDepsCache{path: path, entries: readDepsCacheFile(path)}, nil
}

func depsCacheKey(path, version string) string { return path + "@" + version }

// readDepsCacheFile returns the stored entries. An absent or damaged file
// reads as empty, because the result is a cache and rebuilding an entry costs
// a version check.
func readDepsCacheFile(path string) map[string]depsCacheEntry {
	raw, err := os.ReadFile(path)
	if err != nil {
		return map[string]depsCacheEntry{}
	}
	entries := map[string]depsCacheEntry{}
	if err := json.Unmarshal(raw, &entries); err != nil {
		logger.Debug("deps cache: %s did not parse (%v); starting empty", path, err)
		return map[string]depsCacheEntry{}
	}
	return entries
}

func (c *fileDepsCache) lookup(path, version string) (update string, checkedAt int64, found bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	e, ok := c.entries[depsCacheKey(path, version)]
	if !ok {
		return "", 0, false
	}
	return e.Update, e.CheckedAt, true
}

func (c *fileDepsCache) store(path, version, update string, checkedAt int64) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.entries[depsCacheKey(path, version)] = depsCacheEntry{Update: update, CheckedAt: checkedAt}
	c.dirty = true
}

// close writes what this run recorded, merged onto what is on disk, so a
// go-toolchain running beside this keeps the entries it stored.
func (c *fileDepsCache) close() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.dirty {
		return
	}
	merged := readDepsCacheFile(c.path)
	for k, v := range c.entries {
		merged[k] = v
	}
	if err := writeDepsCacheFile(c.path, merged); err != nil {
		logger.Debug("deps cache: write %s: %v", c.path, err)
		return
	}
	c.dirty = false
}

// writeDepsCacheFile replaces the file atomically, so a reader never sees a
// half-written document.
func writeDepsCacheFile(path string, entries map[string]depsCacheEntry) error {
	raw, err := json.Marshal(entries)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())

	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
