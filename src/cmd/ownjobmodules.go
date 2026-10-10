package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/wow-look-at-my/go-toolchain/src/logger"
	"gopkg.in/yaml.v3"
)

// goToolchainActionRef opens the uses: value of a step that runs this action.
const goToolchainActionRef = "wow-look-at-my/go-toolchain@"

// workflowGlobs are the workflow files of a repository, relative to its root.
var workflowGlobs = []string{".github/workflows/*.yml", ".github/workflows/*.yaml"}

// workflowFile is the part of a GitHub Actions workflow this file reads.
type workflowFile struct {
	Jobs map[string]struct {
		Steps []struct {
			Uses string         `yaml:"uses"`
			With map[string]any `yaml:"with"`
		} `yaml:"steps"`
	} `yaml:"jobs"`
}

// repoRootOf answers the nearest directory at or above dir that holds .git, a
// directory in a clone and a file in a submodule or worktree.
func repoRootOf(dir string) string {
	for at := dir; ; {
		if _, err := os.Lstat(filepath.Join(at, ".git")); err == nil {
			return at
		}
		parent := filepath.Dir(at)
		if parent == at {
			return dir
		}
		at = parent
	}
}

// ownJobModules answers each directory, relative to root, that a workflow
// under root builds with a go-toolchain step of its own through the step's
// working-directory input, mapped to the workflow that does it. A step whose
// working-directory is an expression names no directory this can resolve.
func ownJobModules(root string) (map[string]string, error) {
	dirs := make(map[string]string)
	for _, pattern := range workflowGlobs {
		matches, err := filepath.Glob(filepath.Join(root, filepath.FromSlash(pattern)))
		if err != nil {
			return nil, err
		}
		for _, path := range matches {
			if err := addOwnJobModules(root, path, dirs); err != nil {
				return nil, err
			}
		}
	}
	return dirs, nil
}

// addOwnJobModules records in dirs the working directories that the
// go-toolchain steps of the workflow at path build.
func addOwnJobModules(root, path string, dirs map[string]string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var wf workflowFile
	if err := yaml.Unmarshal(data, &wf); err != nil {
		return fmt.Errorf("reading workflow %s: %w", path, err)
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return err
	}
	for _, job := range wf.Jobs {
		for _, step := range job.Steps {
			dir := stepWorkingDirectory(step.Uses, step.With)
			if dir == "" {
				continue
			}
			dirs[dir] = filepath.ToSlash(rel)
		}
	}
	return nil
}

// stepWorkingDirectory answers the cleaned working-directory of a step that
// runs go-toolchain, or "" for any other step, the repository root, and an
// expression.
func stepWorkingDirectory(uses string, with map[string]any) string {
	if !strings.HasPrefix(uses, goToolchainActionRef) {
		return ""
	}
	value, ok := with["working-directory"].(string)
	if !ok || strings.Contains(value, "${{") {
		return ""
	}
	dir := filepath.Clean(filepath.FromSlash(strings.TrimSpace(value)))
	if dir == "." {
		return ""
	}
	return dir
}

// sweptModules answers the modules this run builds: findGoModules, less every
// nested module a workflow builds in a go-toolchain step of its own. That step
// carries the module's own settings (cgo, platforms, the libraries it
// installs), and a sweep from here would build it with this run's instead.
func sweptModules() ([]string, error) {
	modules := findGoModules()
	wd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	root := repoRootOf(wd)
	owned, err := ownJobModules(root)
	if err != nil {
		return nil, fmt.Errorf("finding the modules a workflow builds in a step of its own: %w", err)
	}
	return withoutOwnJobModules(modules, wd, root, owned)
}

// withoutOwnJobModules drops each module of modules, relative to wd, that
// owned names relative to root. The module at wd itself always stays.
func withoutOwnJobModules(modules []string, wd, root string, owned map[string]string) ([]string, error) {
	kept := make([]string, 0, len(modules))
	for _, mod := range modules {
		fromRoot, err := filepath.Rel(root, filepath.Join(wd, mod))
		if err != nil {
			return nil, err
		}
		workflow, ok := owned[fromRoot]
		if mod == "." || !ok {
			kept = append(kept, mod)
			continue
		}
		logger.Info("Module %s: skipped, %s builds it in a go-toolchain step of its own", mod, workflow)
	}
	return kept, nil
}
