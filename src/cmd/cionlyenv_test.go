package cmd

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheckCIOnlyEnvRefusesEachVariableOutsideCI(t *testing.T) {
	for _, name := range []string{"GOORGPIN", "GO_TOOLCHAIN_FORK_COMMIT"} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("GITHUB_ACTIONS", "")
			t.Setenv("GOORGPIN", "")
			t.Setenv("GO_TOOLCHAIN_FORK_COMMIT", "")
			t.Setenv(name, "x")
			err := CheckCIOnlyEnv()
			require.Error(t, err)
			assert.Contains(t, err.Error(), name)
		})
	}
}

func TestCheckCIOnlyEnvAllowsBothInCI(t *testing.T) {
	t.Setenv("GITHUB_ACTIONS", "true")
	t.Setenv("GOORGPIN", "github.com/wow-look-at-my/x=v0.0.0-20260101000000-abcdefabcdef")
	t.Setenv("GO_TOOLCHAIN_FORK_COMMIT", "abc")
	assert.NoError(t, CheckCIOnlyEnv())
}

func TestCheckCIOnlyEnvAllowsAnUnsetEnvironment(t *testing.T) {
	t.Setenv("GITHUB_ACTIONS", "")
	t.Setenv("GOORGPIN", "")
	t.Setenv("GO_TOOLCHAIN_FORK_COMMIT", "")
	assert.NoError(t, CheckCIOnlyEnv())
}
