package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/wow-look-at-my/testify/assert"
)

func TestParseProfileSpecs(t *testing.T) {
	tests := []struct {
		name    string
		specs   []string
		want    map[string]string
		wantErr string
	}{
		{
			name:  "bare trace",
			specs: []string{"trace"},
			want:  map[string]string{"trace": filepath.Join(selfProfileDir, "trace.out")},
		},
		{
			name:  "trace with custom path",
			specs: []string{"trace:custom/trace.out"},
			want:  map[string]string{"trace": "custom/trace.out"},
		},
		{
			name:  "cpu override",
			specs: []string{"cpu:my_cpu.pprof"},
			want:  map[string]string{"cpu": "my_cpu.pprof"},
		},
		{
			name:  "mem override",
			specs: []string{"mem:/tmp/custom_mem.pprof"},
			want:  map[string]string{"mem": "/tmp/custom_mem.pprof"},
		},
		{
			name:  "multiple specs",
			specs: []string{"trace", "cpu:custom.pprof"},
			want: map[string]string{
				"trace": filepath.Join(selfProfileDir, "trace.out"),
				"cpu":   "custom.pprof",
			},
		},
		{
			name:    "unknown type",
			specs:   []string{"invalid"},
			wantErr: "unknown profile type",
		},
		{
			name:  "empty specs",
			specs: nil,
			want:  map[string]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseProfileSpecs(tt.specs)
			if tt.wantErr != "" {
				assert.NotNil(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			assert.Nil(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestFlagValues(t *testing.T) {
	tests := []struct {
		name string
		args []string
		flag string
		want []string
	}{
		{
			name: "equals form",
			args: []string{"--profile=cpu,trace"},
			flag: "profile",
			want: []string{"cpu", "trace"},
		},
		{
			name: "space form",
			args: []string{"--profile", "cpu,trace"},
			flag: "profile",
			want: []string{"cpu", "trace"},
		},
		{
			name: "repeated flags",
			args: []string{"--profile", "cpu", "--profile", "trace"},
			flag: "profile",
			want: []string{"cpu", "trace"},
		},
		{
			name: "mixed with other args",
			args: []string{"matrix", "--profile=trace", "--os", "linux"},
			flag: "profile",
			want: []string{"trace"},
		},
		{
			name: "not present",
			args: []string{"--json", "matrix"},
			flag: "profile",
			want: nil,
		},
		{
			name: "stops at double dash",
			args: []string{"--", "--profile=cpu"},
			flag: "profile",
			want: nil,
		},
		{
			name: "type:path form",
			args: []string{"--profile", "trace:custom.out"},
			flag: "profile",
			want: []string{"trace:custom.out"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := flagValues(tt.args, tt.flag)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestStartSelfProfile_CreatesFiles(t *testing.T) {
	tmpDir := t.TempDir()
	cpuPath := filepath.Join(tmpDir, "cpu.pprof")
	memPath := filepath.Join(tmpDir, "mem.pprof")

	// Override os.Args to specify custom paths.
	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()
	os.Args = []string{"go-toolchain", "--profile", "cpu:" + cpuPath, "--profile", "mem:" + memPath}

	stop, err := startSelfProfile()
	assert.Nil(t, err)
	assert.NotNil(t, stop)

	// Do some work to generate profile data.
	sum := 0
	for i := 0; i < 1000; i++ {
		sum += i
	}
	_ = sum

	stop()

	// CPU profile should exist and be non-empty.
	info, err := os.Stat(cpuPath)
	assert.Nil(t, err)
	assert.Greater(t, info.Size(), int64(0))

	// Mem profile should exist and be non-empty.
	info, err = os.Stat(memPath)
	assert.Nil(t, err)
	assert.Greater(t, info.Size(), int64(0))
}

func TestStartSelfProfile_WithTrace(t *testing.T) {
	tmpDir := t.TempDir()
	cpuPath := filepath.Join(tmpDir, "cpu.pprof")
	memPath := filepath.Join(tmpDir, "mem.pprof")
	tracePath := filepath.Join(tmpDir, "trace.out")

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()
	os.Args = []string{
		"go-toolchain",
		"--profile", "cpu:" + cpuPath,
		"--profile", "mem:" + memPath,
		"--profile", "trace:" + tracePath,
	}

	stop, err := startSelfProfile()
	assert.Nil(t, err)
	assert.NotNil(t, stop)
	stop()

	// Trace file should exist and be non-empty.
	info, err := os.Stat(tracePath)
	assert.Nil(t, err)
	assert.Greater(t, info.Size(), int64(0))
}

func TestStartSelfProfile_InvalidType(t *testing.T) {
	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()
	os.Args = []string{"go-toolchain", "--profile", "invalid"}

	_, err := startSelfProfile()
	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), "unknown profile type")
}

func TestIsTraceFile(t *testing.T) {
	assert.True(t, isTraceFile("trace.out"))
	assert.True(t, isTraceFile("/tmp/go-toolchain-profile/trace.out"))
	assert.True(t, isTraceFile("my_trace.pprof"))
	assert.False(t, isTraceFile("cpu.pprof"))
	assert.False(t, isTraceFile("/tmp/go-toolchain-profile/mem.pprof"))
}
