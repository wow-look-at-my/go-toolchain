package cmd

import (
	"os"
	"os/exec"
	"strings"

	"github.com/wow-look-at-my/go-toolchain/src/logger"
)

// cosmoPlatformsProbeValue is the sentinel `go env` must echo back if aware.
const cosmoPlatformsProbeValue = "linux/amd64"

// Test seam: the probe execs the fork toolchain, which tests do not have.
var cosmoPlatformsSupportedFunc = cosmoPlatformsSupported

// cosmoPlatformsSupported reports whether the gosmopolitan toolchain at
// forkGoroot honors GOCOSMOPLATFORMS. An older fork ignores an unknown
// GOCOSMO* var silently and emits an APE covering every platform it can, so
// without this probe a build would report a platform set it never enforced.
// Support is declared by echoing the var back through `go env`.
func cosmoPlatformsSupported(forkGoroot string) bool {
	cmd := exec.Command(cosmoGoBinPath(forkGoroot), "env", cosmoPlatformsEnv)
	cmd.Env = append(os.Environ(),
		"GOTOOLCHAIN=local",
		"GOROOT="+forkGoroot,
		cosmoPlatformsEnv+"="+cosmoPlatformsProbeValue,
	)
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == cosmoPlatformsProbeValue
}

// cosmoPlatformsEnvValue returns the GOCOSMOPLATFORMS value for a fat-APE
// build, and warns when the toolchain cannot honor it.
//
// An empty result means "leave the variable unset", which is the fork's
// everything-default: either --cosmo-platforms all was asked for, or this
// toolchain predates platform sets and setting it would change nothing.
func cosmoPlatformsEnvValue(forkGoroot string, platforms []buildPlatform) string {
	if len(platforms) == 0 {
		return ""
	}
	if !cosmoPlatformsSupportedFunc(forkGoroot) {
		logger.Warn("⇒ Warning: the gosmopolitan toolchain at %s does not report %s through `go env`, so it predates platform-set support: the fat APE covers every platform the fork emits instead of just %s. The artifact still runs on all of %s — it is only larger than asked. Update the toolchain (%s, or delete the cached copy) to get the slimmed build.",
			forkGoroot, cosmoPlatformsEnv, platformList(platforms), platformList(platforms), cosmoBranchEnv)
		return ""
	}
	return platformList(platforms)
}
