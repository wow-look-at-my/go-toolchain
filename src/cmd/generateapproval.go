package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/mod/modfile"
)

<<<<<<< HEAD
// generateMarker records a module's approved go:generate hash in go.mod.
=======
// generateMarker records the approved go:generate hash of a module.
>>>>>>> origin/master
const generateMarker = "go-toolchain:generate="

// parseGenerateMarker reads the approved generate hash off a go.mod line, or ""
// when the line records none. Matched by substring so it is found beside an
// indirect comment.
func parseGenerateMarker(line *modfile.Line) string {
	if line == nil {
		return ""
	}
	for _, c := range line.Suffix {
		if i := strings.Index(c.Token, generateMarker); i != -1 {
			return strings.TrimRight(markerValue(c.Token[i+len(generateMarker):]), ";")
		}
	}
	return ""
}

// setGenerateMarker replaces any generate approval on a line with hash, joined
// to an existing comment the same way x/mod's setIndirect joins "// indirect".
// A further Suffix comment would render on its own line below, which corrupts
// the block.
func setGenerateMarker(line *modfile.Line, hash string) {
	kept := line.Suffix[:0]
	for _, c := range line.Suffix {
		token := stripMarks(c.Token, generateMarker)
		if token == "" {
			continue
		}
		c.Token = token
		kept = append(kept, c)
	}
	line.Suffix = kept
	mark := generateMarker + hash
	if len(line.Suffix) == 0 {
		line.Suffix = []modfile.Comment{{Token: "// " + mark, Suffix: true}}
		return
	}
	line.Suffix[0].Token += "; " + mark
}

// lineText spells a go.mod line as it reads inside its block, comment included.
func lineText(line *modfile.Line) string {
	parts := append([]string(nil), line.Token...)
	for _, c := range line.Suffix {
		parts = append(parts, c.Token)
	}
	return strings.Join(parts, " ")
}

// markerValue takes a marker's value off the front of the rest of a comment:
// up to the leading space, so a trailing note stays readable.
func markerValue(rest string) string {
	fields := strings.Fields(rest)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

// stripMarks removes each named marker and its value from a comment token,
// returning "" when nothing but markers was there.
func stripMarks(token string, marks ...string) string {
	for _, mark := range marks {
		i := strings.Index(token, mark)
		if i == -1 {
			continue
		}
		rest := token[i+len(mark):]
		if _, after, found := strings.Cut(rest, " "); found {
			token = token[:i] + after // a trailing note after the marker stays
		} else {
			token = token[:i]
		}
	}
	token = strings.TrimRight(strings.TrimSpace(token), ";")
	if token == "//" {
		return ""
	}
	return strings.TrimSpace(token)
}

// approvedGenerateHash is --generate, else the module line's marker. Depth: docs/PIPELINE.md
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
// or the replace line of a fork whose replacement path is the path cached.
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
