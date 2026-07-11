//go:build !cosmo

package cmd

import (
	"database/sql"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// The persistent dependency-check cache, stored in
// ~/.cache/go-toolchain/deps.db. This file carries the modernc.org/sqlite
// driver import, which (via modernc.org/libc's per-GOOS generated code) has
// no cosmo target — so the whole backend is excluded from GOOS=cosmo builds
// and depscache_cosmo.go supplies a no-op cache instead.
const (
	cacheSubdir = "go-toolchain"
	cacheFile   = "deps.db"
)

// sqliteDepsCache implements depsCache on a local sqlite database.
type sqliteDepsCache struct {
	db *sql.DB
}

func openDepsCache() (depsCache, error) {
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		cacheDir = filepath.Join(os.Getenv("HOME"), ".cache")
	}

	dir := filepath.Join(cacheDir, cacheSubdir)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}

	dbPath := filepath.Join(dir, cacheFile)
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, err
	}

	// Create table if not exists
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS deps (
			path TEXT NOT NULL,
			version TEXT NOT NULL,
			update_version TEXT,
			checked_at INTEGER NOT NULL,
			PRIMARY KEY (path, version)
		)
	`)
	if err != nil {
		db.Close()
		return nil, err
	}

	return &sqliteDepsCache{db: db}, nil
}

func (c *sqliteDepsCache) lookup(path, version string) (update string, checkedAt int64, found bool) {
	var cachedUpdate sql.NullString
	err := c.db.QueryRow(
		`SELECT update_version, checked_at FROM deps WHERE path = ? AND version = ?`,
		path, version,
	).Scan(&cachedUpdate, &checkedAt)
	if err != nil {
		return "", 0, false
	}
	// cachedUpdate.String is "" when NULL (cached as up-to-date).
	return cachedUpdate.String, checkedAt, true
}

func (c *sqliteDepsCache) store(path, version, update string, checkedAt int64) {
	var updateVal sql.NullString
	if update != "" {
		updateVal = sql.NullString{String: update, Valid: true}
	}
	_, _ = c.db.Exec(
		`INSERT OR REPLACE INTO deps (path, version, update_version, checked_at) VALUES (?, ?, ?, ?)`,
		path, version, updateVal, checkedAt,
	)
}

func (c *sqliteDepsCache) close() {
	c.db.Close()
}
