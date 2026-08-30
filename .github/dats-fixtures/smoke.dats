# The union smoke suite: every test from smoke-linux.dats, smoke-macos.dats and
# smoke-windows.dats, verbatim, in one file. All three smoke jobs
(.github/workflows/ci.yml) run THIS file, so every platform executes every OS's
# assertions; a test describing another host is expected to fail there -- that is
# the union being executed, not something to adapt away. Assembled as a
# mechanical set union: identical shared/setup lines deduplicated, nothing else
# touched.
#
# All three jobs run it with --no-sandbox: the pipeline test drives go-toolchain,
# whose own dats phase sandboxes the agent-output-guard fixture it stages, and
# nesting one sandbox inside dats' outer run is what the opt-out avoids.
#
# ../../dist/go-toolchain is the published fat APE the build job handed off, and
# ../../harness/socketharness-* the guard fixtures' harnesses.

	copy:
		gt-ape: ../../dist/go-toolchain
	copy:
		gt-ape: ../../dist/go-toolchain
	copy:
		gt-ape.exe: ../../dist/go-toolchain

setup:
	- chmod +x {shared.gt-ape}
	- chmod +x {shared.gt-ape}

tests:
	# One APE runs on every host, so what it detects here decides every
	# host-specific choice: the buildhost slot, the fork's bin/go suffix, the
	# guard's classifier. A GUESSED answer means the measurement failed and the
	# fallback answered, which reads identically until something breaks.
	- desc: the APE detects a linux host by measurement
	  cmd: '{shared.gt-ape} version host'
	  timeout: 60s
	  inputs:
		env:
			GO_TOOLCHAIN_BUILDHOST_URL: "http://127.0.0.1:1"
	  outputs:
		stdout:
			- "host: linux"
		"!stdout":
			- "GUESSED"

	# The whole pipeline, driven by the APE, in a synthetic consumer module:
	# tidy resolves testify, vet type-checks, the test runs, the build writes a
	# binary. The module also carries the agent-output-guard fixture, which
	# go-toolchain's own dats phase then runs sandboxed against the pristine
	# copies staged beside it -- an APE rewrites its own file on first exec, so
	# a copy of the one that ran the pipeline is no longer what a user gets.
	- desc: the full pipeline runs in a tiny module on a linux host
	  cmd: 'cd "$(dirname {inputs.go.mod})"; chmod +x ./gt-under-test ./socketharness-under-test; {shared.gt-ape}'
	  timeout: 20m
	  inputs:
		env:
			GO_TOOLCHAIN_CACHING_INTENTIONALLY_NOT_CONFIGURED: "1"
		copy:
			gt-under-test: ../../dist/go-toolchain
			socketharness-under-test: ../../harness/socketharness-linux-amd64
			dats/agent-output-guard.dats: smoke-linux-agent-output-guard.dats
		files:
			go.mod: |
				module example.com/apesmoke

				go 1.24

				require github.com/stretchr/testify v1.11.1
			main.go: |
				// Package main is a tiny module used to smoke-test the
				// published APE on this runner's OS.
				package main

				import "fmt"

				// Greeting returns the smoke-test greeting.
				func Greeting(name string) string {
					return "hello, " + name
				}

				func main() {
					fmt.Println(Greeting("cosmo"))
				}
			main_test.go: |
				package main

				import (
					"testing"

					"github.com/stretchr/testify/require"
				)

				func TestGreeting(t *testing.T) {
					require.Equal(t, "hello, cosmo", Greeting("cosmo"))
				}
	  outputs:
		stdout:
			- "Build successful"

	# One APE runs on every host, so what it detects here decides every
	# host-specific choice: the buildhost slot, the fork's bin/go suffix, the
	# guard's classifier. A GUESSED answer means the measurement failed and the
	# fallback answered, which reads identically until something breaks.
	- desc: the APE detects a darwin host by measurement
	  cmd: '{shared.gt-ape} version host'
	  timeout: 60s
	  inputs:
		env:
			GO_TOOLCHAIN_BUILDHOST_URL: "http://127.0.0.1:1"
	  outputs:
		stdout:
			- "host: darwin"
		"!stdout":
			- "GUESSED"

	# The whole pipeline, driven by the exact binary ARM64 mac users download:
	# tidy resolves testify, vet type-checks, the test runs, the build writes a
	# binary. macos-latest carries no Go on PATH, so this is also the first
	# real exercise of the cosmo bootstrap on a darwin host. The module carries
	# the agent-output-guard fixture, which go-toolchain's own dats phase runs.
	- desc: the full pipeline runs in a tiny module on a darwin host
	  cmd: 'cd "$(dirname {inputs.go.mod})"; chmod +x ./gt-under-test ./socketharness-under-test; {shared.gt-ape}'
	  timeout: 20m
	  inputs:
		env:
			GO_TOOLCHAIN_CACHING_INTENTIONALLY_NOT_CONFIGURED: "1"
		copy:
			gt-under-test: ../../dist/go-toolchain
			socketharness-under-test: ../../harness/socketharness-darwin-arm64
			dats/agent-output-guard.dats: smoke-macos-agent-output-guard.dats
		files:
			go.mod: |
				module example.com/nativesmoke

				go 1.24

				require github.com/stretchr/testify v1.11.1
			main.go: |
				// Package main is a tiny module used to smoke-test the
				// published APE on a darwin host.
				package main

				import "fmt"

				// Greeting returns the smoke-test greeting.
				func Greeting(name string) string {
					return "hello, " + name
				}

				func main() {
					fmt.Println(Greeting("mac"))
				}
			main_test.go: |
				package main

				import (
					"testing"

					"github.com/stretchr/testify/require"
				)

				func TestGreeting(t *testing.T) {
					require.Equal(t, "hello, mac", Greeting("mac"))
				}
	  outputs:
		stdout:
			- "Build successful"

	# socketharness wires the binary's stdout through a socketpair -- what a
	# Node or Bun child_process actually uses on macOS -- and names itself as
	# the reader. The run directory needs its own go.mod: without one the child
	# never reaches the guard at all.
	- desc: the guard allows a plain run whose socket reader is the agent itself
	  cmd: 'cd "$(dirname {inputs.go.mod})"; chmod +x ./harness ./gt-right; ./harness ./gt-right'
	  timeout: 60s
	  inputs:
		env:
			GO_TOOLCHAIN_BUILDHOST_URL: "http://127.0.0.1:1"
		copy:
			harness: ../../harness/socketharness-darwin-arm64
			gt-right: ../../dist/go-toolchain
		files:
			go.mod: |
				module example.com/sockprobe

				go 1.24
	  outputs:
		stdout:
			- "HARNESS_GUARD_REFUSED=false"

	- desc: the guard still refuses a socket whose reader is not the agent
	  cmd: 'cd "$(dirname {inputs.go.mod})"; chmod +x ./harness ./gt-wrong; ./harness --wrong-reader ./gt-wrong'
	  timeout: 60s
	  inputs:
		env:
			GO_TOOLCHAIN_BUILDHOST_URL: "http://127.0.0.1:1"
		copy:
			harness: ../../harness/socketharness-darwin-arm64
			gt-wrong: ../../dist/go-toolchain
		files:
			go.mod: |
				module example.com/sockprobe

				go 1.24
	  outputs:
		stdout:
			- "HARNESS_GUARD_REFUSED=true"

	- desc: the shipped artifact carries the APE magic
	  cmd: 'head -c 6 {shared.gt-ape.exe}'
	  timeout: 30s
	  outputs:
		stdout:
			- "MZqFpD"

	# An APE is simultaneously a valid PE, and the payload NT selects is a
	# native windows/amd64 build. Running it is the proof that it loads.
	- desc: the APE's PE payload runs on an NT host
	  cmd: '{shared.gt-ape.exe} version'
	  timeout: 60s
	  inputs:
		env:
			GO_TOOLCHAIN_BUILDHOST_URL: "http://127.0.0.1:1"
	  outputs:
		stdout:
			- "Version:"

	- desc: the APE prints usage under --help on an NT host
	  cmd: '{shared.gt-ape.exe} --help'
	  timeout: 60s
	  inputs:
		env:
			GO_TOOLCHAIN_BUILDHOST_URL: "http://127.0.0.1:1"
	  outputs:
		stdout:
			- "Usage:"

	# One APE runs on every host, so what it detects here decides every
	# host-specific choice: the buildhost slot, the fork's bin/go suffix, the
	# guard's classifier. A GUESSED answer means the measurement failed and the
	# fallback answered, which reads identically until something breaks.
	- desc: the APE detects a windows host by measurement
	  cmd: '{shared.gt-ape.exe} version host'
	  timeout: 60s
	  inputs:
		env:
			GO_TOOLCHAIN_BUILDHOST_URL: "http://127.0.0.1:1"
	  outputs:
		stdout:
			- "host: windows"
		"!stdout":
			- "GUESSED"

	# The whole pipeline, driven by the APE, in a synthetic consumer module:
	# tidy resolves testify, vet type-checks, the test runs, the build writes a
	# binary. GO_TOOLCHAIN_CACHING_INTENTIONALLY_NOT_CONFIGURED says this module
	# has no org cache credentials on purpose.
	- desc: the full pipeline runs in a tiny module on an NT host
	  cmd: 'cd "$(dirname {inputs.go.mod})"; {shared.gt-ape.exe}'
	  timeout: 20m
	  inputs:
		env:
			GO_TOOLCHAIN_CACHING_INTENTIONALLY_NOT_CONFIGURED: "1"
		files:
			go.mod: |
				module example.com/winsmoke

				go 1.24

				require github.com/stretchr/testify v1.11.1
			main.go: |
				// Package main is a tiny module used to smoke-test the
				// published APE on an NT host.
				package main

				import "fmt"

				// Greeting returns the smoke-test greeting.
				func Greeting(name string) string {
					return "hello, " + name
				}

				func main() {
					fmt.Println(Greeting("windows"))
				}
			main_test.go: |
				package main

				import (
					"testing"

					"github.com/stretchr/testify/require"
				)

				func TestGreeting(t *testing.T) {
					require.Equal(t, "hello, windows", Greeting("windows"))
				}
	  outputs:
		stdout:
			- "Build successful"

	# Windows is the host the agent output guard cannot classify on, so the
	# no-op is asserted rather than left to chance. The banner is the only thing
	# a human here gets while the guard is blind, and it names the host it
	# detected -- so a wrong host name means hostos.GOOS() is wrong here too.
	# A refusal instead would mean the guard gained sight and this job should
	# assert the refusal the way the other two hosts do.
	- desc: the agent output guard reports itself inoperative on an NT host
	  cmd: 'mkdir -p {outputs.rundir}; cd {outputs.rundir}; env CLAUDECODE=1 {shared.gt-ape.exe}; echo "gt exit: $?"'
	  timeout: 5m
	  inputs:
		env:
			GO_TOOLCHAIN_BUILDHOST_URL: "http://127.0.0.1:1"
	  outputs:
		stderr:
			- "INOPERATIVE on this windows host"
		"!stderr":
			- "refused to run"
