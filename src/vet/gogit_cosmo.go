//go:build cosmo

package vet

import "errors"

// go-git cannot compile for cosmo (no x/sys/unix port), so this stub makes
// checkFileCommittedByName fall through to the git-CLI check. The error must
// never contain "uncommitted changes", the go-git verdict marker.
func checkFileCommittedGoGit(filename string) error {
	return errors.New("go-git is not available in cosmo builds")
}
