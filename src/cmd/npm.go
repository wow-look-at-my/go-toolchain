package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/wow-look-at-my/go-toolchain/src/gomod"
)

// goosToNpmOS maps Go GOOS values to Node.js process.platform values.
// Only platforms that npm's `os` field accepts and that produce useful
// binaries for typical Go projects are included.
var goosToNpmOS = map[string]string{
	"linux":   "linux",
	"darwin":  "darwin",
	"windows": "win32",
	"freebsd": "freebsd",
	"openbsd": "openbsd",
}

// goarchToNpmArch maps Go GOARCH values to Node.js process.arch values.
var goarchToNpmArch = map[string]string{
	"amd64": "x64",
	"arm64": "arm64",
	"386":   "ia32",
	"arm":   "arm",
}

var (
	npmTag      string
	npmRegistry string
	npmScope    string
	npmName     string
	npmBuildDir string
	npmOutDir   string
	npmDryRun   bool
	npmAccess   string
	npmDistTag  string
)

func init() {
	cmd := &cobra.Command{
		Use:   "npm-publish",
		Short: "Publish per-platform binary npm packages to a Gitea/npm registry",
		Long: `Generate and publish npm packages for cross-compiled binaries.

Produces one wrapper package that selects the right binary at runtime, and one
binary-only package per (os, arch) target. The wrapper depends on the
per-platform packages as optionalDependencies so npm only installs the one
matching the consumer's platform.

Authentication is read from the standard .npmrc the workflow has already
configured (typically a //<host>/path/:_authToken= line). This command does
not write or read tokens itself.`,
		SilenceUsage: true,
		RunE:         runNpmPublish,
	}
	cmd.Flags().StringVar(&npmTag, "tag", "", "Version to publish (leading 'v' is stripped); defaults to git describe")
	cmd.Flags().StringVar(&npmRegistry, "registry", "", "Target npm registry URL (e.g. https://git.pazer.us/api/packages/wow-look-at-my/npm/)")
	cmd.Flags().StringVar(&npmScope, "scope", "", "npm scope including the leading @ (defaults to @<owner> from a Gitea-style registry)")
	cmd.Flags().StringVar(&npmName, "name", "", "Package name without scope (defaults to the Go module's basename)")
	cmd.Flags().StringVar(&npmBuildDir, "build-dir", "build", "Directory containing built binaries to package")
	cmd.Flags().StringVar(&npmOutDir, "out-dir", "build/npm", "Directory where generated package directories are written")
	cmd.Flags().BoolVar(&npmDryRun, "dry-run", false, "Generate package directories but do not run npm publish")
	cmd.Flags().StringVar(&npmAccess, "access", "public", "Value passed to npm publish --access")
	cmd.Flags().StringVar(&npmDistTag, "dist-tag", "", "npm dist-tag for the publish (defaults to npm's 'latest'); use a per-branch tag like 'branch-feat-x' for prerelease/branch builds so consumers don't pick them up by default")
	rootCmd.AddCommand(cmd)
}

// npmExecutor abstracts npm command execution so tests can replace it.
type npmExecutor interface {
	publish(packageDir, registry, access, distTag string) error
	gitOutput(args ...string) (string, error)
}

type realNpmExecutor struct{}

