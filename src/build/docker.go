package build

import (
	"os"
	"runtime"
)

// inDockerCheck is the function used to detect Docker containers.
// It is a variable so tests can override it.
var inDockerCheck = defaultInDockerCheck

func defaultInDockerCheck() bool {
	_, err := os.Stat("/.dockerenv")
	return err == nil
}

// InDocker returns true when the process is running inside a Docker container.
func InDocker() bool {
	return inDockerCheck()
}

// SetInDockerCheck overrides the Docker detection function (for testing).
func SetInDockerCheck(f func() bool) func() {
	old := inDockerCheck
	inDockerCheck = f
	return func() { inDockerCheck = old }
}

// PlatformBinaryName returns the binary name with os_arch suffix appended,
// e.g. "mytool" -> "mytool_linux_amd64".
func PlatformBinaryName(name string) string {
	return name + "_" + runtime.GOOS + "_" + runtime.GOARCH
}
