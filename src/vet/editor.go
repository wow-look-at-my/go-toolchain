package vet

import (
	"bytes"
	"fmt"
	"os"
	"sort"
	"strings"
)

// Editor is the single sink for vet's autofixes. It is built once from whether
// go-toolchain may rewrite the tree (locally) or must only verify it (CI), so
// individual fixers never branch on CI themselves: they compute the canonical
// bytes for a file and hand them to the Editor, which either applies the change
// or reports it. This is the one place the apply-vs-report decision lives.
type Editor interface {
	// Require declares want as the required canonical content of path. Locally
	// it writes the file when it differs (returning wrote=true); on CI it
	// records a violation (reason, plus a unified diff from the file's current
	// content to want) when it differs and writes nothing (returning
	// wrote=false). Use it for fixers that are the SOLE detector of their issue
	// — gofmt, the wow-look-at-my/testify fork and gotest.tools import
	// migrations, and the testify cross-type casts — so the recorded violation
	// is what fails CI.
	Require(path string, want []byte, reason string) (wrote bool, err error)

	// Apply writes want to path locally (returning wrote=true when it changed
	// the file) and is a no-op on CI (returning wrote=false). Use it for fixes
	// whose issue is ALSO surfaced as an analyzer diagnostic, so CI fails
	// through that precise diagnostic rather than a duplicate, coarser violation.
	Apply(path string, want []byte) (wrote bool, err error)

	// Err returns the accumulated CI violations, or nil when applying locally or
	// when the tree is already canonical.
	Err() error

	// Writes reports whether this editor persists changes to disk (true locally,
	// false on CI). Fixers consult it ONLY to skip write-only preconditions —
	// e.g. the uncommitted-changes guard that protects local edits from being
	// clobbered by an applied fix — never to re-derive apply-vs-report behavior.
	Writes() bool
}

// NewEditor returns an apply-on-disk editor when fix is true (the local
// default) or a report-only editor when fix is false (CI). This is the only
// place go-toolchain turns the CI flag into fix-vs-check behavior.
func NewEditor(fix bool) Editor {
	if fix {
		return &applyEditor{}
	}
	return &checkEditor{}
}

// applyEditor writes proposed changes to disk. Require and Apply behave
// identically — locally there is no difference between "must be canonical" and
// "convenience fix"; both are written.
type applyEditor struct{}

func (applyEditor) Require(path string, want []byte, _ string) (bool, error) {
	return writeIfDiffer(path, want)
}

func (applyEditor) Apply(path string, want []byte) (bool, error) {
	return writeIfDiffer(path, want)
}

func (applyEditor) Err() error { return nil }

func (applyEditor) Writes() bool { return true }

// checkEditor records proposed Require changes as violations instead of writing,
// and drops Apply changes (their issue is reported elsewhere as a diagnostic).
type checkEditor struct {
	violations []violation
}

// violation records one file the tree needs changed to be canonical, along
// with a ready-to-read unified diff from its current content to the wanted
// content — computed once, at detection time, while the "want" bytes and the
// still-unmodified file are both in hand. Err() prints it for every reader
// that cannot just run go-toolchain locally and look: most usefully, an
// automated agent with no code-execution capability, for whom this diff — not
// the file+reason line above it — is the actual answer.
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