func (realNpmExecutor) publish(packageDir, registry, access, distTag string) error {
	args := []string{"publish"}
	if access != "" {
		args = append(args, "--access", access)
	}
	if registry != "" {
		args = append(args, "--registry", registry)
	}
	if distTag != "" {
		// npm publish's --tag sets the dist-tag, NOT the version. We use
		// our --tag flag for the version, and pass --dist-tag through here.
		args = append(args, "--tag", distTag)
	}
	cmd := exec.Command("npm", args...)
	cmd.Dir = packageDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func (realNpmExecutor) gitOutput(args ...string) (string, error) {
	out, err := exec.Command("git", args...).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func runNpmPublish(cmd *cobra.Command, args []string) error {
	return runNpmPublishImpl(realNpmExecutor{}, os.Stderr)
}

func runNpmPublishImpl(ex npmExecutor, logOut io.Writer) error {
	cfg, err := resolveNpmConfig(ex)
	if err != nil {
		return err
	}

	binaries, err := discoverNpmBinaries(cfg.buildDir, cfg.binaryName)
	if err != nil {
		return fmt.Errorf("discover binaries in %s: %w", cfg.buildDir, err)
	}
	if len(binaries) == 0 {
		return fmt.Errorf("no binaries found in %s matching name %q (run `go-toolchain matrix` first)", cfg.buildDir, cfg.binaryName)
	}

	if err := os.MkdirAll(cfg.outDir, 0o755); err != nil {
		return fmt.Errorf("create output dir %s: %w", cfg.outDir, err)
	}

	// Generate per-platform packages first so the wrapper's
	// optionalDependencies map matches what we actually built.
	platformDirs := make([]string, 0, len(binaries))
	platformDeps := make(map[string]string, len(binaries))
	for _, b := range binaries {
		dir, fullName, err := writePlatformPackage(cfg, b)
		if err != nil {
			return fmt.Errorf("write platform package for %s/%s: %w", b.goos, b.goarch, err)
		}
		platformDirs = append(platformDirs, dir)
		platformDeps[fullName] = cfg.version
		fmt.Fprintf(logOut, "  prepared %s -> %s\n", fullName, dir)
	}

	wrapperDir, wrapperName, err := writeWrapperPackage(cfg, platformDeps)
	if err != nil {
		return fmt.Errorf("write wrapper package: %w", err)
	}
	fmt.Fprintf(logOut, "  prepared %s -> %s\n", wrapperName, wrapperDir)

	if cfg.dryRun {
		fmt.Fprintf(logOut, "⇒ Dry run: skipped npm publish for %d package(s)\n", len(platformDirs)+1)
		return nil
	}

	// Publish platform packages before the wrapper. The wrapper's
	// optionalDependencies reference the platform packages by exact version,
	// so the registry must already have them when consumers install the wrapper.
	for i, dir := range platformDirs {
		fmt.Fprintf(logOut, "⇒ npm publish [%d/%d] %s\n", i+1, len(platformDirs)+1, dir)
		if err := ex.publish(dir, cfg.registry, cfg.access, cfg.distTag); err != nil {
			return fmt.Errorf("publish %s: %w", dir, err)
		}
	}
	fmt.Fprintf(logOut, "⇒ npm publish [%d/%d] %s\n", len(platformDirs)+1, len(platformDirs)+1, wrapperDir)
	if err := ex.publish(wrapperDir, cfg.registry, cfg.access, cfg.distTag); err != nil {
		return fmt.Errorf("publish %s: %w", wrapperDir, err)
	}

	fmt.Fprintf(logOut, "⇒ Published %d npm package(s) at version %s\n", len(platformDirs)+1, cfg.version)
	return nil
}

// npmConfig is the resolved configuration after applying flag defaults.
type npmConfig struct {
	registry   string
	scope      string
	binaryName string
	version    string
	buildDir   string
	outDir     string
	dryRun     bool
	access     string
	distTag    string
}

func resolveNpmConfig(ex npmExecutor) (npmConfig, error) {
	cfg := npmConfig{
		registry: strings.TrimSpace(npmRegistry),
		scope:    strings.TrimSpace(npmScope),
		buildDir: npmBuildDir,
		outDir:   npmOutDir,
		dryRun:   npmDryRun,
		access:   npmAccess,
		distTag:  strings.TrimSpace(npmDistTag),
	}

	// Scope: explicit, then inferred from a Gitea-style registry path.
	if cfg.scope == "" && cfg.registry != "" {
		cfg.scope = inferScopeFromRegistry(cfg.registry)
	}
	if cfg.scope == "" {
		return cfg, fmt.Errorf("npm scope is required (pass --scope @<owner> or use a Gitea-style --registry)")
	}
	if !strings.HasPrefix(cfg.scope, "@") {
		return cfg, fmt.Errorf("scope %q must start with '@'", cfg.scope)
	}

	// Package name: explicit flag, else the Go module's basename.
	cfg.binaryName = strings.TrimSpace(npmName)
	if cfg.binaryName == "" {
		mod := gomod.ReadModulePath()
		if mod == "" {
			return cfg, fmt.Errorf("--name is required (no go.mod found to infer from)")
		}
		cfg.binaryName = filepath.Base(mod)
	}

	// Version: explicit flag, else git describe. Strip leading 'v' for npm.
	tag := strings.TrimSpace(npmTag)
	if tag == "" {
		out, err := ex.gitOutput("describe", "--tags", "--always")
		if err != nil {
			return cfg, fmt.Errorf("--tag not given and `git describe` failed: %w", err)
		}
		tag = out
	}
	v, err := normalizeNpmVersion(tag)
	if err != nil {
		return cfg, err
	}
	cfg.version = v

	return cfg, nil
}

// gitDescribeSuffix matches the trailing "-<count>-g<hex>" that `git describe`
// appends to a tag when the working tree is past it. The leading hyphen and
// the digit-count anchor it precisely enough that legitimate prerelease
// identifiers containing 'g' (e.g. "feat-graceful-shutdown") do not match.
var gitDescribeSuffix = regexp.MustCompile(`-\d+-g[0-9a-f]+$`)

// normalizeNpmVersion strips a leading 'v' and validates the result is a
// plausible npm semver. npm enforces strict semver on publish, so we want
// to fail fast with a useful message rather than let `npm publish` report
// "invalid version" buried in stderr.
func normalizeNpmVersion(tag string) (string, error) {
	v := strings.TrimPrefix(tag, "v")
	if v == "" {
		return "", fmt.Errorf("empty version")
	}
	// Reject git describe output ending in "-N-g<sha>", which is not valid
	// semver and would produce confusing tarball names.
	if gitDescribeSuffix.MatchString(v) {
		return "", fmt.Errorf("version %q looks like `git describe` output (-N-g<sha>); pass --tag with a clean semver like 1.2.3", tag)
	}
	// Minimal semver shape check: must start with a digit and contain at
	// least two dots' worth of numeric components.
	parts := strings.SplitN(v, "-", 2)
	core := parts[0]
	nums := strings.Split(core, ".")
	if len(nums) < 3 {
		return "", fmt.Errorf("version %q is not semver (need MAJOR.MINOR.PATCH)", tag)
	}
	for _, n := range nums {
		if n == "" {
			return "", fmt.Errorf("version %q has empty component", tag)
		}
		for _, c := range n {
			if c < '0' || c > '9' {
				return "", fmt.Errorf("version %q has non-numeric component %q", tag, n)
			}
		}
	}
	return v, nil
}

// inferScopeFromRegistry extracts an npm scope from a Gitea-style registry URL
// of the form https://<host>/api/packages/<owner>/npm/. Returns "" if the URL
// doesn't follow that pattern.
func inferScopeFromRegistry(registry string) string {
	const marker = "/api/packages/"
	idx := strings.Index(registry, marker)
	if idx < 0 {
		return ""
	}
	rest := registry[idx+len(marker):]
	slash := strings.Index(rest, "/")
	if slash <= 0 {
		return ""
	}
	owner := rest[:slash]
	if owner == "" {
		return ""
	}
	return "@" + owner
}

// discoveredBinary describes a built binary that should be packaged.
type discoveredBinary struct {
	binaryPath string // path on disk to the built binary
	binaryName string // logical name without _<goos>_<goarch> suffix
	goos       string
	goarch     string
	npmOS      string
	npmArch    string
	exeSuffix  string // ".exe" on Windows, "" elsewhere
}

// discoverNpmBinaries walks buildDir for files matching `<binaryName>_<goos>_<goarch>[.exe]`
// and returns the ones whose (goos, goarch) we have npm mappings for. Symlinks
// (host aliases) and checksum files are skipped.
func discoverNpmBinaries(buildDir, binaryName string) ([]discoveredBinary, error) {
	entries, err := os.ReadDir(buildDir)
	if err != nil {
		return nil, err
	}

	var binaries []discoveredBinary
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		// Skip checksum/signature files (they're not binaries).
		if strings.HasPrefix(name, "checksums") {
			continue
		}
		full := filepath.Join(buildDir, name)
		info, err := os.Lstat(full)
		if err != nil {
			continue
		}
		// Skip symlinks: matrix produces host-name aliases (e.g. `go-toolchain`,
		// `go-toolchain_host`) for local convenience that point at the real files
		// we already iterate over.
		if info.Mode()&os.ModeSymlink != 0 {
			continue
		}

		base := name
		exeSuffix := ""
		if strings.HasSuffix(base, ".exe") {
			exeSuffix = ".exe"
			base = strings.TrimSuffix(base, ".exe")
		}
		parts := strings.Split(base, "_")
		if len(parts) < 3 {
			continue
		}
		goarch := parts[len(parts)-1]
		goos := parts[len(parts)-2]
		gotName := strings.Join(parts[:len(parts)-2], "_")
		if gotName != binaryName {
			continue
		}
		npmOS, ok := goosToNpmOS[goos]
		if !ok {
			continue
		}
		npmArch, ok := goarchToNpmArch[goarch]
		if !ok {
			continue
		}
		binaries = append(binaries, discoveredBinary{
			binaryPath: full,
			binaryName: gotName,
			goos:       goos,
			goarch:     goarch,
			npmOS:      npmOS,
			npmArch:    npmArch,
			exeSuffix:  exeSuffix,
		})
	}

	// Stable order: sort by (npmOS, npmArch) for deterministic output.
	sort.Slice(binaries, func(i, j int) bool {
		if binaries[i].npmOS != binaries[j].npmOS {
			return binaries[i].npmOS < binaries[j].npmOS
		}
		return binaries[i].npmArch < binaries[j].npmArch
	})

	return binaries, nil
}

// writePlatformPackage materializes a per-(os, arch) package directory.
// Returns the directory path and the fully-qualified package name.
func writePlatformPackage(cfg npmConfig, b discoveredBinary) (string, string, error) {
	fullName := platformPackageName(cfg.scope, cfg.binaryName, b.npmOS, b.npmArch)
	// Use a filesystem-safe directory name (npm scopes contain a slash).
	dirName := strings.ReplaceAll(strings.TrimPrefix(fullName, "@"), "/", "__")
	pkgDir := filepath.Join(cfg.outDir, dirName)
	binDir := filepath.Join(pkgDir, "bin")
	if err := os.RemoveAll(pkgDir); err != nil {
		return "", "", err
	}
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return "", "", err
	}

	binTarget := filepath.Join(binDir, cfg.binaryName+b.exeSuffix)
	if err := copyExecutable(b.binaryPath, binTarget); err != nil {
		return "", "", fmt.Errorf("copy binary: %w", err)
	}

	binEntry := "bin/" + cfg.binaryName + b.exeSuffix
	pkg := platformPackageJSON(fullName, cfg.version, cfg.binaryName, binEntry, b)
	if err := writeJSONFile(filepath.Join(pkgDir, "package.json"), pkg); err != nil {
		return "", "", err
	}

	return pkgDir, fullName, nil
}

