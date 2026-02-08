package build

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// CommandRunner abstracts command execution for testing
type CommandRunner interface {
	Run(name string, args ...string) error
	RunWithOutput(name string, args ...string) ([]byte, error)
}

type Target struct {
	ImportPath string
	OutputName string
}

// findMainPackages uses go list to discover all main packages in the module.
func findMainPackages(runner CommandRunner) ([]string, error) {
	out, err := runner.RunWithOutput("go", "list", "-f", `{{if eq .Name "main"}}{{.ImportPath}}{{end}}`, "./...")
	if err != nil {
		return nil, fmt.Errorf("go list failed: %w", err)
	}

	var pkgs []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			pkgs = append(pkgs, line)
		}
	}
	return pkgs, nil
}

// binaryNameFromImportPath derives a binary name from a package's import path
// and its module name. When the package is one level below the module root
// (e.g., module/src), the binary is named after the module. When deeper
// (e.g., module/cmd/foo), the binary is named after the leaf directory.
func binaryNameFromImportPath(pkg, moduleName string) string {
	rel := strings.TrimPrefix(pkg, moduleName+"/")
	if rel == pkg {
		// Package doesn't start with module prefix — just use basename
		return filepath.Base(pkg)
	}
	if !strings.Contains(rel, "/") {
		// Single level below module root (e.g., "src") — use module name
		return filepath.Base(moduleName)
	}
	// Deeper path (e.g., "cmd/foo") — use leaf directory
	return filepath.Base(pkg)
}

// ResolveBuildTargets determines what to build and what to name the binaries.
// When srcPath is set explicitly (not "."), builds that single path.
// Otherwise, uses go list to find all main packages.
// Binary names are always auto-derived from the package/directory name.
func ResolveBuildTargets(runner CommandRunner, srcPath string) ([]Target, error) {
	if srcPath != "." {
		return []Target{{ImportPath: srcPath, OutputName: filepath.Base(srcPath)}}, nil
	}

	// Check if there are Go files in the current directory
	goFiles, _ := filepath.Glob("*.go")
	if len(goFiles) > 0 {
		// Main package in root — name from working directory
		wd, err := os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("failed to get working directory: %w", err)
		}
		return []Target{{ImportPath: ".", OutputName: filepath.Base(wd)}}, nil
	}

	// No Go files in root — find main packages in subdirectories
	pkgs, err := findMainPackages(runner)
	if err != nil {
		return nil, err
	}

	// Get module name for smart binary naming
	modOut, modErr := runner.RunWithOutput("go", "list", "-m")
	moduleName := ""
	if modErr == nil {
		moduleName = strings.TrimSpace(string(modOut))
	}

	targets := make([]Target, len(pkgs))
	for i, pkg := range pkgs {
		targets[i] = Target{ImportPath: pkg, OutputName: binaryNameFromImportPath(pkg, moduleName)}
	}
	return targets, nil
}
