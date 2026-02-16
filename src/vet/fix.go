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

// ASTFix represents an AST-based fix: replace OldNode with NewNode.
type ASTFix struct {
	OldNode ast.Node
	NewNode ast.Node // nil for deletion
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
	if fix.NewNode == nil {
		fmt.Printf("%s %s -%s\n", yellow, grey, red)
	} else {
		newStr := strings.TrimSpace(nodeText(f.Fset, fix.NewNode))
		if idx := strings.Index(newStr, "\n"); idx > 0 {
			newStr = newStr[:idx] + "..."
		}
		green := ansi.Style(newStr, ansi.Green.FG())
		fmt.Printf("%s %s %s → %s\n", yellow, grey, red, green)
	}
}

// WriteTo applies fixes and writes the result to w.
func (f *ASTFixes) WriteTo(w io.Writer) error {
	// Apply fixes using astutil.Apply
	astutil.Apply(f.File, nil, func(c *astutil.Cursor) bool {
		node := c.Node()
		for _, fix := range f.Fixes {
			if node == fix.OldNode {
				if fix.NewNode != nil {
					c.Replace(fix.NewNode)
				} else {
					c.Delete()
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
	if err := f.WriteTo(out); err != nil {
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
