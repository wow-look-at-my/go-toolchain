#!/usr/bin/env bats

setup() {
	# Create a temp directory for test projects
	TEST_DIR="$(mktemp -d)"
	export TEST_DIR

	# Path to the built binary
	BINARY="$BATS_TEST_DIRNAME/../go-safe-build"
	export BINARY
}

teardown() {
	rm -rf "$TEST_DIR"
}

# Helper to create a minimal Go project
create_test_project() {
	local dir="$1"
	local coverage="${2:-0}"

	mkdir -p "$dir"
	cd "$dir"

	cat > go.mod <<EOF
module testproject
go 1.21
EOF

	if [ "$coverage" -eq 100 ]; then
		# Fully covered code
		cat > main.go <<EOF
package main

func Add(a, b int) int {
	return a + b
}

func main() {}
EOF
		cat > main_test.go <<EOF
package main

import "testing"

func TestAdd(t *testing.T) {
	if Add(1, 2) != 3 {
		t.Error("Add failed")
	}
}
EOF
	elif [ "$coverage" -eq 50 ]; then
		# Partially covered code
		cat > main.go <<EOF
package main

func Add(a, b int) int {
	return a + b
}

func Sub(a, b int) int {
	return a - b
}

func main() {}
EOF
		cat > main_test.go <<EOF
package main

import "testing"

func TestAdd(t *testing.T) {
	if Add(1, 2) != 3 {
		t.Error("Add failed")
	}
}
EOF
	else
		# No tests = 0% coverage
		cat > main.go <<EOF
package main

func Add(a, b int) int {
	return a + b
}

func main() {}
EOF
	fi
}

@test "shows help with --help" {
	run "$BINARY" --help
	[ "$status" -eq 0 ]
	[[ "$output" == *"Build Go projects with coverage enforcement"* ]]
	[[ "$output" == *"--min-coverage"* ]]
	[[ "$output" == *"--cov-detail"* ]]
}

@test "--min-coverage 0 runs tests only, no build, exits non-zero" {
	create_test_project "$TEST_DIR/proj" 100
	cd "$TEST_DIR/proj"

	run "$BINARY" --min-coverage 0
	[ "$status" -ne 0 ]
	[[ "$output" == *"Test-only mode"* ]]
	[[ "$output" == *"skipping build"* ]]
	# Should not have created a binary
	[ ! -f "proj" ]
}

@test "fails when coverage below threshold" {
	create_test_project "$TEST_DIR/proj" 50
	cd "$TEST_DIR/proj"

	run "$BINARY" --min-coverage 90
	[ "$status" -ne 0 ]
	[[ "$output" == *"below minimum"* ]]
}

@test "succeeds when coverage meets threshold" {
	create_test_project "$TEST_DIR/proj" 100
	cd "$TEST_DIR/proj"

	run "$BINARY" --min-coverage 80
	[ "$status" -eq 0 ]
	[[ "$output" == *"Build successful"* ]]
	# Should have created a binary
	[ -f "proj" ]
}

@test "--cov-detail=func shows function coverage" {
	create_test_project "$TEST_DIR/proj" 100
	cd "$TEST_DIR/proj"

	run "$BINARY" --min-coverage 80 --cov-detail func
	[ "$status" -eq 0 ]
	[[ "$output" == *"Function coverage"* ]]
}

@test "--cov-detail=file shows file coverage" {
	create_test_project "$TEST_DIR/proj" 100
	cd "$TEST_DIR/proj"

	run "$BINARY" --min-coverage 80 --cov-detail file
	[ "$status" -eq 0 ]
	[[ "$output" == *"File coverage"* ]]
}

@test "--json outputs valid JSON" {
	create_test_project "$TEST_DIR/proj" 100
	cd "$TEST_DIR/proj"

	run "$BINARY" --min-coverage 80 --json
	[ "$status" -eq 0 ]
	# Verify it's valid JSON with total field
	echo "$output" | jq -e '.total' > /dev/null
}

@test "-o flag sets output binary name" {
	create_test_project "$TEST_DIR/proj" 100
	cd "$TEST_DIR/proj"

	run "$BINARY" --min-coverage 80 -o myapp
	[ "$status" -eq 0 ]
	[ -f "myapp" ]
}

