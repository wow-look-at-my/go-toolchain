# What the published APE must do on an NT host. Each host has its own file
# because dats runs every test in the file it is given and has no filtering: a
# linux assertion in here would fail this leg by construction.
#
# The APE is copied under an .exe name because that is how an NT host runs it.
#
# The leg runs it with --no-sandbox, for the reason smoke-linux.dats gives.

shared:
	copy:
		gt-ape.exe: ../../dist/go-toolchain

tests:
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
	# A refusal instead would mean the guard gained sight and this leg should
	# assert the refusal the way the other hosts do.
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
