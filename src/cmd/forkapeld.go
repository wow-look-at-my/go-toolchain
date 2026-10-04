package cmd

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/wow-look-at-my/go-toolchain/src/runner"
)

// The zig and LLD the fork's cmd/dist requires for the APE loaders, whose
// bytes cmd/link's tests pin. ld64.lld writes its own version.
const (
	apeldZigVersion = "0.16.0"
	apeldLLDVersion = "18.1.8"
)

const apeldLLVMInstall = "LLVM " + apeldLLDVersion + ": the llvm-18 and lld-18 packages from apt.llvm.org (then put /usr/lib/llvm-18/bin on PATH), " +
	"brew install llvm@18, or LLVM-" + apeldLLDVersion + "-win64.exe from https://github.com/llvm/llvm-project/releases"

// apeldTool is one program the loader build needs, and where to get it.
type apeldTool struct {
	name    string
	install string
}

var apeldTools = []apeldTool{
	{"zig", "zig " + apeldZigVersion + " from https://ziglang.org/download/"},
	{"ld64.lld", apeldLLVMInstall},
	{"llvm-strip", apeldLLVMInstall},
}

// apeldFlags go to every compile of a loader.
var apeldFlags = []string{"-Os", "-fno-stack-protector", "-fno-unwind-tables", "-fno-asynchronous-unwind-tables", "-Wall"}

// apeldDir is where the loader sources live in the fork checkout, and bin/ under it is where cmd/link's go:embed reads the loaders.
const apeldDir = "src/cmd/link/internal/ld/apeld"

// apeldLookPath finds a loader tool on PATH.
var apeldLookPath = exec.LookPath

// buildForkApeLoaders compiles the APE loaders cmd/link embeds, as the fork's
// cmd/dist (src/cmd/dist/apeld.go) does before it builds cmd/link. The tree
// does not track them, so a checkout that make.bash never ran in has none.
func buildForkApeLoaders(r runner.CommandRunner, goroot string) error {
	paths := map[string]string{}
	var missing []string
	for _, tool := range apeldTools {
		found, err := apeldLookPath(tool.name)
		if err != nil {
			missing = append(missing, "\t"+tool.name+": "+tool.install)
			continue
		}
		paths[tool.name] = found
	}
	if len(missing) > 0 {
		return fmt.Errorf("the fork's APE loaders cannot be built. These tools are not on PATH:\n%s", strings.Join(missing, "\n"))
	}
	zig, lld, strip := paths["zig"], paths["ld64.lld"], paths["llvm-strip"]

	ver, err := apeldOutput(r, "", zig, "version")
	if err != nil {
		return err
	}
	if v := strings.TrimSpace(ver); v != apeldZigVersion {
		return fmt.Errorf("the fork's APE loaders need zig %s, and %s is %s. Install it from https://ziglang.org/download/", apeldZigVersion, zig, v)
	}
	ver, err = apeldOutput(r, "", lld, "--version")
	if err != nil {
		return err
	}
	if lldVersion(ver) != apeldLLDVersion {
		return fmt.Errorf("the fork's APE loaders need LLD %s, and %s says %s. Install %s", apeldLLDVersion, lld, strings.TrimSpace(ver), apeldLLVMInstall)
	}

	dir := filepath.Join(goroot, filepath.FromSlash(apeldDir))
	if err := os.MkdirAll(filepath.Join(dir, "bin"), 0o755); err != nil {
		return err
	}

	// The linker script packs each Linux loader into one PT_LOAD, and the strip drops the section headers a static executable does not need.
	for _, target := range []struct{ triple, arch string }{{"x86_64-linux-musl", "amd64"}, {"aarch64-linux-musl", "arm64"}} {
		out := "bin/apeld-linux-" + target.arch
		args := []string{"cc", "-target", target.triple}
		args = append(args, apeldFlags...)
		args = append(args, "-nostdlib", "-ffreestanding", "-static", "-fno-pic", "-fno-pie",
			"-Wl,--gc-sections", "-Wl,--build-id=none", "-Wl,-z,norelro", "-Wl,-T,linux/apeld.ld", "-s",
			"-o", out, "linux/apeld.c")
		if _, err := apeldOutput(r, dir, zig, args...); err != nil {
			return err
		}
		if _, err := apeldOutput(r, dir, strip, "--strip-sections", out); err != nil {
			return err
		}
	}

	// zig compiles the darwin object and ld64.lld links it: zig's own Mach-O linker cannot merge __DATA_CONST into __DATA.
	env, err := apeldOutput(r, "", zig, "env")
	if err != nil {
		return err
	}
	libDir, err := parseZigLibDir(env)
	if err != nil {
		return err
	}
	if st, err := os.Stat(filepath.Join(libDir, "libc", "darwin")); err != nil || !st.IsDir() {
		return fmt.Errorf("zig's darwin libc stubs are not in %s", filepath.Join(libDir, "libc", "darwin"))
	}
	tmp, err := os.MkdirTemp("", "apeld-darwin")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	obj := filepath.Join(tmp, "apeld-darwin.o")
	// zig's own headers alone, so a Mac host's SDK cannot change the loader.
	args := []string{"cc", "-target", "aarch64-macos"}
	args = append(args, apeldFlags...)
	args = append(args, "-nostdinc", "-isystem", filepath.Join(libDir, "include"), "-isystem", filepath.Join(libDir, "libc", "include", "any-darwin-any"),
		"-c", "-o", obj, "darwin/apeld.c")
	if _, err := apeldOutput(r, dir, zig, args...); err != nil {
		return err
	}
	// The object goes before -lSystem: LLD lists imports in the order it first sees them, and a .tbd's order differs by host.
	_, err = apeldOutput(r, dir, lld, "-arch", "arm64", "-platform_version", "macos", "12.0", "12.0",
		"-o", "bin/apeld-darwin-arm64", obj,
		"-Z", "-L"+filepath.Join(libDir, "libc", "darwin"), "-lSystem",
		"-dead_strip", "-S", "-x", "-no_uuid", "-no_function_starts", "-no_data_const", "-fixup_chains")
	return err
}

