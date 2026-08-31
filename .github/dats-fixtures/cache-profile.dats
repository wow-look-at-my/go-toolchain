# The first build's own cache report, asserted in a suite an engineer can run.
# These lines used to sit in a workflow step, where the only way to reproduce a
# red was to push. host-build copies build/profile.json in beside this file.

tests:
	# A nonzero tripwire means the remote SERVED an object that a client-side
	# integrity gate then had to refuse, so the shared cache carries poison.
	# put_refused_modindex is deliberately NOT gated: refusing to UPLOAD a
	# module index is normal, and large on every cold run.
	- desc: no integrity gate refused an object the remote served
	  cmd: 'jq -r "if .web == null then \"no-web-tier\" else \"checksum=\(.web.miss_checksum) buildid=\(.web.miss_buildid) modindex=\(.web.miss_modindex)\" end" {inputs.profile.json}'
	  timeout: 30s
	  inputs:
		copy:
			profile.json: ../../build/profile.json
	  outputs:
		stdout:
			0: "^(no-web-tier|checksum=0 buildid=0 modindex=0)$"

	# An advertised index with entries, yielding zero web hits on a fresh
	# runner's first build, is the dead-remote signature: the tier serves
	# nothing. The threshold keeps a nearly empty index from reading as dead.
	- desc: a populated cache index served at least one object
	  cmd: 'jq -r "if .web == null then \"no-web-tier\" elif (.web.index_keys > 1000 and .web.hits == 0) then \"dead-remote\" else \"serving\" end" {inputs.profile.json}'
	  timeout: 30s
	  inputs:
		copy:
			profile.json: ../../build/profile.json
	  outputs:
		stdout:
			0: "^(no-web-tier|serving)$"
