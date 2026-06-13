package memlimit

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestGuardDetection compiles the embedded guard source together with a small
// harness and runs it against fixture cgroup filesystems, asserting the exact
// limit the shipped code detects. This exercises the real bytes that go into
// every built binary, not a reimplementation.
func TestGuardDetection(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not on PATH; cannot compile the guard")
	}
	bin := buildGuard(t)

	const (
		mb100 = 104857600
		mb50  = 52428800
		mb200 = 209715200
	)

	v2Mount := "30 23 0:26 / /sys/fs/cgroup rw,nosuid,nodev,noexec,relatime - cgroup2 cgroup2 rw\n"
	v1Mount := "35 30 0:32 / /sys/fs/cgroup/memory rw,nosuid - cgroup cgroup rw,memory\n"

	cases := []struct {
		name      string
		files     map[string]string
		wantLimit uint64
		wantOK    bool
	}{
		{
			name: "cgroup_v2",
			files: map[string]string{
				"proc/self/mountinfo":              v2Mount,
				"proc/self/cgroup":                 "0::/mygroup\n",
				"sys/fs/cgroup/mygroup/memory.max": "104857600\n",
				"sys/fs/cgroup/memory.max":         "max\n",
			},
			wantLimit: mb100,
			wantOK:    true,
		},
		{
			name: "cgroup_v2_parent_is_tighter",
			files: map[string]string{
				"proc/self/mountinfo":              v2Mount,
				"proc/self/cgroup":                 "0::/mygroup\n",
				"sys/fs/cgroup/mygroup/memory.max": "104857600\n",
				"sys/fs/cgroup/memory.max":         "52428800\n",
			},
			wantLimit: mb50,
			wantOK:    true,
		},
		{
			name: "cgroup_v2_unlimited",
			files: map[string]string{
				"proc/self/mountinfo":              v2Mount,
				"proc/self/cgroup":                 "0::/mygroup\n",
				"sys/fs/cgroup/mygroup/memory.max": "max\n",
				"sys/fs/cgroup/memory.max":         "max\n",
			},
			wantLimit: 0,
			wantOK:    false,
		},
		{
			name: "cgroup_v1",
			files: map[string]string{
				"proc/self/mountinfo": v1Mount,
				"proc/self/cgroup":    "5:memory:/mygroup\n",
				"sys/fs/cgroup/memory/mygroup/memory.limit_in_bytes": "209715200\n",
			},
			wantLimit: mb200,
			wantOK:    true,
		},
		{
			name: "cgroup_v1_hierarchical_is_tighter",
			files: map[string]string{
				"proc/self/mountinfo": v1Mount,
				"proc/self/cgroup":    "5:memory:/mygroup\n",
				"sys/fs/cgroup/memory/mygroup/memory.limit_in_bytes": "209715200\n",
				"sys/fs/cgroup/memory/mygroup/memory.stat":           "cache 0\nhierarchical_memory_limit 104857600\nrss 0\n",
			},
			wantLimit: mb100,
			wantOK:    true,
		},
		{
			name: "cgroup_v1_unlimited",
			files: map[string]string{
				"proc/self/mountinfo": v1Mount,
				"proc/self/cgroup":    "5:memory:/mygroup\n",
				"sys/fs/cgroup/memory/mygroup/memory.limit_in_bytes": "9223372036854775807\n",
			},
			wantLimit: 0,
			wantOK:    false,
		},
		{
			name:      "no_cgroup",
			files:     map[string]string{},
			wantLimit: 0,
			wantOK:    false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			for rel, content := range c.files {
				writeFile(t, root, filepath.FromSlash(rel), content)
			}

			limit, ok := runGuard(t, bin, root)
			if limit != c.wantLimit || ok != c.wantOK {
				t.Fatalf("detected (%d, %t), want (%d, %t)", limit, ok, c.wantLimit, c.wantOK)
			}
		})
	}
}

// buildGuard compiles the embedded guard source plus a harness into a binary
// that prints "<limit> <ok>" for the cgroup root given as its first argument.
func buildGuard(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	harness := `package main

import (
	"fmt"
	"os"
)

func main() {
	limit, ok := gtmlCgroupMemoryLimit(os.Args[1])
	fmt.Printf("%d %t\n", limit, ok)
}
`
	writeFile(t, dir, "go.mod", "module guardprobe\n\ngo 1.19\n")
	writeFile(t, dir, GuardFileName, guardSource)
	writeFile(t, dir, "harness.go", harness)

	bin := filepath.Join(dir, "guardprobe")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Dir = dir
	// Neutralize inherited build settings (e.g. -mod=vendor) and avoid any
	// toolchain download for the temp module.
	cmd.Env = append(os.Environ(), "GOFLAGS=", "GOTOOLCHAIN=local")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building guard probe: %v\n%s", err, out)
	}
	return bin
}

func runGuard(t *testing.T, bin, root string) (uint64, bool) {
	t.Helper()
	out, err := exec.Command(bin, root).Output()
	if err != nil {
		t.Fatalf("running guard probe: %v", err)
	}
	var limit uint64
	var ok bool
	if _, err := fmt.Sscanf(strings.TrimSpace(string(out)), "%d %t", &limit, &ok); err != nil {
		t.Fatalf("parsing probe output %q: %v", out, err)
	}
	return limit, ok
}
