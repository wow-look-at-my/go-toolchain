//go:build cosmo

package vet

import "errors"

// go-git cannot compile for GOOS=cosmo (its go-billy/osfs matches cosmo's
// `unix` build tag but needs golang.org/x/sys/unix, which has no cosmo port),
// so cosmo builds exclude gogit.go and this stub makes
// checkFileCommittedByName always fall through to the git-CLI check: the
// returned error deliberately does not contain "uncommitted changes", which
// is the marker the caller trusts as a definitive go-git verdict.
func checkFileCommittedGoGit(filename string) error {
	return errors.New("go-git is not available in cosmo builds")
}
