# The first build's own timing profile, asserted in a suite an engineer can
# run. host-build copies build/profile.json in beside this file. Build cache
# hit/miss reporting moved to gosmopolitan's cmd/go (docs/CACHE.md); this
# fixture checks only that the actiongraph timing report itself is sane.

tests:
	- desc: the build profile reports real actions with real wall time
	  cmd: 'jq -r "\"schema=\(.schema) actions=\(.total_actions) executed=\(.executed_actions) wall=\(.wall_ms_total)\"" {inputs.profile.json}'
	  timeout: 30s
	  inputs:
		copy:
			profile.json: ../../build/profile.json
	  outputs:
		stdout:
			0: "^schema=2 actions=[1-9][0-9]* executed=[1-9][0-9]* wall=[1-9][0-9.]*$"
