package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wow-look-at-my/testify/assert"
)

func TestNormalizeNpmVersion(t *testing.T) {
	cases := []struct {
		in   string
		want string
		err  bool
	}{
		{"1.2.3", "1.2.3", false},
		{"v1.2.3", "1.2.3", false},
		{"v0.0.1759872345", "0.0.1759872345", false},
		{"v1.2.3-rc.1", "1.2.3-rc.1", false},
		{"", "", true},
		{"v", "", true},
		{"1.2", "", true},                    // missing patch
		{"v1.2.3-4-g1234567", "", true},      // git describe with sha suffix
		{"abc", "", true},                    // not numeric
		{"1.2.x", "", true},                  // non-numeric component
	}
	for _, tc := range cases {
		got, err := normalizeNpmVersion(tc.in)
		if tc.err {
			assert.Error(t, err, "input %q", tc.in)
			continue
		}
		assert.NoError(t, err, "input %q", tc.in)
		assert.Equal(t, tc.want, got, "input %q", tc.in)
	}
}

func TestInferScopeFromRegistry(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"https://git.pazer.us/api/packages/pazer/npm/", "@pazer"},
		{"https://gitea.example.com/api/packages/some-org/npm/", "@some-org"},
		{"https://git.pazer.us/api/packages/pazer/npm", "@pazer"},
		{"https://npm.pkg.github.com", ""},
		{"", ""},
		{"https://git.pazer.us/api/packages//npm/", ""},
	}
	for _, tc := range cases {
		got := inferScopeFromRegistry(tc.in)
		assert.Equal(t, tc.want, got, "input %q", tc.in)
	}
}

func TestPlatformPackageName(t *testing.T) {
	got := platformPackageName("@pazer", "go-toolchain", "linux", "x64")
	assert.Equal(t, "@pazer/go-toolchain-linux-x64", got)
}

func TestDiscoverNpmBinariesParses(t *testing.T) {
	dir := t.TempDir()
	// Create the layout `matrix` produces.
	files := []string{
		"go-toolchain_linux_amd64",
		"go-toolchain_linux_arm64",
		"go-toolchain_darwin_amd64",
		"go-toolchain_darwin_arm64",
		"go-toolchain_windows_amd64.exe",
		"go-toolchain_windows_arm64.exe",
		"checksums.txt",
		"checksums.txt.sig",
	}
	for _, f := range files {
		assert.NoError(t, os.WriteFile(filepath.Join(dir, f), []byte("binary"), 0o755))
	}
	// A symlink alias that should be ignored.
	assert.NoError(t, os.Symlink("go-toolchain_linux_amd64", filepath.Join(dir, "go-toolchain")))

	binaries, err := discoverNpmBinaries(dir, "go-toolchain")
	assert.NoError(t, err)
	assert.Equal(t, 6, len(binaries))

	// Verify mappings (results are sorted by npmOS, npmArch).
	assert.Equal(t, "darwin", binaries[0].npmOS)
	assert.Equal(t, "arm64", binaries[0].npmArch)
	assert.Equal(t, "darwin", binaries[1].npmOS)
	assert.Equal(t, "x64", binaries[1].npmArch)
	assert.Equal(t, "linux", binaries[2].npmOS)
	assert.Equal(t, "arm64", binaries[2].npmArch)
	assert.Equal(t, "linux", binaries[3].npmOS)
	assert.Equal(t, "x64", binaries[3].npmArch)
	assert.Equal(t, "win32", binaries[4].npmOS)
	assert.Equal(t, "arm64", binaries[4].npmArch)
	assert.Equal(t, "win32", binaries[5].npmOS)
	assert.Equal(t, "x64", binaries[5].npmArch)
	assert.Equal(t, ".exe", binaries[5].exeSuffix)
}

func TestDiscoverNpmBinariesFiltersByName(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{
		"go-toolchain_linux_amd64",
		"some-other-tool_linux_amd64",
	} {
		assert.NoError(t, os.WriteFile(filepath.Join(dir, f), []byte("x"), 0o755))
	}

	binaries, err := discoverNpmBinaries(dir, "go-toolchain")
	assert.NoError(t, err)
	assert.Equal(t, 1, len(binaries))
	assert.Equal(t, "go-toolchain", binaries[0].binaryName)
}

