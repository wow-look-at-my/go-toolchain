//go:build cosmo

package vet

import "errors"

// go-git can't compile for cosmo; this stub falls through to the git-CLI
// check. Error text must never contain "uncommitted changes".
func checkFileCommittedGoGit(filename string) error {
	return errors.New("go-git is not available in cosmo builds")
}
