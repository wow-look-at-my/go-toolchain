# The linux smoke suite. These assertions used to be bash inside the smoke-linux
# job (.github/workflows/ci.yml); a workflow step schedules work, it is not a
# test harness, so they live here where an engineer can run them.
#
# The job runs this file with --no-sandbox, and that is not a shortcut: the
# pipeline test below drives go-toolchain, whose own dats phase sandboxes the
# agent-output-guard fixture it stages. Nesting bwrap inside bwrap is what the
# opt-out avoids, and the guard's own isolation is unaffected.
#
# ../../dist/go-toolchain is the published fat APE the build job handed off, and
# ../../harness/socketharness-linux-amd64 the guard fixture's harness.

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
