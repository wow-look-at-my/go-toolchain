tests:
	- desc: go-toolchain version outputs version info
	  cmd: go run ./src version
	  outputs:
		stdout:
			- "go-toolchain"
