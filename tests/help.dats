tests:
	- desc: go-toolchain --help shows usage
	  cmd: go run ./src --help
	  outputs:
		stdout:
			- "Build Go projects with coverage enforcement"
