# The failure the action prints when buildhost has no build of its commit is the
# only text a caller sees. It must name the exact edit: the workflow line, the
# ref to write, and that the caller acts without waiting or asking.

tests:
	- desc: the message names the line to change and the lkgb ref
	  cmd: grep -F 'FIX: in the workflow that failed, change "uses: wow-look-at-my/go-toolchain@${ref}" to "uses: wow-look-at-my/go-toolchain@lkgb".' action.yml
	  outputs:
		stdout:
			- "buildhost has no published go-toolchain build of"

	- desc: the message orders the edit and rules out waiting and asking
	  cmd: grep -F 'Edit that workflow now. Do not wait for ${ref} to publish. Do not ask first.' action.yml
	  outputs:
		stdout:
			- "core.setFailed"
