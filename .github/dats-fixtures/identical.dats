tests:
	- desc: the linux host handed off an APE
	  cmd: test -f ape/linux/go-toolchain

	- desc: the darwin host handed off an APE
	  cmd: test -f ape/darwin/go-toolchain

	- desc: the windows host handed off an APE
	  cmd: test -f ape/windows/go-toolchain

	- desc: the darwin build is the same bytes as the linux build
	  cmd: cmp ape/linux/go-toolchain ape/darwin/go-toolchain

	- desc: the windows build is the same bytes as the linux build
	  cmd: cmp ape/linux/go-toolchain ape/windows/go-toolchain
