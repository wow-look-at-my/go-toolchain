tests:
	- desc: buildhost names a gosmopolitan release, so every host resolves the same fork
	  cmd: go-toolchain version cosmo
	  outputs:
		stdout:
			0: "^v[0-9]+$"
