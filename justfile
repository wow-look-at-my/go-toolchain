[private]
help:
	@just --list

alias test := build

build:
	go mod tidy
	go test -coverprofile=coverage.out ./src/...
	go build -o build/go-safe-build ./src
	bats tests/