// platformPackageName builds the fully-qualified npm name for a platform
// package: `<scope>/<base>-<npmOS>-<npmArch>`.
func platformPackageName(scope, base, npmOS, npmArch string) string {
	return scope + "/" + base + "-" + npmOS + "-" + npmArch
}

// platformPackageJSON returns the package.json contents for a platform package.
// Note: the binary is renamed to `bin/<base>[.exe]` inside the package so the
// wrapper can locate it by a stable path rather than the matrix-shaped
// `<base>_<goos>_<goarch>` filename.
func platformPackageJSON(fullName, version, baseName, binEntry string, b discoveredBinary) map[string]any {
	return map[string]any{
		"name":        fullName,
		"version":     version,
		"description": fmt.Sprintf("%s binary for %s/%s", baseName, b.npmOS, b.npmArch),
		"os":          []string{b.npmOS},
		"cpu":         []string{b.npmArch},
		"bin": map[string]string{
			baseName: binEntry,
		},
		"files": []string{"bin/"},
	}
}

// writeWrapperPackage materializes the top-level wrapper package, which
// contains the JS shim and an optionalDependencies map listing every
// per-platform package.
func writeWrapperPackage(cfg npmConfig, platformDeps map[string]string) (string, string, error) {
	fullName := cfg.scope + "/" + cfg.binaryName
	dirName := strings.ReplaceAll(strings.TrimPrefix(fullName, "@"), "/", "__")
	pkgDir := filepath.Join(cfg.outDir, dirName)
	binDir := filepath.Join(pkgDir, "bin")
	if err := os.RemoveAll(pkgDir); err != nil {
		return "", "", err
	}
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return "", "", err
	}

	shimPath := filepath.Join(binDir, cfg.binaryName+".js")
	shim := wrapperShim(cfg.scope, cfg.binaryName)
	if err := os.WriteFile(shimPath, []byte(shim), 0o755); err != nil {
		return "", "", err
	}

	pkg := wrapperPackageJSON(fullName, cfg.version, cfg.binaryName, platformDeps)
	if err := writeJSONFile(filepath.Join(pkgDir, "package.json"), pkg); err != nil {
		return "", "", err
	}

	return pkgDir, fullName, nil
}

