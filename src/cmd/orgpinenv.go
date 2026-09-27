package cmd

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"

	"github.com/wow-look-at-my/go-toolchain/src/logger"
)

// orgPinEnv names the variable the fork's go command reads org pins from.
const orgPinEnv = "GOORGPIN"

// orgPinFormat prints each module the build graph holds, and the target of a replace.
const orgPinFormat = "{{if not .Main}}{{.Path}}={{.Version}}{{with .Replace}} {{.Path}}={{.Version}}{{end}}{{end}}"

// orgPinsInCI reports whether this run may pin org modules. Only a test replaces it.
var orgPinsInCI = isGHA

// listOrgModules answers what `go list -m all` prints in dir, in orgPinFormat.
var listOrgModules = func(dir string) (string, error) {
	cmd := exec.Command("go", "list", "-mod=readonly", "-m", "-f", orgPinFormat, "all")
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("go list -m all in %s: %w\n%s", dir, err, strings.TrimSpace(stderr.String()))
	}
	return string(out), nil
}

// pinOrgModules resolves every org module once, at the start of a CI run, and
// exports the result to each go command the run starts. Otherwise each go
// command resolves a branch head of its own, and the passes of a single run
// can compile different commits of a dependency. A pin the workflow set wins.
// Outside CI it does nothing, and CheckCIOnlyEnv refuses a GOORGPIN set there.
func pinOrgModules(modules []string) error {
	if !orgPinsInCI() {
		return nil
	}
	if set := os.Getenv(orgPinEnv); set != "" {
		logOrgPins("set by the workflow", strings.Fields(set))
		return nil
	}
	pins, err := resolveOrgPins(modules)
	if err != nil {
		return fmt.Errorf("pinning the org modules for this run: %w", err)
	}
	if len(pins) == 0 {
		return nil
	}
	if err := os.Setenv(orgPinEnv, strings.Join(pins, " ")); err != nil {
		return fmt.Errorf("exporting %s: %w", orgPinEnv, err)
	}
	logOrgPins("resolved at the start of this run", pins)
	return nil
}

// resolveOrgPins answers a sorted `path=version` entry for each org module
// that any of modules requires. The modules must agree on each version.
func resolveOrgPins(modules []string) ([]string, error) {
	versions := map[string]string{}
	for _, dir := range modules {
		out, err := listOrgModules(dir)
		if err != nil {
			return nil, err
		}
		for _, entry := range strings.Fields(out) {
			path, version, ok := strings.Cut(entry, "=")
			if !ok || !isOrgModulePath(path) || version == "" {
				continue
			}
			if !looksLikeVersionToken(version) {
				return nil, fmt.Errorf("%s: go list answered %q, which is not a version", dir, entry)
			}
			if prev, seen := versions[path]; seen && prev != version {
				return nil, fmt.Errorf("%s resolves to %s in one module and to %s in %s", path, prev, version, dir)
			}
			versions[path] = version
		}
	}
	pins := make([]string, 0, len(versions))
	for path, version := range versions {
		pins = append(pins, path+"="+version)
	}
	sort.Strings(pins)
	return pins, nil
}

// logOrgPins prints each pin on a line of its own, so the log names every commit the run built.
func logOrgPins(source string, pins []string) {
	logger.Output("⇒ org pins, %s:", source)
	for _, p := range pins {
		logger.Output("   %s", p)
	}
}