func TestDiscoverNpmBinariesSkipsUnknownPlatforms(t *testing.T) {
	dir := t.TempDir()
	// plan9 is not in our mapping, so this entry must be skipped.
	for _, f := range []string{
		"go-toolchain_linux_amd64",
		"go-toolchain_plan9_amd64",
		"go-toolchain_linux_riscv64",
	} {
		assert.NoError(t, os.WriteFile(filepath.Join(dir, f), []byte("x"), 0o755))
	}
	binaries, err := discoverNpmBinaries(dir, "go-toolchain")
	assert.NoError(t, err)
	assert.Equal(t, 1, len(binaries))
	assert.Equal(t, "linux", binaries[0].npmOS)
	assert.Equal(t, "x64", binaries[0].npmArch)
}

func TestPlatformPackageJSON(t *testing.T) {
	b := discoveredBinary{npmOS: "linux", npmArch: "x64"}
	pkg := platformPackageJSON("@pazer/go-toolchain-linux-x64", "1.2.3", "go-toolchain", "bin/go-toolchain", b)

	assert.Equal(t, "@pazer/go-toolchain-linux-x64", pkg["name"])
	assert.Equal(t, "1.2.3", pkg["version"])
	assert.Equal(t, []string{"linux"}, pkg["os"])
	assert.Equal(t, []string{"x64"}, pkg["cpu"])
	bins := pkg["bin"].(map[string]string)
	assert.Equal(t, "bin/go-toolchain", bins["go-toolchain"])
}

func TestWrapperPackageJSONHasOptionalDeps(t *testing.T) {
	deps := map[string]string{
		"@pazer/go-toolchain-linux-x64":  "1.2.3",
		"@pazer/go-toolchain-darwin-x64": "1.2.3",
	}
	pkg := wrapperPackageJSON("@pazer/go-toolchain", "1.2.3", "go-toolchain", deps)
	assert.Equal(t, "@pazer/go-toolchain", pkg["name"])
	assert.Equal(t, "1.2.3", pkg["version"])
	bins := pkg["bin"].(map[string]string)
	assert.Equal(t, "bin/go-toolchain.js", bins["go-toolchain"])
	got := pkg["optionalDependencies"].(map[string]string)
	assert.Equal(t, "1.2.3", got["@pazer/go-toolchain-linux-x64"])
	assert.Equal(t, "1.2.3", got["@pazer/go-toolchain-darwin-x64"])
}

func TestWrapperShimContainsScopeAndName(t *testing.T) {
	shim := wrapperShim("@pazer", "go-toolchain")
	assert.Contains(t, shim, `"@pazer"`)
	assert.Contains(t, shim, `"go-toolchain"`)
	// must be a node script
	assert.True(t, strings.HasPrefix(shim, "#!/usr/bin/env node"))
	// must use process.platform / process.arch
	assert.Contains(t, shim, "process.platform")
	assert.Contains(t, shim, "process.arch")
}

// fakeNpmExecutor records publish calls and returns canned git output.
type fakeNpmExecutor struct {
	publishCalls   []fakePublish
	gitOutputFunc  func(args ...string) (string, error)
	publishReturns error
}

type fakePublish struct {
	dir      string
	registry string
	access   string
}

func (f *fakeNpmExecutor) publish(dir, registry, access string) error {
	f.publishCalls = append(f.publishCalls, fakePublish{dir: dir, registry: registry, access: access})
	return f.publishReturns
}

func (f *fakeNpmExecutor) gitOutput(args ...string) (string, error) {
	if f.gitOutputFunc != nil {
		return f.gitOutputFunc(args...)
	}
	return "", nil
}

