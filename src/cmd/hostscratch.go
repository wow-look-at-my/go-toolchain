package cmd

import "os"

// scratchBase answers a base a native tool can open, "" for os.MkdirTemp's
// default. Why NT needs this: docs/CI.md.
func scratchBase(hostGOOS string) string {
	if hostGOOS != "windows" {
		return ""
	}
	dir, err := goCacheDirFunc()
	if err != nil {
		return ""
	}
	return dir
}

// argListTempDir answers that base concretely, for a caller that joins onto it.
func argListTempDir(hostGOOS string) string {
	if base := scratchBase(hostGOOS); base != "" {
		return base
	}
	return os.TempDir()
}
