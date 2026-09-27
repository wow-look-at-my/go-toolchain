package cmd

import (
	"errors"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/go-toolchain/src/runner"
)

// stubOrgPins stands in for CI and for `go list -m all`, and records each directory it lists.
func stubOrgPins(t *testing.T, inCI bool, lists map[string]string, err error) *[]string {
	t.Helper()
	var listed []string
	prevCI, prevList := orgPinsInCI, listOrgModules
	orgPinsInCI = func() bool { return inCI }
	listOrgModules = func(dir string) (string, error) {
		listed = append(listed, dir)
		if err != nil {
			return "", err
		}
		return lists[dir], nil
	}
	t.Cleanup(func() { orgPinsInCI, listOrgModules = prevCI, prevList })
	return &listed
}

const goListRoot = `github.com/stretchr/testify=v1.12.1
github.com/wow-look-at-my/slopfix=v0.0.0-20260926074833-23586e671eee
github.com/wow-look-at-my/dats=v0.0.0-20260910122754-5dfcc0b24b09
golang.org/x/tools=v0.49.0 github.com/wow-look-at-my/gosmopolitan_tools=v0.0.0-20260920035531-824084d2645f
example.com/local=v1.0.0 github.com/wow-look-at-my/nested=
`

func TestPinOrgModulesResolvesOnceInCI(t *testing.T) {
	t.Serial()
	t.Setenv(orgPinEnv, "")
	listed := stubOrgPins(t, true, map[string]string{
		".":    goListRoot,
		"tool": "github.com/wow-look-at-my/dats=v0.0.0-20260910122754-5dfcc0b24b09\ngithub.com/wow-look-at-my/yaml-fixed=v0.0.0-20260806231905-d99b869b77a1\n",
	}, nil)

	require.NoError(t, pinOrgModules([]string{".", "tool"}))

	assert.Equal(t, []string{".", "tool"}, *listed)
	assert.Equal(t, "github.com/wow-look-at-my/dats=v0.0.0-20260910122754-5dfcc0b24b09"+
		" github.com/wow-look-at-my/gosmopolitan_tools=v0.0.0-20260920035531-824084d2645f"+
		" github.com/wow-look-at-my/slopfix=v0.0.0-20260926074833-23586e671eee"+
		" github.com/wow-look-at-my/yaml-fixed=v0.0.0-20260806231905-d99b869b77a1",
		os.Getenv(orgPinEnv), "the pins cover every module, the target of a replace, and no third-party or local module")
}

func TestPinOrgModulesKeepsTheWorkflowPins(t *testing.T) {
	t.Serial()
	const set = "github.com/wow-look-at-my/slopfix=v0.0.0-20260926074833-23586e671eee"
	t.Setenv(orgPinEnv, set)
	listed := stubOrgPins(t, true, map[string]string{".": goListRoot}, nil)

	require.NoError(t, pinOrgModules([]string{"."}))

	assert.Empty(t, *listed, "a pin the workflow set is never resolved again")
	assert.Equal(t, set, os.Getenv(orgPinEnv))
}

func TestPinOrgModulesFailsWhenGoListFails(t *testing.T) {
	t.Serial()
	t.Setenv(orgPinEnv, "")
	stubOrgPins(t, true, nil, errors.New("go list -m all in .: exit status 1"))

	err := pinOrgModules([]string{"."})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "pinning the org modules for this run")
	assert.Empty(t, os.Getenv(orgPinEnv), "a failed resolve must not leave half a pin set behind")
}

func TestPinOrgModulesDoesNothingOutsideCI(t *testing.T) {
	t.Serial()
	t.Setenv(orgPinEnv, "")
	listed := stubOrgPins(t, false, map[string]string{".": goListRoot}, nil)

	require.NoError(t, pinOrgModules([]string{"."}))

	assert.Empty(t, *listed, "outside CI nothing resolves pins")
	assert.Empty(t, os.Getenv(orgPinEnv), "outside CI nothing sets GOORGPIN")
}

func TestPinOrgModulesSetsNothingWithNoOrgModule(t *testing.T) {
	t.Serial()
	t.Setenv(orgPinEnv, "")
	stubOrgPins(t, true, map[string]string{".": "github.com/stretchr/testify=v1.12.1\n"}, nil)

	require.NoError(t, pinOrgModules([]string{"."}))

	_, set := os.LookupEnv(orgPinEnv)
	assert.True(t, set, "t.Setenv leaves the variable present")
	assert.Empty(t, os.Getenv(orgPinEnv))
}

func TestResolveOrgPinsRefusesModulesThatDisagree(t *testing.T) {
	t.Serial()
	stubOrgPins(t, true, map[string]string{
		".":    "github.com/wow-look-at-my/dats=v0.0.0-20260910122754-5dfcc0b24b09\n",
		"tool": "github.com/wow-look-at-my/dats=v0.0.0-20260801000000-aaaaaaaaaaaa\n",
	}, nil)

	_, err := resolveOrgPins([]string{".", "tool"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "github.com/wow-look-at-my/dats resolves to")
}

func TestResolveOrgPinsRefusesAnEntryThatIsNoVersion(t *testing.T) {
	t.Serial()
	stubOrgPins(t, true, map[string]string{".": "github.com/wow-look-at-my/dats=master\n"}, nil)

	_, err := resolveOrgPins([]string{"."})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a version")
}

func TestComputeFingerprintIncludesOrgPins(t *testing.T) {
	t.Serial()
	chdirTemp(t)
	prevEnv := runEnv
	runEnv = []string{"PATH=/bin"}
	t.Cleanup(func() { runEnv = prevEnv })
	os.WriteFile("go.mod", []byte("module example.com\n\ngo 1.21\n"), 0644)

	t.Setenv(orgPinEnv, "github.com/wow-look-at-my/dats=v0.0.0-20260910122754-5dfcc0b24b09")
	fp1, err := computeFingerprint(runner.NewMock())
	require.NoError(t, err)

	t.Setenv(orgPinEnv, "github.com/wow-look-at-my/dats=v0.0.0-20260926000000-bbbbbbbbbbbb")
	fp2, err := computeFingerprint(runner.NewMock())
	require.NoError(t, err)

	assert.NotEqual(t, fp1, fp2, "a moved org dependency is a new input")
}
