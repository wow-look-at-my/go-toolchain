package cmd

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/wow-look-at-my/go-toolchain/src/hostos"
	"github.com/wow-look-at-my/go-toolchain/src/logger"
	"github.com/wow-look-at-my/go-toolchain/src/runner"
)

// activeGoCmd starts the go command this pipeline builds with: this
// executable, under its go subcommand.
var activeGoCmd []string

// activeGoroot is the GOROOT every go command of this run reads: the fork
// checkout in this module, and this executable, which carries the standard
// library, anywhere else.
var activeGoroot string

// goLinkDir holds the go link that puts this executable on PATH, for a child
// process that starts go by name.
var goLinkDir string

// selfExecutableFunc is os.Executable, as a seam.
var selfExecutableFunc = os.Executable

// EnsureGoVersion makes this executable the go command of the run: it goes
// earliest on PATH under the name go, GOROOT names the standard library it
// builds against, and GOTOOLCHAIN is local so the go command fetches nothing.
// Inside this module the fork checkout is the GOROOT and is put at its
// branch's head earliest.
func EnsureGoVersion() error {
	exe, err := selfExecutableFunc()
	if err != nil {
		return fmt.Errorf("finding this executable: %w", err)
	}
	goroot := exe
	if ownModule() {
		commit, err := syncForkSource(runner.New())
		if err != nil {
			return err
		}
		resolvedForkCommit = commit
		if goroot, err = forkGorootDir(); err != nil {
			return err
		}
	}
	linkDir, err := linkGoToSelf(exe)
	if err != nil {
		return err
	}
	useSelfAsPipelineToolchain(exe, linkDir, goroot)

	goPath, err := exec.LookPath("go")
	if err != nil {
		return fmt.Errorf("the go link at %s is not on PATH after setup: %w", linkDir, err)
	}
	if err := verifyGoToolchainFunc(goPath); err != nil {
		return fmt.Errorf("the linked go command failed its integrity probe: %w", err)
	}

	installed, err := installedGoVersion()
	if err != nil {
		return fmt.Errorf("the linked go command will not report its version: %w", err)
	}
	if err := forkSatisfiesGoMod(installed); err != nil {
		return err
	}
	activeGoVersion = "go" + installed
	recordGoMinor(goVersionCore(installed))
	logger.Info("go-bootstrap: this binary is the go command, %s, with GOROOT %s", installed, goroot)
	return nil
}

// linkGoToSelf answers the directory holding go, a link to exe. The directory
// is named after exe, so every run of a single executable puts the same PATH
// in front of the programs it starts: a test that resolves a program records
// PATH as an input, and a name that changed per run kept every such test from
// replaying. A link left by an earlier run of the same file is reused; a
// single pointing elsewhere is replaced in a single rename. NT runs no
// symlink as a program, so there it is a hard link, or a copy when the volumes differ.
func linkGoToSelf(exe string) (string, error) {
	base := scratchBase(hostos.GOOS())
	if base == "" {
		base = os.TempDir()
	}
	sum := sha256.Sum256([]byte(exe))
	dir := filepath.Join(base, "go-toolchain-go-"+hex.EncodeToString(sum[:8]))
	if err := os.MkdirAll(dir, 0o777); err != nil {
		return "", err
	}
	link := filepath.Join(dir, "go"+hostExeSuffix())
	if linksTo(link, exe) {
		return dir, nil
	}
	fresh := filepath.Join(dir, fmt.Sprintf(".go-%d%s", os.Getpid(), hostExeSuffix()))
	if err := placeLink(exe, fresh); err != nil {
		return "", err
	}
	if err := os.Rename(fresh, link); err != nil {
		os.Remove(fresh)
		return "", fmt.Errorf("installing the go link at %s: %w", link, err)
	}
	return dir, nil
}

// linksTo reports whether name resolves to the file at exe.
func linksTo(name, exe string) bool {
	got, err := os.Stat(name)
	if err != nil {
		return false
	}
	want, err := os.Stat(exe)
	if err != nil {
		return false
	}
	return os.SameFile(got, want)
}

// placeLink creates name as a way to run exe.
func placeLink(exe, name string) error {
	if hostos.GOOS() != "windows" {
		if err := os.Symlink(exe, name); err != nil {
			return fmt.Errorf("linking %s to this executable: %w", name, err)
		}
		return nil
	}
	if err := os.Link(exe, name); err == nil {
		return nil
	}
	if err := copyFile(exe, name); err != nil {
		return fmt.Errorf("copying this executable to %s: %w", name, err)
	}
	return nil
}

// useSelfAsPipelineToolchain points this process and its children at the go
// link and the GOROOT.
func useSelfAsPipelineToolchain(exe, linkDir, goroot string) {
	activeGoCmd = []string{exe, "go"}
	activeGoroot = goroot
	goLinkDir = linkDir
	os.Setenv("PATH", pathWithFirst(linkDir, os.Getenv("PATH"), hostos.GOOS()))
	os.Setenv("GOROOT", goroot)
	os.Setenv("GOTOOLCHAIN", "local")
	os.Setenv(linkedGoEnv, "1")
}

// pathWithFirst puts dir ahead of rest, with the list separator of hostGOOS
// rather than of the machine doing the join.
func pathWithFirst(dir, rest, hostGOOS string) string {
	listSep := ":"
	if hostGOOS == "windows" {
		listSep = ";"
	}
	if rest == "" {
		return dir
	}
	return dir + listSep + rest
}

// removeGoLink deletes the go link directory at the end of the run.
func removeGoLink() {
	if goLinkDir == "" {
		return
	}
	if err := os.RemoveAll(goLinkDir); err != nil {
		logger.Debug("go link: leaving %s behind: %v", goLinkDir, err)
	}
	goLinkDir = ""
}
