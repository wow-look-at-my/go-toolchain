# The smoke suite. ONE file, run unchanged by every leg of the smoke job
# (.github/workflows/ci.yml), because one APE is what every host downloads and
# the question is the same on all of them: does the published artifact boot,
# report the host it is actually on, and drive a whole pipeline here.
#
# A host-specific answer is asserted by PAIRING it with what the shell reports,
# so the assertion holds everywhere without the file knowing where it runs: the
# command prints the APE's answer and `uname -s` on one line, and the pattern
# matches only the combinations that agree.
#
# Every leg runs it with --no-sandbox: the pipeline test drives go-toolchain,
# whose own dats phase sandboxes the agent-output-guard fixture it stages, and
# nesting a sandbox inside dats' outer run is what the opt-out avoids.
#
# The APE is copied under an .exe name on every host. NT needs the suffix, a
# posix host does not care, and one name is what keeps this file host-agnostic.

shared:
	copy:
		gt-ape.exe: ../../dist/go-toolchain

setup:
	- chmod +x {shared.gt-ape.exe}

tests:
	- desc: the shipped artifact carries the APE magic
	  cmd: 'head -c 6 {shared.gt-ape.exe}'
	  timeout: 30s
	  outputs:
		stdout:
			- "MZqFpD"

	# An APE is a valid PE, a valid ELF and a valid Mach-O at once, so the
	# payload each host selects has to start here rather than in theory.
	- desc: the APE's payload runs on this host
	  cmd: '{shared.gt-ape.exe} version'
	  timeout: 60s
	  inputs:
		env:
			GO_TOOLCHAIN_BUILDHOST_URL: "http://127.0.0.1:1"
	  outputs:
		stdout:
			- "Version:"

	- desc: the APE prints usage under --help
	  cmd: '{shared.gt-ape.exe} --help'
	  timeout: 60s
	  inputs:
		env:
			GO_TOOLCHAIN_BUILDHOST_URL: "http://127.0.0.1:1"
	  outputs:
		stdout:
			- "Usage:"

	# What the APE detects decides every host-specific choice it makes: the
	# buildhost slot, the fork's bin/go suffix, the guard's classifier. GUESSED
	# means the measurement failed and the fallback answered, which reads
	# identically until something breaks. The pattern accepts only an answer
	# that agrees with the shell's own name for this host.
	- desc: the APE detects this host by measurement, and names the host the shell names
	  cmd: 'printf "%s|%s\n" "$({shared.gt-ape.exe} version host | head -1)" "$(uname -s)"'
	  timeout: 60s
	  inputs:
		env:
			GO_TOOLCHAIN_BUILDHOST_URL: "http://127.0.0.1:1"
	  outputs:
		stdout:
			0: "^host: (linux.*\\|Linux|darwin.*\\|Darwin|windows.*\\|(MINGW|MSYS|CYGWIN))"
		"!stdout":
			- "GUESSED"

	# The whole pipeline, driven by the APE, in a synthetic consumer module:
	# tidy resolves testify, vet type-checks, the test runs, the build writes a
	# binary. The module also carries the agent-output-guard fixture, which
	# go-toolchain's own dats phase then runs sandboxed against the pristine
	# copies staged beside it -- an APE rewrites its own file on first exec, so
	# a copy of the one that ran the pipeline is no longer what a user gets.
	# Both harnesses travel: the fixture picks the one this host can run.
	- desc: the full pipeline runs in a tiny module on this host
	  cmd: 'cd "$(dirname {inputs.go.mod})"; chmod +x ./gt-under-test ./socketharness-linux ./socketharness-darwin; {shared.gt-ape.exe}'
	  timeout: 20m
	  inputs:
		env:
			GO_TOOLCHAIN_CACHING_INTENTIONALLY_NOT_CONFIGURED: "1"
		copy:
			gt-under-test: ../../dist/go-toolchain
			socketharness-linux: ../../harness/socketharness-linux-amd64
			socketharness-darwin: ../../harness/socketharness-darwin-arm64
			dats/agent-output-guard.dats: agent-output-guard.dats
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

	# The guard on the HOST, where the answer differs by host and both answers
	# are correct: a host whose descriptors it can classify refuses a captured
	# run, and a host it cannot see on says so instead of allowing silently.
	# Pairing with uname is what keeps that one test rather than three: an
	# INOPERATIVE banner on Linux, or a refusal that never comes on NT, fails.
	- desc: the agent output guard answers for the host it detects
	  cmd: 'mkdir -p {outputs.rundir}; cd {outputs.rundir}; out=$(env CLAUDECODE=1 {shared.gt-ape.exe} 2>&1); printf "%s|%s\n" "$(uname -s)" "$(printf "%s" "$out" | tr "\n" " ")"'
	  timeout: 5m
	  inputs:
		env:
			GO_TOOLCHAIN_BUILDHOST_URL: "http://127.0.0.1:1"
	  outputs:
		stdout:
			0: "^((Linux|Darwin)\\|.*refused to run|(MINGW|MSYS|CYGWIN).*\\|.*INOPERATIVE on this windows host)"
