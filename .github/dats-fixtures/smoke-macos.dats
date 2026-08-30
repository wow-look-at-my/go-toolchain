# The macOS smoke suite. These assertions used to be bash inside the smoke-macos
# job (.github/workflows/ci.yml); a workflow step schedules work, it is not a
# test harness, so they live here where an engineer can run them.
#
# The job runs this file with --no-sandbox, and that is not a shortcut: the
# pipeline test below drives go-toolchain, whose own dats phase runs the
# agent-output-guard fixture it stages under seatbelt. The socket cases at the
# bottom are the same two the fixture makes, deliberately OUTSIDE any sandbox --
# the shape a real agent invocation has on a developer's own mac.
#
# Every copy comes from the pristine handed-off artifact rather than from a
# binary an earlier test ran: an APE rewrites its own file on first exec, so a
# copy of one that has run is no longer what a mac user downloads.

shared:
	copy:
		gt-ape: ../../dist/go-toolchain

setup:
	- chmod +x {shared.gt-ape}

tests:
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