func TestRunNpmPublishImplGeneratesAndPublishes(t *testing.T) {
	tmp := t.TempDir()
	buildDir := filepath.Join(tmp, "build")
	assert.NoError(t, os.MkdirAll(buildDir, 0o755))
	for _, f := range []string{
		"go-toolchain_linux_amd64",
		"go-toolchain_darwin_arm64",
		"go-toolchain_windows_amd64.exe",
	} {
		assert.NoError(t, os.WriteFile(filepath.Join(buildDir, f), []byte("BIN-"+f), 0o755))
	}

	// Seed flags via package globals (init() registered them on rootCmd).
	saved := saveNpmFlags()
	defer saved.restore()
	npmRegistry = "https://git.pazer.us/api/packages/pazer/npm/"
	npmTag = "v1.2.3"
	npmName = "go-toolchain"
	npmBuildDir = buildDir
	npmOutDir = filepath.Join(tmp, "out")
	npmDryRun = false
	npmAccess = "public"
	npmScope = "" // exercise inference

	ex := &fakeNpmExecutor{}
	err := runNpmPublishImpl(ex, os.Stderr)
	assert.NoError(t, err)

	// One platform package per binary plus the wrapper.
	assert.Equal(t, 4, len(ex.publishCalls))

	// Wrapper is published last.
	last := ex.publishCalls[len(ex.publishCalls)-1]
	assert.Contains(t, last.dir, "pazer__go-toolchain")
	assert.True(t, !strings.Contains(filepath.Base(last.dir), "linux") &&
		!strings.Contains(filepath.Base(last.dir), "darwin") &&
		!strings.Contains(filepath.Base(last.dir), "win32"),
		"last publish must be the wrapper, got %s", last.dir)

	for _, c := range ex.publishCalls {
		assert.Equal(t, "https://git.pazer.us/api/packages/pazer/npm/", c.registry)
		assert.Equal(t, "public", c.access)
	}

	// Verify wrapper package.json has correct optionalDependencies.
	wrapperPkgPath := filepath.Join(last.dir, "package.json")
	data, err := os.ReadFile(wrapperPkgPath)
	assert.NoError(t, err)
	var wrapper map[string]any
	assert.NoError(t, json.Unmarshal(data, &wrapper))
	assert.Equal(t, "@pazer/go-toolchain", wrapper["name"])
	assert.Equal(t, "1.2.3", wrapper["version"])
	deps, _ := wrapper["optionalDependencies"].(map[string]any)
	assert.Equal(t, "1.2.3", deps["@pazer/go-toolchain-linux-x64"])
	assert.Equal(t, "1.2.3", deps["@pazer/go-toolchain-darwin-arm64"])
	assert.Equal(t, "1.2.3", deps["@pazer/go-toolchain-win32-x64"])

	// Verify a platform package has the binary copied with executable mode.
	linuxDir := filepath.Join(npmOutDir, "pazer__go-toolchain-linux-x64")
	binPath := filepath.Join(linuxDir, "bin", "go-toolchain")
	info, err := os.Stat(binPath)
	assert.NoError(t, err)
	assert.True(t, info.Mode()&0o100 != 0, "binary must be executable, got mode %v", info.Mode())

	body, err := os.ReadFile(binPath)
	assert.NoError(t, err)
	assert.Equal(t, "BIN-go-toolchain_linux_amd64", string(body))

	// Windows package keeps the .exe suffix.
	winBin := filepath.Join(npmOutDir, "pazer__go-toolchain-win32-x64", "bin", "go-toolchain.exe")
	_, err = os.Stat(winBin)
	assert.NoError(t, err)

	// Wrapper has the JS shim under bin/.
	shimPath := filepath.Join(last.dir, "bin", "go-toolchain.js")
	shim, err := os.ReadFile(shimPath)
	assert.NoError(t, err)
	assert.Contains(t, string(shim), `"@pazer"`)
}

func TestRunNpmPublishImplDryRun(t *testing.T) {
	tmp := t.TempDir()
	buildDir := filepath.Join(tmp, "build")
	assert.NoError(t, os.MkdirAll(buildDir, 0o755))
	assert.NoError(t, os.WriteFile(filepath.Join(buildDir, "go-toolchain_linux_amd64"), []byte("x"), 0o755))

	saved := saveNpmFlags()
	defer saved.restore()
	npmRegistry = "https://git.pazer.us/api/packages/pazer/npm/"
	npmTag = "1.2.3"
	npmName = "go-toolchain"
	npmBuildDir = buildDir
	npmOutDir = filepath.Join(tmp, "out")
	npmDryRun = true

	ex := &fakeNpmExecutor{}
	assert.NoError(t, runNpmPublishImpl(ex, os.Stderr))
	assert.Equal(t, 0, len(ex.publishCalls))
	// But the package directories should still exist.
	_, err := os.Stat(filepath.Join(npmOutDir, "pazer__go-toolchain-linux-x64", "package.json"))
	assert.NoError(t, err)
	_, err = os.Stat(filepath.Join(npmOutDir, "pazer__go-toolchain", "package.json"))
	assert.NoError(t, err)
}

func TestRunNpmPublishImplRequiresScope(t *testing.T) {
	tmp := t.TempDir()
	buildDir := filepath.Join(tmp, "build")
	assert.NoError(t, os.MkdirAll(buildDir, 0o755))
	assert.NoError(t, os.WriteFile(filepath.Join(buildDir, "go-toolchain_linux_amd64"), []byte("x"), 0o755))

	saved := saveNpmFlags()
	defer saved.restore()
	npmRegistry = "https://npm.pkg.github.com" // no /api/packages/<owner>/ pattern
	npmTag = "1.2.3"
	npmName = "go-toolchain"
	npmBuildDir = buildDir
	npmOutDir = filepath.Join(tmp, "out")
	npmScope = ""

	ex := &fakeNpmExecutor{}
	err := runNpmPublishImpl(ex, os.Stderr)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "scope")
}

