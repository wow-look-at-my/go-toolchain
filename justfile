[private]
help:
	@just --list

alias test := build

build:
	go mod tidy
	go test -coverprofile=coverage.out ./src/...
	go build -o go-safe-build ./src
	bats tests/
