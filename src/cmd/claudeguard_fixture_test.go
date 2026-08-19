package cmd

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/dats/schema"
)

// TestSmokeLinuxFixtureSetsTMPDIRForAPERuns pins the env the smoke-linux job's
// agent-output-guard fixture depends on: every test that invokes the shipped
// APE must set TMPDIR=/tmp, or the cosmo loader stages its first-exec
// self-assimilation under the docker sandbox's HOME=/ (dats' docker backend
// runs commands as --user <host-uid>:<host-gid>, and a UID with no
// /etc/passwd entry in the image gets HOME=/ from Docker) and every APE
// invocation exits 121. This invariant was dropped once (commit 5fe6a42) and
// reddened smoke-linux until re-added; the fixture env is the one setting
// that works under BOTH dats backends (docker's image /tmp and bwrap's
// private tmpfs) -- the upstream dats HOME=/tmp default (wow-look-at-my/dats#38)
// would only cover docker. Parsing with dats' own schema keeps this honest:
// it fails if the fixture ever stops being valid dats, not just if the env
// key is missing.
func TestSmokeLinuxFixtureSetsTMPDIRForAPERuns(t *testing.T) {
	f, err := schema.ParseFile(filepath.Join("..", "..", ".github", "dats-fixtures", "smoke-linux-agent-output-guard.dats"))
	require.NoError(t, err, "fixture must parse with the pinned dats schema")

	invoking := 0
	for _, test := range f.Tests {
		if strings.Contains(test.Cmd, "gt-under-test") {
			invoking++
			require.Equal(t, "/tmp", test.Inputs.Env["TMPDIR"],
				"test %q invokes the shipped APE and must set TMPDIR=/tmp: dats' docker sandbox gives HOME=/ to a UID with no passwd entry, and the cosmo loader's ${TMPDIR:-${HOME:-/tmp}} chain then targets the unwritable filesystem root (exit 121)", test.Desc)
		}
	}
	require.Greater(t, invoking, 0, "fixture must actually exercise the shipped APE")
}
