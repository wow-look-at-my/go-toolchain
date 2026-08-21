tests:
	- desc: go-toolchain version outputs version info
	  cmd: go run ./src version
	  inputs:
		env:
			GO_TOOLCHAIN_BUILDHOST_URL: "http://127.0.0.1:1"
	  outputs:
		stdout:
			- "Version:"
			- "Commit:"
