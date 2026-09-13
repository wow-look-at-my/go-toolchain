package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/mod/modfile"
)

// Approvals live in go.mod, as a generateMarker comment. The module line carries
// the hash of this tree's own directives, and each dependency's require line the
// hash of that dependency's. Depth: docs/PIPELINE.md

// approvedGenerateHash is the hash this tree's own directives may run under:
// --generate for a single run, else the module line's record.
func approvedGenerateHash() string {
	if generateHash != "" {
		return generateHash
	}
	f, err := readGoModFile(".")
	if err != nil || f.Module == nil {
		return ""
	}
	return parseGenerateMarker(f.Module.Syntax)
}

// readGoModFile parses the go.mod under root.
func readGoModFile(root string) (*modfile.File, error) {
	path := filepath.Join(root, "go.mod")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return modfile.Parse(path, data, nil)
}

// moduleLine is the go.mod line that speaks for a dependency: its require line,
// or the replace line of a fork whose replacement path is the one cached.
func moduleLine(f *modfile.File, path string) *modfile.Line {
	for _, r := range f.Require {
		if r.Mod.Path == path {
			return r.Syntax
		}
	}
	for _, r := range f.Replace {
		if r.New.Path == path {
			return r.Syntax
		}
	}
	return nil
}

// dependencyApproval reads the hash go.mod approves for a dependency's directives.
func dependencyApproval(f *modfile.File, path string) string {
	if f == nil {
		return ""
	}
	return parseGenerateMarker(moduleLine(f, path))
}

// approvedLine spells the go.mod line that records hash for a module, as the
// reader should write it. The parsed file is left as it was.
func approvedLine(f *modfile.File, path, version, hash string) string {
	var line *modfile.Line
	if f != nil {
		line = moduleLine(f, path)
	}
	if line == nil {
		return fmt.Sprintf("require %s %s // %s%s", path, version, generateMarker, hash)
	}
	return markedText(line, hash)
}

// approvedModuleLine spells the module line of the go.mod under root recording
// hash, or names the marker alone when there is no module line to copy.
func approvedModuleLine(root, hash string) string {
	f, err := readGoModFile(root)
	if err != nil || f.Module == nil {
		return "// " + generateMarker + hash
	}
	return markedText(f.Module.Syntax, hash)
}

// markedText spells line with hash recorded on it, leaving line itself alone.
func markedText(line *modfile.Line, hash string) string {
	marked := &modfile.Line{
		Token:    append([]string(nil), line.Token...),
		Comments: modfile.Comments{Suffix: append([]modfile.Comment(nil), line.Suffix...)},
	}
	setGenerateMarker(marked, hash)
	return lineText(marked)
}