func TestRunNpmPublishImplFailsWhenNoBinaries(t *testing.T) {
	tmp := t.TempDir()
	buildDir := filepath.Join(tmp, "build")
	assert.NoError(t, os.MkdirAll(buildDir, 0o755))
	// Only a checksums file -- no binaries.
	assert.NoError(t, os.WriteFile(filepath.Join(buildDir, "checksums.txt"), []byte("x"), 0o644))

	saved := saveNpmFlags()
	defer saved.restore()
	npmRegistry = "https://git.pazer.us/api/packages/pazer/npm/"
	npmTag = "1.2.3"
	npmName = "go-toolchain"
	npmBuildDir = buildDir
	npmOutDir = filepath.Join(tmp, "out")

	ex := &fakeNpmExecutor{}
	err := runNpmPublishImpl(ex, os.Stderr)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no binaries found")
}

func TestRunNpmPublishImplStopsOnPublishError(t *testing.T) {
	tmp := t.TempDir()
	buildDir := filepath.Join(tmp, "build")
	assert.NoError(t, os.MkdirAll(buildDir, 0o755))
	for _, f := range []string{
		"go-toolchain_linux_amd64",
		"go-toolchain_darwin_arm64",
	} {
		assert.NoError(t, os.WriteFile(filepath.Join(buildDir, f), []byte("x"), 0o755))
	}

	saved := saveNpmFlags()
	defer saved.restore()
	npmRegistry = "https://git.pazer.us/api/packages/pazer/npm/"
	npmTag = "1.2.3"
	npmName = "go-toolchain"
	npmBuildDir = buildDir
	npmOutDir = filepath.Join(tmp, "out")

	ex := &fakeNpmExecutor{publishReturns: assertErr("boom")}
	err := runNpmPublishImpl(ex, os.Stderr)
	assert.Error(t, err)
	// Only the first publish should be attempted before bailing.
	assert.Equal(t, 1, len(ex.publishCalls))
}

func TestRunNpmPublishImplUsesGitDescribeWhenNoTag(t *testing.T) {
	tmp := t.TempDir()
	buildDir := filepath.Join(tmp, "build")
	assert.NoError(t, os.MkdirAll(buildDir, 0o755))
	assert.NoError(t, os.WriteFile(filepath.Join(buildDir, "go-toolchain_linux_amd64"), []byte("x"), 0o755))

	saved := saveNpmFlags()
	defer saved.restore()
	npmRegistry = "https://git.pazer.us/api/packages/pazer/npm/"
	npmTag = ""
	npmName = "go-toolchain"
	npmBuildDir = buildDir
	npmOutDir = filepath.Join(tmp, "out")
	npmDryRun = true

	ex := &fakeNpmExecutor{
		gitOutputFunc: func(args ...string) (string, error) {
			return "v9.8.7", nil
		},
	}
	assert.NoError(t, runNpmPublishImpl(ex, os.Stderr))

	// The wrapper's package.json must record the version we got from git describe.
	pkgPath := filepath.Join(npmOutDir, "pazer__go-toolchain", "package.json")
	data, err := os.ReadFile(pkgPath)
	assert.NoError(t, err)
	var wrapper map[string]any
	assert.NoError(t, json.Unmarshal(data, &wrapper))
	assert.Equal(t, "9.8.7", wrapper["version"])
}

// --- helpers -----------------------------------------------------------------

type savedNpmFlags struct {
	tag, registry, scope, name, buildDir, outDir, access string
	dryRun                                               bool
}

func saveNpmFlags() savedNpmFlags {
	return savedNpmFlags{
		tag:      npmTag,
		registry: npmRegistry,
		scope:    npmScope,
		name:     npmName,
		buildDir: npmBuildDir,
		outDir:   npmOutDir,
		dryRun:   npmDryRun,
		access:   npmAccess,
	}
}

func (s savedNpmFlags) restore() {
	npmTag = s.tag
	npmRegistry = s.registry
	npmScope = s.scope
	npmName = s.name
	npmBuildDir = s.buildDir
	npmOutDir = s.outDir
	npmDryRun = s.dryRun
	npmAccess = s.access
}

type assertErr string

func (e assertErr) Error() string { return string(e) }
