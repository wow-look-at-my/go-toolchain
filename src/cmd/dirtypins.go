package cmd

import (
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"golang.org/x/mod/modfile"

	"github.com/wow-look-at-my/go-containers/set"
)

// trackedPinMoves maps a module directory to the modules whose branch-tracked
// pin this run re-resolved, from `git status --short`. A directory is absent
// when its go.mod changed some other way, so the exclusion below never covers
// a real edit. A go.sum shares its go.mod's directory key.
func trackedPinMoves(statusOut string) map[string][]string {
	moves := map[string][]string{}
	for _, line := range strings.Split(statusOut, "\n") {
		path := statusLinePath(line)
		if filepath.Base(path) != "go.mod" {
			continue
		}
		if moved, ok := movedTrackedPins(path); ok {
			moves[filepath.Dir(path)] = moved
		}
	}
	return moves
}

// movedTrackedPins reports the modules whose branch-tracked version the
// working tree moved, relative to HEAD, and whether that movement is the ONLY
// change to the file.
func movedTrackedPins(path string) ([]string, bool) {
	head, err := exec.Command("git", "show", "HEAD:./"+path).Output()
	if err != nil {
		return nil, false // untracked, or no HEAD to compare against
	}
	work, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	return trackedPinMovement(head, work)
}

// trackedPinMovement compares two go.mod files and reports which
// branch-tracked lines moved to a different commit, and whether restoring
// those versions makes the two files identical.
//
// Restoring and re-comparing is what makes this precise: anything else the
// working tree changed -- a require added or dropped, a comment edited, a go
// directive bumped, a marker that appeared or disappeared -- survives the
// restore and is reported as the dirt it is. Only the version token on a line
// that carries the same branch marker in both files is forgiven, because that
// token is the one thing the marker says nobody chooses by hand.
func trackedPinMovement(headData, workData []byte) ([]string, bool) {
	hf, err := modfile.Parse("go.mod", headData, nil)
	if err != nil {
		return nil, false
	}
	wf, err := modfile.Parse("go.mod", workData, nil)
	if err != nil {
		return nil, false
	}

	moved := map[string]bool{}
	restore := map[string]string{}
	for _, req := range wf.Require {
		m := parseMarker(req.Syntax)
		if !m.tracks {
			continue
		}
		head := findRequire(hf, req.Mod.Path)
		if head == nil || parseMarker(head.Syntax) != m || head.Mod.Version == req.Mod.Version {
			continue
		}
		moved[req.Mod.Path] = true
		restore[req.Mod.Path] = head.Mod.Version
	}
	for mod, version := range restore {
		if err := wf.AddRequire(mod, version); err != nil {
			return nil, false
		}
	}

	for _, rep := range wf.Replace {
		m := parseMarker(rep.Syntax)
		if !m.tracks {
			continue
		}
		head := findReplace(hf, rep.Old.Path, rep.Old.Version)
		if head == nil || parseMarker(head.Syntax) != m {
			continue
		}
		if head.New.Path != rep.New.Path || head.New.Version == rep.New.Version {
			continue
		}
		moved[rep.New.Path] = true
		if err := wf.AddReplace(rep.Old.Path, rep.Old.Version, head.New.Path, head.New.Version); err != nil {
			return nil, false
		}
	}

	if len(moved) == 0 {
		return nil, false
	}
	restored, err := wf.Format()
	if err != nil {
		return nil, false
	}
	original, err := hf.Format()
	if err != nil {
		return nil, false
	}
	if string(restored) != string(original) {
		return nil, false
	}
	return slices.Sorted(maps.Keys(moved)), true
}

// findReplace returns the replace line for an old path and version, or nil.
func findReplace(f *modfile.File, oldPath, oldVersion string) *modfile.Replace {
	for _, rep := range f.Replace {
		if rep.Old.Path == oldPath && rep.Old.Version == oldVersion {
			return rep
		}
	}
	return nil
}

// goSumFollowsPins reports whether every line the working tree added to or
// removed from a go.sum names a module whose tracked pin moved. A hash for
// anything else is a change the pin movement does not account for, so it is
// reported rather than forgiven.
func goSumFollowsPins(path string, moved []string) bool {
	head, err := exec.Command("git", "show", "HEAD:./"+path).Output()
	if err != nil {
		return false
	}
	work, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return sumDiffOnlyTouches(string(head), string(work), moved)
}

// sumDiffOnlyTouches reports whether the lines present in exactly one of two
// go.sum files all belong to modules in moved.
func sumDiffOnlyTouches(head, work string, moved []string) bool {
	allowed := set.Of(moved...)
	headLines := sumLines(head)
	workLines := sumLines(work)
	for line := range headLines.All() {
		if !workLines.Contains(line) && !allowed.Contains(sumLineModule(line)) {
			return false
		}
	}
	for line := range workLines.All() {
		if !headLines.Contains(line) && !allowed.Contains(sumLineModule(line)) {
			return false
		}
	}
	return true
}

// sumLines returns a go.sum's non-blank lines as a set.
func sumLines(data string) set.Set[string] {
	out := set.New[string]()
	for _, line := range strings.Split(data, "\n") {
		if strings.TrimSpace(line) != "" {
			out.Add(line)
		}
	}
	return out
}

// sumLineModule returns the module path a go.sum line is about.
func sumLineModule(line string) string {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}
