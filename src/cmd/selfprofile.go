package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"runtime/trace"
	"strings"

	"github.com/spf13/cobra"
)

const selfProfileDir = "/tmp/go-toolchain-profile"

var selfProfileSpecs []string

// registerSelfProfileFlags adds the --profile persistent flag to rootCmd.
func registerSelfProfileFlags() {
	rootCmd.PersistentFlags().StringSliceVar(&selfProfileSpecs, "profile", nil,
		"Self-profile go-toolchain (trace, cpu:path, mem:path, trace:path)")
}

// validProfileTypes lists the recognized profile type names.
var validProfileTypes = map[string]bool{
	"cpu":   true,
	"mem":   true,
	"trace": true,
}

// defaultProfilePaths returns the default output path for each profile type.
func defaultProfilePaths() map[string]string {
	return map[string]string{
		"cpu":   filepath.Join(selfProfileDir, "cpu.pprof"),
		"mem":   filepath.Join(selfProfileDir, "mem.pprof"),
		"trace": filepath.Join(selfProfileDir, "trace.out"),
	}
}

// parseProfileSpecs parses specs like ["trace", "cpu:custom.pprof"] into a
// type→path map. Unknown types return an error.
func parseProfileSpecs(specs []string) (map[string]string, error) {
	result := make(map[string]string)
	defaults := defaultProfilePaths()
	for _, spec := range specs {
		typ, path, _ := strings.Cut(spec, ":")
		typ = strings.TrimSpace(typ)
		path = strings.TrimSpace(path)
		if !validProfileTypes[typ] {
			return nil, fmt.Errorf("unknown profile type %q (use: cpu, mem, trace)", typ)
		}
		if path == "" {
			path = defaults[typ]
		}
		result[typ] = path
	}
	return result, nil
}

// flagValues extracts all values for a --name flag from args, before Cobra
// parses. Supports --name=a,b and --name a --name b forms.
func flagValues(args []string, name string) []string {
	var vals []string
	prefix := "--" + name + "="
	flag := "--" + name
	for i := 0; i < len(args); i++ {
		if args[i] == "--" {
			break
		}
		if strings.HasPrefix(args[i], prefix) {
			raw := args[i][len(prefix):]
			vals = append(vals, strings.Split(raw, ",")...)
			continue
		}
		if args[i] == flag && i+1 < len(args) {
			i++
			vals = append(vals, strings.Split(args[i], ",")...)
		}
	}
	return vals
}

