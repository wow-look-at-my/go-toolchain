// gateFixedPackages refuses a fix whose package no longer type-checks.

package vet

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/wow-look-at-my/go-containers/set"
	"github.com/wow-look-at-my/go-toolchain/src/buildtags"
	"github.com/wow-look-at-my/go-toolchain/src/logger"
	"golang.org/x/tools/go/packages"
)

// settleFixes ends a round of fixes. It blanks the range variables the round
// left unused, tidies go.mod for the imports it added, and gates the packages
// it touched. With no write since the last gate it does nothing.
func settleFixes(ed Editor, tagCfg buildtags.Config) error {
	a, ok := ed.(*applyEditor)
	if !ok {
		return nil
	}
	files := a.pendingGoFiles()
	if len(files) == 0 {
		return nil
	}
	for _, f := range files {
		if _, err := fixFileUnusedRangeVars(f); err != nil {
			return fmt.Errorf("fixing unused range vars in %s: %w", f, err)
		}
	}
	// The fixes can add an import of testify or the set package.
	if err := runModTidy(); err != nil {
		return err
	}
	reverted, err := gateFixedPackages(ed, tagCfg)
	if err != nil {
		return err
	}
	if reverted {
		// A revert can remove the last use of a module the tidy above added.
		return runModTidy()
	}
	return nil
}

// runModTidy runs go mod tidy with its output on the console.
func runModTidy() error {
	tidyCmd := exec.Command("go", "mod", "tidy")
	tidyCmd.Stdout = os.Stdout
	tidyCmd.Stderr = os.Stderr
	if err := tidyCmd.Run(); err != nil {
		return fmt.Errorf("go mod tidy failed: %w", err)
	}
	return nil
}

// pendingGoFiles lists the Go files written since the last gate.
func (a *applyEditor) pendingGoFiles() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	var files []string
	for path := range a.before {
		if strings.HasSuffix(path, ".go") {
			files = append(files, path)
		}
	}
	sort.Strings(files)
	return files
}

// takeWrites returns the Go files written since the last gate. Each with its
// content from before the first of those writes, and starts a new record.
func (a *applyEditor) takeWrites() map[string][]byte {
	a.mu.Lock()
	defer a.mu.Unlock()
	writes := make(map[string][]byte)
	for path, old := range a.before {
		if strings.HasSuffix(path, ".go") {
			writes[path] = old
		}
	}
	a.before = make(map[string][]byte)
	return writes
}

// revert puts each file back and refuses every later write to it this run.
func (a *applyEditor) revert(files map[string][]byte, report string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	for path, old := range files {
		if err := os.WriteFile(path, old, 0o644); err != nil {
			return fmt.Errorf("reverting the auto-fix of %s: %w", path, err)
		}
		a.refused.Add(path)
	}
	a.reverted = append(a.reverted, report)
	return nil
}

// revertErr names every package a gate reverted, or is nil.
func (a *applyEditor) revertErr() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.reverted) == 0 {
		return nil
	}
	return fmt.Errorf("%s", strings.Join(a.reverted, "\n"))
}

// gateFixedPackages type-checks each package that holds a Go file the editor
// wrote since the last gate. A fixer must never leave code that does not
// compile. So a package that fails gets every written file back as it was,
// and those files take no further fix this run. The revert is logged as an
// error and fails the run. The fixes it undid stay as diagnostics. It returns
// whether it reverted anything. A check editor writes nothing, so it has
// nothing to gate.
func gateFixedPackages(ed Editor, tagCfg buildtags.Config) (bool, error) {
	a, ok := ed.(*applyEditor)
	if !ok {
		return false, nil
	}
	writes := a.takeWrites()
	if len(writes) == 0 {
		return false, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return false, err
	}
	dirs := set.New[string]()
	for path := range writes {
		rel, err := filepath.Rel(cwd, filepath.Dir(path))
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		dirs.Add("./" + filepath.ToSlash(rel))
	}
	patterns := dirs.Values()
	sort.Strings(patterns)
	if len(patterns) == 0 {
		return false, nil
	}

	cfg := &packages.Config{Mode: loadMode(), Tests: true}
	if arg := tagCfg.Arg(); arg != "" {
		cfg.BuildFlags = []string{"-tags", arg}
	}
	pkgs, err := packages.Load(cfg, patterns...)
	if err != nil {
		return false, fmt.Errorf("type-checking the auto-fixed packages: %w", err)
	}

	reverted := false
	for _, pkg := range pkgs {
		var errs []string
		for _, e := range pkg.Errors {
			if msg := e.Error(); !strings.Contains(msg, "package requires newer Go version") {
				errs = append(errs, msg)
			}
		}
		if len(errs) == 0 {
			continue
		}
		files := make(map[string][]byte)
		for _, f := range append(append([]string(nil), pkg.CompiledGoFiles...), pkg.GoFiles...) {
			key := editorKey(f)
			if old, ok := writes[key]; ok {
				files[key] = old
			}
		}
		if len(files) == 0 {
			continue
		}
		names := make([]string, 0, len(files))
		for f := range files {
			names = append(names, f)
		}
		sort.Strings(names)
		report := fmt.Sprintf("auto-fix reverted: package %s did not type-check after the fix, so these files are back as they were and take no fix this run:\n  %s\nthe errors the fix caused:\n  %s",
			pkg.ID, strings.Join(names, "\n  "), strings.Join(errs, "\n  "))
		logger.Error("%s", report)
		if err := a.revert(files, report); err != nil {
			return reverted, err
		}
		for f := range files {
			delete(writes, f)
		}
		reverted = true
	}
	return reverted, nil
}
