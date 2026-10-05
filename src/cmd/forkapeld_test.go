package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/go-toolchain/src/runner"
)

// apeldRunner answers the loader build's commands from a table keyed by
// program and first argument, and records every command it ran.
type apeldRunner struct {
	answers map[string]string
	fail    string
	ran     []runner.Config
}

func (a *apeldRunner) Run(cfg runner.Config) (runner.IProcess, error) {
	a.ran = append(a.ran, cfg)
	key := filepath.Base(cfg.Name)
	if len(cfg.Args) > 0 {
		key += " " + cfg.Args[0]
	}
	if key == a.fail {
		return runner.MockProcess(nil, errors.New("exit status 1")), nil
	}
	return runner.MockProcess([]byte(a.answers[key]), nil), nil
}

// fakeApeldTools puts every loader tool at a path under dir.
func fakeApeldTools(t *testing.T, dir string, absent ...string) {
	t.Helper()
	old := apeldLookPath
	t.Cleanup(func() { apeldLookPath = old })
	apeldLookPath = func(name string) (string, error) {
		for _, a := range absent {
			if a == name {
				return "", errors.New("not found")
			}
		}
		return filepath.Join(dir, name), nil
	}
}

func zigEnv(libDir string) string {
	return ".{\n    .zig_exe = \"/x/zig\",\n    .lib_dir = \"" + libDir + "\",\n    .std_dir = \"/x/std\",\n}\n"
}

func TestBuildForkApeLoadersRunsDistsCommands(t *testing.T) {
	t.Serial()
	goroot := t.TempDir()
	libDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(libDir, "libc", "darwin"), 0o755))
	fakeApeldTools(t, "/tools")
	r := &apeldRunner{answers: map[string]string{
		"zig version":        apeldZigVersion + "\n",
		"ld64.lld --version": "Homebrew LLD 18.1.8 (compatible with GNU linkers)\n",
		"zig env":            zigEnv(libDir),
	}}

	require.NoError(t, buildForkApeLoaders(r, goroot))

	dir := filepath.Join(goroot, filepath.FromSlash(apeldDir))
	assert.DirExists(t, filepath.Join(dir, "bin"))
	var lines []string
	for _, c := range r.ran {
		lines = append(lines, filepath.Base(c.Name)+" "+strings.Join(c.Args, " "))
		if strings.HasPrefix(strings.Join(c.Args, " "), "cc ") || filepath.Base(c.Name) == "llvm-strip" {
			assert.Equal(t, dir, c.Dir, "a compile runs in the loader source directory")
		}
	}
	all := strings.Join(lines, "\n")
	assert.Contains(t, all, "zig cc -target x86_64-linux-musl")
	assert.Contains(t, all, "-o bin/apeld-linux-amd64 linux/apeld.c")
	assert.Contains(t, all, "llvm-strip --strip-sections bin/apeld-linux-amd64")
	assert.Contains(t, all, "zig cc -target aarch64-linux-musl")
	assert.Contains(t, all, "llvm-strip --strip-sections bin/apeld-linux-arm64")
	assert.Contains(t, all, "zig cc -target aarch64-macos")
	assert.Contains(t, all, "-nostdinc -isystem "+filepath.Join(libDir, "include"))
	assert.Contains(t, all, "ld64.lld -arch arm64 -platform_version macos 12.0 12.0 -o bin/apeld-darwin-arm64")
	assert.Contains(t, all, "-L"+filepath.Join(libDir, "libc", "darwin")+" -lSystem")
}

func TestBuildForkApeLoadersNamesMissingTools(t *testing.T) {
	t.Serial()
	fakeApeldTools(t, "/tools", "zig", "llvm-strip")
	err := buildForkApeLoaders(&apeldRunner{}, t.TempDir())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "zig: zig "+apeldZigVersion)
	assert.Contains(t, err.Error(), "llvm-strip: LLVM "+apeldLLDVersion)
	assert.NotContains(t, err.Error(), "ld64.lld:")
}

func TestBuildForkApeLoadersRefusesOtherVersions(t *testing.T) {
	t.Serial()
	fakeApeldTools(t, "/tools")

	err := buildForkApeLoaders(&apeldRunner{answers: map[string]string{"zig version": "0.15.1\n"}}, t.TempDir())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "need zig "+apeldZigVersion)

	err = buildForkApeLoaders(&apeldRunner{answers: map[string]string{
		"zig version":        apeldZigVersion,
		"ld64.lld --version": "Ubuntu LLD 18.1.3 (compatible with GNU linkers)",
	}}, t.TempDir())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "need LLD "+apeldLLDVersion)
}

func TestBuildForkApeLoadersReportsAFailedCompile(t *testing.T) {
	t.Serial()
	fakeApeldTools(t, "/tools")
	r := &apeldRunner{
		answers: map[string]string{"zig version": apeldZigVersion, "ld64.lld --version": "LLD 18.1.8"},
		fail:    "llvm-strip --strip-sections",
	}
	err := buildForkApeLoaders(r, t.TempDir())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "llvm-strip --strip-sections bin/apeld-linux-amd64")
}

func TestBuildForkApeLoadersNeedsZigsDarwinStubs(t *testing.T) {
	t.Serial()
	fakeApeldTools(t, "/tools")
	libDir := t.TempDir()
	r := &apeldRunner{answers: map[string]string{
		"zig version":        apeldZigVersion,
		"ld64.lld --version": "LLD 18.1.8",
		"zig env":            zigEnv(libDir),
	}}
	err := buildForkApeLoaders(r, t.TempDir())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "darwin libc stubs")
}

func TestLLDVersion(t *testing.T) {
	t.Serial()
	assert.Equal(t, "18.1.8", lldVersion("Homebrew LLD 18.1.8 (compatible with GNU linkers)"))
	assert.Equal(t, "18.1.3", lldVersion("Ubuntu LLD 18.1.3 (compatible with GNU linkers)\n"))
	assert.Equal(t, "", lldVersion("ld64.lld: nothing"))
	assert.Equal(t, "", lldVersion("LLD"))
}

func TestParseZigLibDir(t *testing.T) {
	t.Serial()
	dir, err := parseZigLibDir(zigEnv("/opt/zig/lib"))
	require.NoError(t, err)
	assert.Equal(t, "/opt/zig/lib", dir)

	_, err = parseZigLibDir(".{\n    .lib_dir = not-quoted,\n}\n")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot read lib_dir")

	_, err = parseZigLibDir(".{}\n")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "names no lib_dir")
}
