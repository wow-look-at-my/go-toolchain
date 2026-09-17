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

	- desc: the from-scratch bootstrap handed off an APE
	  cmd: test -f ape/scratch/go-toolchain

	- desc: the from-scratch bootstrap reaches the same bytes as the release bootstrap
	  cmd: cmp ape/linux/go-toolchain ape/scratch/go-toolchain

	- desc: host-build handed off an APE
	  cmd: test -f ape/host/go-toolchain

	- desc: the build job reproduces the host-build APE it ran
	  cmd: cmp ape/host/go-toolchain ape/linux/go-toolchain
