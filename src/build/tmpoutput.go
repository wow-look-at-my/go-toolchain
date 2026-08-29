package build

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// TmpPrefix marks a build target's output while it is still being written:
// the compiler's -o lands at "<dir>/.tmp-<name>", and only when the build
// succeeds does the result move onto "<dir>/<name>". Nothing else ever
// writes that spelling, so a ".tmp-" file in the output directory is an
// in-flight build or an orphan of a failed or killed one — never a result,
// which is what a binary at build/<name> must be (see docs/BUILD-OUTPUTS.md).
const TmpPrefix = ".tmp-"

// TempOutputPath returns the path the compiler writes to before the result
// is moved onto final: final's directory, plus ".tmp-" before the base name.
func TempOutputPath(final string) string {
	dir, base := filepath.Split(final)
	return filepath.Join(dir, TmpPrefix+base)
}

// CommitOutput completes a successful build: everything the build wrote
// under the temp spelling of final moves onto its final name.
//
// The compiler is handed TempOutputPath(final) as its -o, so the target file
// never carries a partially written binary: cmd/go touches its -o only after
// the link step is done, and it COPIES onto the -o path (a plain direct
// write, visible mid-copy) when the build cache and the output directory sit
// on different filesystems. This move is a rename inside the output
// directory: atomic where os.Rename is atomic, an in-place replacement
// otherwise — the result appears all at once or not at all, and only when a
// build actually finished.
//
// The cosmo fat-APE build names its sidecar ELFs after the -o path, so those
// come out ".tmp-"-prefixed too and move with the binary: the bare temp name
// and every "<base>.…" shape are this build's. The "<base>_…" shapes are
// separate -o targets with their own commits, and are deliberately NOT swept
// here — a sibling build whose file this moves would be robbed mid-write.
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
		// The temp spelling means not-a-result yet; keep it out of sight
		// while it waits for this move (no-op off Windows). The attribute
		// rides the rename, so the final name is unhidden right after.
		hideFile(src)
		if err := os.Rename(src, dst); err != nil {
			return fmt.Errorf("moving build output into place (%s): %w", dst, err)
		}
		revealFile(dst)
		moved++
	}
	if moved == 0 {
		// The temp output existed at the stat above; on error at this point,
		// failing loudly beats reporting a build nobody can find.
		return fmt.Errorf("build output %s disappeared before it could be moved into place", tmp)
	}
	return nil
}

// DiscardOutput removes a failed build's temp outputs — the -o target under
// its temp spelling plus any "<base>.…" shapes it derived from it — with the
// same stick-to-its-own-shapes rule as CommitOutput. The target file is not
// touched: the compiler never wrote it, so it still looks the way
// clearBuildOutputs left it. Best-effort: the build has already failed, and
// the artifact sweep in cmd/staleoutputs also removes leftover ".tmp-"
// shapes, so an error here only skips one orphan file.
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