// startSelfProfile begins always-on CPU+mem profiling, plus opt-in trace.
// It returns a stop function that must be deferred to flush profiles.
func startSelfProfile() (func(), error) {
	// Parse --profile from os.Args before Cobra runs.
	specs := flagValues(os.Args[1:], "profile")
	overrides, err := parseProfileSpecs(specs)
	if err != nil {
		return nil, err
	}

	// Determine paths: always-on CPU+mem, opt-in trace.
	defaults := defaultProfilePaths()
	cpuPath := defaults["cpu"]
	memPath := defaults["mem"]
	if p, ok := overrides["cpu"]; ok {
		cpuPath = p
	}
	if p, ok := overrides["mem"]; ok {
		memPath = p
	}
	tracePath := ""
	if p, ok := overrides["trace"]; ok {
		tracePath = p
	}

	// Ensure profile directory exists for default paths.
	if err := os.MkdirAll(selfProfileDir, 0o755); err != nil {
		return nil, fmt.Errorf("self-profile: mkdir %s: %w", selfProfileDir, err)
	}
	// Also ensure parent dirs for custom paths.
	for _, p := range []string{cpuPath, memPath, tracePath} {
		if p == "" {
			continue
		}
		dir := filepath.Dir(p)
		if dir != "." && dir != selfProfileDir {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return nil, fmt.Errorf("self-profile: mkdir %s: %w", dir, err)
			}
		}
	}

	var closers []func()

	// Start CPU profiling.
	cpuFile, err := os.Create(cpuPath)
	if err != nil {
		return nil, fmt.Errorf("self-profile: create %s: %w", cpuPath, err)
	}
	if err := pprof.StartCPUProfile(cpuFile); err != nil {
		cpuFile.Close()
		return nil, fmt.Errorf("self-profile: start cpu: %w", err)
	}
	closers = append(closers, func() {
		pprof.StopCPUProfile()
		cpuFile.Close()
	})

	// Start execution trace if requested.
	if tracePath != "" {
		traceFile, err := os.Create(tracePath)
		if err != nil {
			return nil, fmt.Errorf("self-profile: create %s: %w", tracePath, err)
		}
		if err := trace.Start(traceFile); err != nil {
			traceFile.Close()
			return nil, fmt.Errorf("self-profile: start trace: %w", err)
		}
		closers = append(closers, func() {
			trace.Stop()
			traceFile.Close()
		})
	}

	// Memory profile is a point-in-time snapshot written at exit.
	closers = append(closers, func() {
		runtime.GC()
		f, err := os.Create(memPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "self-profile: %v\n", err)
			return
		}
		defer f.Close()
		if err := pprof.WriteHeapProfile(f); err != nil {
			fmt.Fprintf(os.Stderr, "self-profile: %v\n", err)
		}
	})

	// Build the summary that will be printed at exit.
	paths := []struct{ typ, path string }{
		{"cpu", cpuPath},
		{"mem", memPath},
	}
	if tracePath != "" {
		paths = append(paths, struct{ typ, path string }{"trace", tracePath})
	}

	stop := func() {
		// Run closers in reverse (LIFO).
		for i := len(closers) - 1; i >= 0; i-- {
			closers[i]()
		}
		// Print summary.
		fmt.Fprintf(os.Stderr, "\nSelf-profile:\n")
		for _, p := range paths {
			fmt.Fprintf(os.Stderr, "  %s: %s\n", p.typ, p.path)
		}
		fmt.Fprintf(os.Stderr, "  View: go-toolchain profile open\n")
	}

	return stop, nil
}

// profileOpenCmd opens a profile file with the appropriate Go tool.
var profileOpenCmd = &cobra.Command{
	Use:   "open [file]",
	Short: "Open a profile in the browser",
	Long: `Open a pprof or trace file with the appropriate Go analysis tool.

If no file is given, opens the most recent CPU profile from the default
self-profile directory (/tmp/go-toolchain-profile/).

Examples:
  go-toolchain profile open                                    # Open latest CPU profile
  go-toolchain profile open /tmp/go-toolchain-profile/cpu.pprof
  go-toolchain profile open /tmp/go-toolchain-profile/trace.out`,
	SilenceUsage: true,
	RunE:         runProfileOpen,
}

func runProfileOpen(cmd *cobra.Command, args []string) error {
	file := ""
	if len(args) > 0 {
		file = args[0]
	}

	if file == "" {
		// Default to most recent profile in the profile dir.
		file = filepath.Join(selfProfileDir, "cpu.pprof")
	}

	if _, err := os.Stat(file); err != nil {
		return fmt.Errorf("profile not found: %s\n\nRun go-toolchain first to generate a profile", file)
	}

	// Detect file type by extension.
	if isTraceFile(file) {
		fmt.Printf("==> Opening trace: %s\n", file)
		return openTrace(file)
	}

	fmt.Printf("==> Opening pprof: %s\n", file)
	return openPprof(file)
}

// isTraceFile returns true if the file looks like an execution trace.
func isTraceFile(path string) bool {
	ext := filepath.Ext(path)
	base := filepath.Base(path)
	return ext == ".out" || strings.Contains(base, "trace")
}

func openPprof(file string) error {
	return runExternalTool("go", "tool", "pprof", "-http=:", file)
}

func openTrace(file string) error {
	return runExternalTool("go", "tool", "trace", file)
}

func runExternalTool(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}