// apeldOutput runs one loader-build command in dir and answers its stdout,
// with its stderr in the error when it fails.
func apeldOutput(r runner.CommandRunner, dir, name string, args ...string) (string, error) {
	proc, err := runner.Cmd(name, args...).WithDir(dir).WithQuiet().Run(r)
	if err != nil {
		return "", fmt.Errorf("building the fork's APE loaders: %s: %w", name, err)
	}
	out, _ := io.ReadAll(proc.Stdout())
	stderr, _ := io.ReadAll(proc.Stderr())
	if err := proc.Wait(); err != nil {
		return "", fmt.Errorf("building the fork's APE loaders: %s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(stderr)))
	}
	return string(out), nil
}

// lldVersion returns the word after "LLD" in the output of ld64.lld --version,
// such as "18.1.8" from "Homebrew LLD 18.1.8 (compatible with GNU linkers)".
func lldVersion(out string) string {
	words := strings.Fields(out)
	for idx := 0; idx+1 < len(words); idx++ {
		if words[idx] == "LLD" {
			return words[idx+1]
		}
	}
	return ""
}

// parseZigLibDir reads .lib_dir out of `zig env`.
func parseZigLibDir(env string) (string, error) {
	for _, line := range strings.Split(env, "\n") {
		value, ok := strings.CutPrefix(strings.TrimSpace(line), ".lib_dir = ")
		if !ok {
			continue
		}
		dir, err := strconv.Unquote(strings.TrimSuffix(value, ","))
		if err != nil {
			return "", fmt.Errorf("cannot read lib_dir in zig env: %q: %w", line, err)
		}
		return dir, nil
	}
	return "", fmt.Errorf("zig env names no lib_dir:\n%s", env)
}
