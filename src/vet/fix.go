package vet

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/printer"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"strings"

	ansi "github.com/wow-look-at-my/ansi-writer"
	"golang.org/x/tools/go/ast/astutil"
)

// ASTFix represents an AST-based fix: replace OldNode with NewNodes.
type ASTFix struct {
	OldNode  ast.Node
	NewNodes []ast.Node // empty=delete, 1=replace, >1=replace+insert
}

// ASTFixes targets a single file with multiple fixes.
type ASTFixes struct {
	File  *ast.File
	Fset  *token.FileSet
	Fixes []ASTFix
}

// SourceLocation identifies a position in source code.
type SourceLocation struct {
	File   string
	Line   int
	Column int
}

// ShortLoc returns "relative/path:line" for display.
func (s SourceLocation) ShortLoc() string {
	cwd, _ := os.Getwd()
	relPath := s.File
	if rel, err := filepath.Rel(cwd, s.File); err == nil {
		relPath = rel
	}
	return fmt.Sprintf("%s:%d", relPath, s.Line)
}

// nodeText returns the source text for an AST node.
func nodeText(fset *token.FileSet, node ast.Node) string {
	var buf strings.Builder
	printer.Fprint(&buf, fset, node)
	return buf.String()
}

// printFix prints a single fix message showing old (red) and new (green) text.
func (f *ASTFixes) printFix(fix ASTFix) {
	pos := f.Fset.Position(fix.OldNode.Pos())
	loc := SourceLocation{File: pos.Filename, Line: pos.Line, Column: pos.Column}
	fileURL := fmt.Sprintf("vscode://file/%s:%d:%d", loc.File, loc.Line, loc.Column)
	yellow := ansi.Concat(ansi.Yellow.FG, "fixed:", ansi.Reset)
	grey := ansi.Link(fileURL, ansi.Concat(ansi.BrightBlack.FG, loc.ShortLoc(), ansi.Reset))

	// Format old text (red)
	oldStr := strings.TrimSpace(nodeText(f.Fset, fix.OldNode))
	if idx := strings.Index(oldStr, "\n"); idx > 0 {
		oldStr = oldStr[:idx] + "..."
	}
	red := ansi.Concat(ansi.Red.FG, oldStr, ansi.Reset)

	// Format output based on whether this is a deletion or replacement
	if len(fix.NewNodes) == 0 {
		fmt.Printf("%s %s -%s\n", yellow, grey, red)
	} else {
		// Combine all new nodes into a single string
		var newParts []string
		for _, n := range fix.NewNodes {
			part := strings.TrimSpace(nodeText(f.Fset, n))
			if idx := strings.Index(part, "\n"); idx > 0 {
				part = part[:idx] + "..."
			}
			newParts = append(newParts, part)
		}
		newStr := strings.Join(newParts, "; ")
		green := ansi.Concat(ansi.Green.FG, newStr, ansi.Reset)
		fmt.Printf("%s %s %s → %s\n", yellow, grey, red, green)
	}
}

// Fprint applies fixes and writes the result to w.
func (f *ASTFixes) Fprint(w io.Writer) error {
	// Apply fixes using astutil.Apply
	astutil.Apply(f.File, nil, func(c *astutil.Cursor) bool {
		node := c.Node()
		for _, fix := range f.Fixes {
			if node == fix.OldNode {
				if len(fix.NewNodes) == 0 {
					c.Delete()
				} else {
					c.Replace(fix.NewNodes[0])
					// Insert remaining nodes in reverse order (InsertAfter prepends)
					for i := len(fix.NewNodes) - 1; i > 0; i-- {
						c.InsertAfter(fix.NewNodes[i])
					}
				}
				break
			}
		}
		return true
	})

	// Remove comments that were inside replaced nodes. These comments are
	// orphaned after the replacement and would be placed incorrectly by
	// the printer since their positions refer to code that no longer exists.
	f.removeOrphanedComments()

	return printer.Fprint(w, f.Fset, f.File)
}

// removeOrphanedComments removes comment groups from f.File.Comments that fall
// within the source range of any replaced OldNode. Comments before the node
// (leading comments) are preserved.
func (f *ASTFixes) removeOrphanedComments() {
	if len(f.Fixes) == 0 {
		return
	}

	// Collect ranges of replaced nodes
	type posRange struct{ start, end token.Pos }
	var ranges []posRange
	for _, fix := range f.Fixes {
		ranges = append(ranges, posRange{fix.OldNode.Pos(), fix.OldNode.End()})
	}

	filtered := f.File.Comments[:0]
	for _, cg := range f.File.Comments {
		orphaned := false
		for _, r := range ranges {
			if cg.Pos() >= r.start && cg.End() <= r.end {
				orphaned = true
				break
			}
		}
		if !orphaned {
			filtered = append(filtered, cg)
		}
	}
	f.File.Comments = filtered
}

// Apply renders all fixes and routes the result through ed. AST fixes
// (assertnorm/assertlint/redundantcast) also emit an analyzer diagnostic, so on
// CI the diagnostic is what fails the build; ed.Apply writes locally and is a
// no-op on CI (no duplicate violation). Returns whether it wrote.
func (f *ASTFixes) Apply(ed Editor) (bool, error) {
	if len(f.Fixes) == 0 {
		return false, nil
	}

	filename := f.Fset.Position(f.File.Pos()).Filename

	var buf bytes.Buffer
	if err := f.Fprint(&buf); err != nil {
		return false, err
	}

	wrote, err := ed.Apply(filename, buf.Bytes())
	if err != nil {
		return false, err
	}
	if wrote {
		for _, fix := range f.Fixes {
			f.printFix(fix)
		}
	}

	return wrote, nil
}
