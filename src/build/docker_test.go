package build

import (
	"runtime"
	"testing"

	"github.com/wow-look-at-my/testify/assert"
)

func TestInDockerReturnsFalseOutsideDocker(t *testing.T) {
	old := inDockerCheck
	defer func() { inDockerCheck = old }()

	inDockerCheck = func() bool { return false }
	assert.False(t, InDocker())
}

func TestInDockerReturnsTrueInsideDocker(t *testing.T) {
	old := inDockerCheck
	defer func() { inDockerCheck = old }()

	inDockerCheck = func() bool { return true }
	assert.True(t, InDocker())
}

func TestPlatformBinaryName(t *testing.T) {
	got := PlatformBinaryName("myapp")
	want := "myapp_" + runtime.GOOS + "_" + runtime.GOARCH
	assert.Equal(t, want, got)
}
