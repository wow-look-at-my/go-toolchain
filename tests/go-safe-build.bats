#!/usr/bin/env bats

setup() {
	# Create a temp directory for test projects
	TEST_DIR="$(mktemp -d)"
	export TEST_DIR

	# Path to the built binary
	BINARY="$BATS_TEST_DIRNAME/../build/go-toolchain"
	export BINARY
}

teardown() {
	rm -rf "$TEST_DIR"
}

# Cross-platform xattr helpers (macOS uses xattr, Linux uses setfattr/getfattr)
set_xattr() {
	local name="$1" value="$2" path="$3"
	if command -v xattr &>/dev/null; then
		xattr -w "$name" "$value" "$path"
	else
		setfattr -n "$name" -v "$value" "$path"
	fi
}

get_xattr() {
	local name="$1" path="$2"
	if command -v xattr &>/dev/null; then
		xattr -p "$name" "$path"
	else
		getfattr -n "$name" --only-values "$path"
	fi
}

remove_xattr() {
	local name="$1" path="$2"
	if command -v xattr &>/dev/null; then
		xattr -d "$name" "$path"
	else
		setfattr -x "$name" "$path"
	fi
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
	[ ! -d "build" ]
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
	# Should have created a binary in build/
	[ -f "build/proj" ]
}

@test "--cov-detail=func shows function coverage" {
	create_test_project "$TEST_DIR/proj" 100
	cd "$TEST_DIR/proj"

	run "$BINARY" --min-coverage 80 --cov-detail func
	[ "$status" -eq 0 ]
	# Should show function names with line numbers
	[[ "$output" == *"main.go"* ]]
	[[ "$output" == *"main"* ]]
}

@test "--cov-detail=file shows file coverage" {
	create_test_project "$TEST_DIR/proj" 100
	cd "$TEST_DIR/proj"

	run "$BINARY" --min-coverage 80 --cov-detail file
	[ "$status" -eq 0 ]
	# Should show file names
	[[ "$output" == *"main.go"* ]]
}

@test "--json outputs valid JSON" {
	create_test_project "$TEST_DIR/proj" 100
	cd "$TEST_DIR/proj"

	run "$BINARY" --min-coverage 80 --json
	[ "$status" -eq 0 ]
	# Verify it's valid JSON with total field
	echo "$output" | jq -e '.total' > /dev/null
}

@test "auto-detects cmd/ layout" {
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
	[ -f "build/myapp" ]
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
	[ -f "build/linter" ]
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

@test "--add-watermark stores watermark" {
	create_test_project "$TEST_DIR/proj" 100
	cd "$TEST_DIR/proj"

	run "$BINARY" --min-coverage 80 --add-watermark
	[ "$status" -eq 0 ]
	[[ "$output" == *"Watermark set to"* ]]

	# Verify xattr was written
	wm="$(get_xattr user.go-toolchain.watermark .)"
	[ -n "$wm" ]
}

@test "watermark auto-enforced on next run" {
	create_test_project "$TEST_DIR/proj" 50
	cd "$TEST_DIR/proj"

	# Set watermark to 60% — coverage is ~50%, grace = 57.5%, effective = min(80,57.5) = 57.5
	# 50 < 57.5 → should fail
	set_xattr user.go-toolchain.watermark "60.0" .

	run "$BINARY" --min-coverage 80
	[ "$status" -ne 0 ]
	[[ "$output" == *"Watermark:"* ]]
	[[ "$output" == *"below minimum"* ]]
}

@test "watermark passes when coverage drops within 2.5% grace" {
	create_test_project "$TEST_DIR/proj" 50
	cd "$TEST_DIR/proj"

	# Set watermark to 52% — coverage is ~50%, grace = 49.5%, effective = min(80,49.5) = 49.5
	# 50 > 49.5 → should pass
	set_xattr user.go-toolchain.watermark "52.0" .

	run "$BINARY" --min-coverage 80
	[ "$status" -eq 0 ]
	[[ "$output" == *"Watermark:"* ]]
	[[ "$output" == *"Build successful"* ]]
}

@test "watermark fails when coverage drops more than 2.5%" {
	create_test_project "$TEST_DIR/proj" 50
	cd "$TEST_DIR/proj"

	# Set watermark to 60% — coverage is ~50%, grace = 57.5%, effective = min(80,57.5) = 57.5
	# 50 < 57.5 → should fail
	set_xattr user.go-toolchain.watermark "60.0" .

	run "$BINARY" --min-coverage 80
	[ "$status" -ne 0 ]
	[[ "$output" == *"below minimum"* ]]
}

@test "watermark ratchets up when coverage improves" {
	create_test_project "$TEST_DIR/proj" 100
	cd "$TEST_DIR/proj"

	# Set watermark to 50% — coverage is 100%, should ratchet up
	set_xattr user.go-toolchain.watermark "50.0" .

	run "$BINARY" --min-coverage 80
	[ "$status" -eq 0 ]
	[[ "$output" == *"Watermark updated:"* ]]

	# Verify watermark was updated to 100%
	wm="$(get_xattr user.go-toolchain.watermark .)"
	[[ "$wm" == "100.0" ]]
}

@test "--remove-watermark removes it" {
	create_test_project "$TEST_DIR/proj" 100
	cd "$TEST_DIR/proj"

	# Set a watermark first
	set_xattr user.go-toolchain.watermark "85.0" .

	# Pipe "y" for confirmation
	run bash -c "echo y | '$BINARY' --remove-watermark"
	[ "$status" -eq 0 ]
	[[ "$output" == *"Watermark removed"* ]]

	# Verify xattr is gone
	run get_xattr user.go-toolchain.watermark .
	[ "$status" -ne 0 ]
}

@test "--remove-watermark is hidden from help" {
	run "$BINARY" --help
	[ "$status" -eq 0 ]
	[[ "$output" == *"--add-watermark"* ]]
	[[ "$output" != *"--remove-watermark"* ]]
}

@test "--benchmark flag runs benchmarks after build" {
	create_test_project "$TEST_DIR/proj" 100
	cd "$TEST_DIR/proj"

	# Add a benchmark to the test file
	cat >> main_test.go <<EOF

func BenchmarkAdd(b *testing.B) {
	for i := 0; i < b.N; i++ {
		Add(1, 2)
	}
}
EOF

	run "$BINARY" --benchmark
	[ "$status" -eq 0 ]
	[[ "$output" == *"Build successful"* ]]
	[[ "$output" == *"Running benchmarks"* ]]
	[[ "$output" == *"Benchmarks complete"* ]]
}

@test "--benchmark flag shows in help" {
	run "$BINARY" --help
	[ "$status" -eq 0 ]
	[[ "$output" == *"--benchmark"* ]]
	[[ "$output" == *"Run benchmarks after build"* ]]
}

@test "bootstrap: can build itself 3x and produce identical binaries" {
	# Copy source to temp dir
	cp -r "$BATS_TEST_DIRNAME/.." "$TEST_DIR/go-toolchain"
	cd "$TEST_DIR/go-toolchain"

	# Clean any existing binaries
	rm -rf build stage1 stage2

	# Stage 1: Original compile with go build
	go build -o stage1 ./src

	# Stage 2: Use stage1 to build itself
	./stage1
	cp build/go-toolchain stage2
	rm -rf build

	# Stage 3: Use stage2 to build itself
	./stage2
	cp build/go-toolchain stage3

	# Compare binaries from stage 2 and 3 — they should be identical
	cmp stage2 stage3
}
