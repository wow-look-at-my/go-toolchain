package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/wow-look-at-my/go-toolchain/src/hostos"
	"github.com/wow-look-at-my/go-toolchain/src/logger"
	"github.com/wow-look-at-my/go-toolchain/src/runner"
)

// generatorPackages names the program each dependency directive invokes and the
// version that builds it. Every host installs the same version, so they all
// complete a module to the same bytes and share a cache entry.
var generatorPackages = map[string]string{
	"stringer":      "golang.org/x/tools/cmd/stringer@v0.49.0",
	"goyacc":        "golang.org/x/tools/cmd/goyacc@v0.49.0",
	"gotext":        "golang.org/x/text/cmd/gotext@v0.42.0",
	"esc":           "github.com/mjibson/esc@v0.2.0",
	"protoc-gen-go": "google.golang.org/protobuf/cmd/protoc-gen-go@v1.34.1",
}

// directiveTool names the program a directive invokes, or "" when the go
// command runs it and there is nothing separate to install.
func directiveTool(d generateDirective) string {
	args, err := splitGenerateCommand(d.Command)
	if err != nil || len(args) == 0 || args[0] == "go" {
		return ""
	}
	return args[0]
}

// installGenerators builds every generator the pending directives name and this
// machine does not already have. A dependency ships the directive and commits
// the result, so a consumer that never installed the generator still has to run
// it to get the same bytes.
func installGenerators(r runner.CommandRunner, pending []generateDirective) error {
	want := map[string]string{}
	for _, d := range pending {
		tool := directiveTool(d)
		if tool == "" {
			continue
		}
		if _, err := exec.LookPath(tool); err == nil {
			continue
		}
		pkg, known := generatorPackages[tool]
		if !known {
			return fmt.Errorf("%s:%d needs the generator %q, which is not on PATH and has no pinned package: add it to generatorPackages (%s)",
				d.Label, d.Line, tool, d.Command)
		}
		want[tool] = pkg
	}
	if len(want) == 0 {
		return nil
	}
	binDir, err := generatorBinDir()
	if err != nil {
		return err
	}
	st := logStep("go install (dependency generators)")
	defer st.done()
	for tool, pkg := range want {
		st.noteOutput()
		logger.Info("\t%s from %s", tool, pkg)
		// Building a generator must not complete the module that provides it:
		// that fetch wants the generator this call is making.
		proc, err := runner.Cmd("go", "install", pkg).WithQuiet().WithEnv("GOGENERATEDEPS", "off").Run(r)
		if err != nil {
			return fmt.Errorf("installing the generator %s: %w", pkg, err)
		}
		if err := proc.Wait(); err != nil {
			return fmt.Errorf("installing the generator %s: %w", pkg, err)
		}
	}
	os.Setenv("PATH", pathWithFirst(binDir, os.Getenv("PATH"), hostos.GOOS()))
	return nil
}

// generatorBinDir answers where go install leaves a generator. This go command
// is an APE targeting cosmo, and cmd/go reads a cosmo build as a cross-compile
// because the host it RUNS on is not cosmo, so the program lands in a
// per-target subdirectory of GOPATH/bin rather than in GOBIN.
func generatorBinDir() (string, error) {
	read := func(name string) (string, error) {
		out, err := goOutput("env", name)
		return strings.TrimSpace(out), err
	}
	gopath, err := read("GOPATH")
	if err != nil {
		return "", fmt.Errorf("reading GOPATH for the generator install: %w", err)
	}
	goos, err := read("GOOS")
	if err != nil {
		return "", fmt.Errorf("reading GOOS for the generator install: %w", err)
	}
	goarch, err := read("GOARCH")
	if err != nil {
		return "", fmt.Errorf("reading GOARCH for the generator install: %w", err)
	}
	bin := strings.TrimRight(slashPath(gopath), "/") + "/bin"
	if goos != hostos.GOOS() {
		return bin + "/" + goos + "_" + goarch, nil
	}
	return bin, nil
}
