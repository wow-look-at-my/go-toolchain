package vet

import (
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
	yellow := ansi.Style("fixed:", ansi.Yellow.FG())
	grey := ansi.Link(fileURL, ansi.Style(loc.ShortLoc(), ansi.BrightBlack.FG()))

	// Format old text (red)
	oldStr := strings.TrimSpace(nodeText(f.Fset, fix.OldNode))
	if idx := strings.Index(oldStr, "\n"); idx > 0 {
		oldStr = oldStr[:idx] + "..."
	}
	red := ansi.Style(oldStr, ansi.Red.FG())

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
		green := ansi.Style(newStr, ansi.Green.FG())
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

	return printer.Fprint(w, f.Fset, f.File)
}

// Apply applies all fixes to the file and writes it back.
func (f *ASTFixes) Apply() error {
	if len(f.Fixes) == 0 {
		return nil
	}

	filename := f.Fset.Position(f.File.Pos()).Filename

	out, err := os.Create(filename)
	if err != nil {
		return err
	}
	if err := f.Fprint(out); err != nil {
		out.Close()
		return err
	}
	out.Close()

	// Print all fixes
	for _, fix := range f.Fixes {
		f.printFix(fix)
	}

	return nil
}
