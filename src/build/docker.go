package build

import "os"

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

// BinaryName returns name_goos_goarch with .exe appended for Windows.
func BinaryName(name, goos, goarch string) string {
	out := name + "_" + goos + "_" + goarch
	if goos == "windows" {
		out += ".exe"
	}
	return out
}
