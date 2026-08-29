package build

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// TmpPrefix spells an output no successful build has committed yet. Depth: docs/BUILD-OUTPUTS.md
const TmpPrefix = ".tmp-"

// TempOutputPath is final's directory plus ".tmp-" before its base name.
func TempOutputPath(final string) string {
	dir, base := filepath.Split(final)
	return filepath.Join(dir, TmpPrefix+base)
}

// CommitOutput moves everything a successful build wrote under the temp
// spelling of final onto its final name. cmd/go COPIES onto its -o across
// filesystems, so the target is visible mid-write; a rename inside one
// directory is not.
//
// The bare temp name and every "<base>.…" shape are this build's (the cosmo
// sidecar ELFs derive from the -o path). A "<base>_…" shape belongs to
// another target's own build and is deliberately NOT swept — moving it robs
// a sibling build mid-write.
func CommitOutput(final string) error {
	tmp := TempOutputPath(final)
	if _, err := os.Stat(tmp); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("go build reported success but wrote no output at %s", tmp)
		}
		return fmt.Errorf("committing build output %s: %w", tmp, err)
	}
	dir, base := filepath.Split(final)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("committing build output %s: %w", tmp, err)
	}
	moved := 0
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || (n != TmpPrefix+base && !strings.HasPrefix(n, TmpPrefix+base+".")) {
			continue
		}
		src := filepath.Join(dir, n)
		dst := filepath.Join(dir, strings.TrimPrefix(n, TmpPrefix))
		// Not a result yet: keep it out of sight until the move (Windows only).
		hideFile(src)
		if err := os.Rename(src, dst); err != nil {
			return fmt.Errorf("moving build output into place (%s): %w", dst, err)
		}
		revealFile(dst)
		moved++
	}
	if moved == 0 {
		// It existed at the stat above, so something else took it.
		return fmt.Errorf("build output %s disappeared before it could be moved into place", tmp)
	}
	return nil
}

// DiscardOutput removes a failed build's temp outputs, on CommitOutput's
// same stick-to-its-own-shapes rule. The target file is untouched: the
// compiler never wrote it. Best-effort — the build already failed, and the
// sweep in cmd/staleoutputs also removes leftover ".tmp-" shapes.
func DiscardOutput(final string) {
	dir, base := filepath.Split(final)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || (n != TmpPrefix+base && !strings.HasPrefix(n, TmpPrefix+base+".")) {
			continue
		}
		os.Remove(filepath.Join(dir, n))
	}
}