@test "--src builds from cmd/ layout" {
	mkdir -p "$TEST_DIR/proj/cmd/myapp"
	cd "$TEST_DIR/proj"

	cat > go.mod <<EOF
module testproject
go 1.21
EOF

	cat > cmd/myapp/main.go <<EOF
package main

func Add(a, b int) int {
	return a + b
}

func main() {}
EOF

	cat > cmd/myapp/main_test.go <<EOF
package main

import "testing"

func TestAdd(t *testing.T) {
	if Add(1, 2) != 3 {
		t.Error("Add failed")
	}
}
EOF

	run "$BINARY" --min-coverage 80 -o custombin --src ./cmd/myapp
	[ "$status" -eq 0 ]
	[ -f "custombin" ]
}

@test "auto-detects cmd/ layout without --src" {
	mkdir -p "$TEST_DIR/proj/cmd/myapp"
	cd "$TEST_DIR/proj"

	cat > go.mod <<EOF
module testproject
go 1.21
EOF

	cat > cmd/myapp/main.go <<EOF
package main

func Add(a, b int) int {
	return a + b
}

func main() {}
EOF

	cat > cmd/myapp/main_test.go <<EOF
package main

import "testing"

func TestAdd(t *testing.T) {
	if Add(1, 2) != 3 {
		t.Error("Add failed")
	}
}
EOF

	run "$BINARY" --min-coverage 80
	[ "$status" -eq 0 ]
	[[ "$output" == *"Build successful"* ]]
	[ -f "myapp" ]
}

@test "auto-detects main packages in non-cmd directories" {
	mkdir -p "$TEST_DIR/proj/tools/linter"
	cd "$TEST_DIR/proj"

	cat > go.mod <<EOF
module testproject
go 1.21
EOF

	cat > tools/linter/main.go <<EOF
package main

func Check(s string) bool {
	return len(s) > 0
}

func main() {}
EOF

	cat > tools/linter/main_test.go <<EOF
package main

import "testing"

func TestCheck(t *testing.T) {
	if !Check("hello") {
		t.Error("Check failed")
	}
}
EOF

	run "$BINARY" --min-coverage 80
	[ "$status" -eq 0 ]
	[[ "$output" == *"Build successful"* ]]
	[ -f "linter" ]
}

@test "auto-detects cmd/ layout with -o override" {
	mkdir -p "$TEST_DIR/proj/cmd/myapp"
	cd "$TEST_DIR/proj"

	cat > go.mod <<EOF
module testproject
go 1.21
EOF

	cat > cmd/myapp/main.go <<EOF
package main

func Add(a, b int) int {
	return a + b
}

func main() {}
EOF

	cat > cmd/myapp/main_test.go <<EOF
package main

import "testing"

func TestAdd(t *testing.T) {
	if Add(1, 2) != 3 {
		t.Error("Add failed")
	}
}
EOF

	run "$BINARY" --min-coverage 80 -o customname
	[ "$status" -eq 0 ]
	[ -f "customname" ]
}

@test "shows package coverage in output" {
	create_test_project "$TEST_DIR/proj" 100
	cd "$TEST_DIR/proj"

	run "$BINARY" --min-coverage 80
	[ "$status" -eq 0 ]
	[[ "$output" == *"Package coverage"* ]]
	[[ "$output" == *"testproject"* ]]
}

@test "reports total coverage percentage" {
	create_test_project "$TEST_DIR/proj" 100
	cd "$TEST_DIR/proj"

	run "$BINARY" --min-coverage 0
	[[ "$output" == *"Total coverage:"* ]]
	[[ "$output" == *"%"* ]]
}

@test "bootstrap: can build itself 3x and produce identical binaries" {
	# Copy source to temp dir
	cp -r "$BATS_TEST_DIRNAME/.." "$TEST_DIR/go-safe-build"
	cd "$TEST_DIR/go-safe-build"

	# Clean any existing binaries
	rm -f go-safe-build go-safe-build1 go-safe-build2

	# Stage 1: Original compile with go build
	go build -o go-safe-build ./src

	# Stage 2: Use go-safe-build to build itself
	./go-safe-build -o go-safe-build1

	# Stage 3: Use go-safe-build1 to build itself
	./go-safe-build1 -o go-safe-build2

	# Verify both stage 2 and 3 binaries exist
	[ -f "go-safe-build1" ]
	[ -f "go-safe-build2" ]

	# Compare binaries - they should be identical
	cmp go-safe-build1 go-safe-build2
}
