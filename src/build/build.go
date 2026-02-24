package build

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/wow-look-at-my/go-toolchain/src/runner"
)

type Target struct {
	ImportPath string
	OutputName string
}

// findMainPackages uses go list to discover all main packages in the module.
func findMainPackages(r runner.CommandRunner) ([]string, error) {
	proc, err := runner.Cmd("go", "list", "-f", `{{if eq .Name "main"}}{{.ImportPath}}{{end}}`, "./...").
		WithQuiet().
		Run(r)
	if err != nil {
		return nil, fmt.Errorf("go list failed: %w", err)
	}
	out, _ := io.ReadAll(proc.Stdout())
	if err := proc.Wait(); err != nil {
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
// and its module name. When the package is at or one level below the module root
// (e.g., module or module/src), the binary is named after the module. When deeper
// (e.g., module/cmd/foo), the binary is named after the leaf directory.
func binaryNameFromImportPath(pkg, moduleName string) string {
	if pkg == moduleName {
		// Package is at module root — use module name
		return filepath.Base(moduleName)
	}
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
// Uses go list to find all main packages in the module.
// Binary names are always auto-derived from the package/directory name.
func ResolveBuildTargets(r runner.CommandRunner) ([]Target, error) {
	// Find all main packages in the module
	pkgs, err := findMainPackages(r)
	if err != nil {
		return nil, err
	}

	// Get module name for smart binary naming
	proc, modErr := runner.Cmd("go", "list", "-m").WithQuiet().Run(r)
	moduleName := ""
	if modErr == nil {
		modOut, _ := io.ReadAll(proc.Stdout())
		proc.Wait()
		moduleName = strings.TrimSpace(string(modOut))
	}

	targets := make([]Target, len(pkgs))
	for i, pkg := range pkgs {
		targets[i] = Target{ImportPath: pkg, OutputName: binaryNameFromImportPath(pkg, moduleName)}
	}
	return targets, nil
}
