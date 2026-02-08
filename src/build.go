package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type buildTarget struct {
	importPath string
	outputName string
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

// resolveBuildTargets determines what to build and what to name the binaries.
// When --src is set explicitly, builds that single path.
// Otherwise, uses go list to find all main packages.
func resolveBuildTargets(runner CommandRunner) ([]buildTarget, error) {
	if srcPath != "." {
		name := output
		if name == "" {
			name = filepath.Base(srcPath)
		}
		return []buildTarget{{importPath: srcPath, outputName: name}}, nil
	}

	// Check if there are Go files in the current directory
	goFiles, _ := filepath.Glob("*.go")
	if len(goFiles) > 0 {
		// Main package in root — original behavior
		name := output
		if name == "" {
			wd, err := os.Getwd()
			if err != nil {
				return nil, fmt.Errorf("failed to get working directory: %w", err)
			}
			name = filepath.Base(wd)
		}
		return []buildTarget{{importPath: ".", outputName: name}}, nil
	}

	// No Go files in root — find main packages in subdirectories
	pkgs, err := findMainPackages(runner)
	if err != nil {
		return nil, err
	}

	if output != "" && len(pkgs) > 1 {
		return nil, fmt.Errorf("-o flag cannot be used with multiple main packages; use --src to pick one")
	}

	targets := make([]buildTarget, len(pkgs))
	for i, pkg := range pkgs {
		name := output
		if name == "" {
			name = filepath.Base(pkg)
		}
		targets[i] = buildTarget{importPath: pkg, outputName: name}
	}
	return targets, nil
}