// wrapperPackageJSON returns the package.json contents for the wrapper package.
func wrapperPackageJSON(fullName, version, baseName string, platformDeps map[string]string) map[string]any {
	return map[string]any{
		"name":        fullName,
		"version":     version,
		"description": fmt.Sprintf("%s wrapper that selects the right binary for the host platform", baseName),
		"bin": map[string]string{
			baseName: "bin/" + baseName + ".js",
		},
		"optionalDependencies": platformDeps,
		"files":                []string{"bin/"},
	}
}

// wrapperShim is the JS that gets installed as the wrapper's `bin` entry. It
// resolves the matching @scope/<name>-<os>-<arch> package via require.resolve,
// reads its package.json to find the bin entry, and execs the binary forwarding
// stdio and the exit code.
func wrapperShim(scope, baseName string) string {
	// scope and baseName are validated upstream (scope starts with @, baseName
	// is the Go module basename), so JS-string-escaping is not needed.
	return `#!/usr/bin/env node
"use strict";
const path = require("node:path");
const fs = require("node:fs");
const { spawnSync } = require("node:child_process");
const { createRequire } = require("node:module");

const SCOPE = ` + jsString(scope) + `;
const NAME = ` + jsString(baseName) + `;

function platformPackage() {
  return SCOPE + "/" + NAME + "-" + process.platform + "-" + process.arch;
}

function resolveBinary() {
  const pkgName = platformPackage();
  const req = createRequire(__filename);
  let pkgJsonPath;
  try {
    pkgJsonPath = req.resolve(pkgName + "/package.json");
  } catch (err) {
    throw new Error(
      "Cannot find platform package " + pkgName + ". " +
      "Your platform (" + process.platform + "/" + process.arch + ") may not be supported, " +
      "or installation of optional dependencies was disabled."
    );
  }
  const pkg = JSON.parse(fs.readFileSync(pkgJsonPath, "utf8"));
  const rel = pkg.bin && pkg.bin[NAME];
  if (!rel) {
    throw new Error("Platform package " + pkgName + " is missing bin." + NAME);
  }
  return path.join(path.dirname(pkgJsonPath), rel);
}

const result = spawnSync(resolveBinary(), process.argv.slice(2), {
  stdio: "inherit",
  windowsHide: true,
});
if (result.error) {
  console.error(result.error.message);
  process.exit(1);
}
process.exit(result.status === null ? 1 : result.status);
`
}

// jsString returns a JSON-encoded JavaScript string literal for s. JSON is a
// strict subset of JS object literal syntax, so json.Marshal output is a valid
// JS string.
func jsString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// writeJSONFile encodes v to path with two-space indentation and a trailing newline.
func writeJSONFile(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return os.WriteFile(path, b, 0o644)
}

// copyExecutable copies src to dst and ensures the result has 0755 permissions.
// actions/upload-artifact strips the execute bit, so binaries arriving via
// download-artifact lose +x; npm publish preserves whatever mode is on disk
// when packing the tarball, so consumers would get a non-executable binary
// without this explicit chmod.
func copyExecutable(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	// OpenFile honors umask, so re-chmod explicitly.
	return os.Chmod(dst, 0o755)
}
