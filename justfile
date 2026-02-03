[private]
help:
	@just --list

alias test := build

build:
	go mod tidy
	go test -coverprofile=coverage.out ./...
	go build -o go-safe-build .
	bats tests/
