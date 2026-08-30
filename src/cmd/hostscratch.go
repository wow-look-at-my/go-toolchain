package cmd

import "os"

// A path inside another program's argument list is a string that program
// parses, and nothing translates it. The APE reports GOOS=cosmo and answers
// cosmo's POSIX view, so on an NT host it hands a native go.exe or git a
// spelling neither can open. A path cosmo itself gives the OS -- cmd.Dir, its
// own file calls -- is translated and needs none of this.

// scratchBase places a scratch directory where a native tool can open it. It
// answers the empty string when the ambient temp directory already serves,
// which is what os.MkdirTemp reads as "use os.TempDir()".
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

// argListTempDir answers the same directory as a concrete path, for a caller
// that joins onto it rather than handing it to os.MkdirTemp.
func argListTempDir(hostGOOS string) string {
	if base := scratchBase(hostGOOS); base != "" {
		return base
	}
	return os.TempDir()
}
