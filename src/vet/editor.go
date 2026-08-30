package vet

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/wow-look-at-my/go-containers/set"
)

// Editor is the single sink for vet's autofixes. It is built from whether
// go-toolchain may rewrite the tree (locally) or must only verify it (CI), so
// individual fixers never branch on CI themselves: they compute the canonical
// bytes for a file and hand them to the Editor, which either applies the change
// or reports it. This is the only place the apply-vs-report decision lives.
type Editor interface {
	// Require writes canonical content locally, or records a CI violation; use when this is the issue's only detector.
	Require(path string, want []byte, reason string) (wrote bool, err error)

	// Apply writes locally and no-ops on CI; use when the issue already surfaces as its own analyzer diagnostic.
	Apply(path string, want []byte) (wrote bool, err error)

	// Err returns the accumulated CI violations, or nil when applying locally or already canonical.
	Err() error

	// Writes reports whether this editor persists to disk; fixers use it only to skip write-only preconditions.
	Writes() bool

	// Wrote reports whether this editor already rewrote path during this run.
	// The uncommitted-changes guard asks so it can tell an edit this run made
	// from one the user made: refusing the first strands a tree half-fixed
	// whenever two fixers reach the same file.
	Wrote(path string) bool
}

// NewEditor returns an apply-on-disk editor when fix is true, or a report-only editor on CI.
func NewEditor(fix bool) Editor {
	if fix {
		return &applyEditor{written: set.New[string]()}
	}
	return &checkEditor{}
}

// applyEditor writes proposed changes to disk; Require and Apply behave identically since locally both get written.
// It remembers what it wrote: fixers run concurrently, so the record is locked.
type applyEditor struct {
	mu      sync.Mutex
	written set.Set[string]
}

func (a *applyEditor) Require(path string, want []byte, _ string) (bool, error) {
	return a.write(path, want)
}

func (a *applyEditor) Apply(path string, want []byte) (bool, error) {
	return a.write(path, want)
}

func (a *applyEditor) write(path string, want []byte) (bool, error) {
	wrote, err := writeIfDiffer(path, want)
	if wrote {
		a.mu.Lock()
		a.written.Add(editorKey(path))
		a.mu.Unlock()
	}
	return wrote, err
}

func (a *applyEditor) Wrote(path string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.written.Contains(editorKey(path))
}

func (applyEditor) Err() error { return nil }

func (applyEditor) Writes() bool { return true }

// editorKey normalizes a path so the same file recorded through two spellings
// answers Wrote the same way.
func editorKey(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		return abs
	}
	return filepath.Clean(path)
}

// checkEditor records Require changes as violations; Apply changes are dropped (reported elsewhere as a diagnostic).
type checkEditor struct {
	violations []violation
}

// violation pairs a file needing change with a precomputed diff to canonical content.
type violation struct {
	path, reason, diff string
}

func (c *checkEditor) Require(path string, want []byte, reason string) (bool, error) {
	old, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	if bytes.Equal(old, want) {
		return false, nil
	}
	diff, err := unifiedDiff(path, old, want)
	if err != nil {
		return false, err
	}
	c.violations = append(c.violations, violation{path: path, reason: reason, diff: diff})
	return false, nil
}

func (c *checkEditor) Apply(string, []byte) (bool, error) { return false, nil }

func (c *checkEditor) Writes() bool { return false }

func (c *checkEditor) Wrote(string) bool { return false }

func (c *checkEditor) Err() error {
	if len(c.violations) == 0 {
		return nil
	}
	sort.Slice(c.violations, func(i, j int) bool {
		if c.violations[i].reason != c.violations[j].reason {
			return c.violations[i].reason < c.violations[j].reason
		}
		return c.violations[i].path < c.violations[j].path
	})
	var sb strings.Builder
	sb.WriteString("working tree is not canonical — run `go-toolchain` locally to fix. The diff to the canonical content is below for each file, so this is answerable without running anything:")
	for _, v := range c.violations {
		fmt.Fprintf(&sb, "\n  %s (%s)", v.path, v.reason)
	}
	for _, v := range c.violations {
		sb.WriteString("\n\n")
		sb.WriteString(v.diff)
	}
	return fmt.Errorf("%s", strings.TrimRight(sb.String(), "\n"))
}

// writeIfDiffer writes want to path only when it differs from the current
// contents, reporting whether it wrote.
func writeIfDiffer(path string, want []byte) (bool, error) {
	differs, err := fileDiffers(path, want)
	if err != nil {
		return false, err
	}
	if !differs {
		return false, nil
	}
	if err := os.WriteFile(path, want, 0o644); err != nil {
		return false, err
	}
	return true, nil
}

// fileDiffers reports whether the file at path differs from want.
func fileDiffers(path string, want []byte) (bool, error) {
	old, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	return !bytes.Equal(old, want), nil
}
