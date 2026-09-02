package cmd

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// Pins action.yml's hand-off name template, distinct per job, matrix leg and build.
const handoffNameTemplate = "go-build-${{ github.job }}${{ matrix && format('.m{0}', strategy.job-index) || '' }}.b${{ steps.build-id.outputs.id }}"

// actionStep is the subset of a composite step the hand-off tests inspect.
type actionStep struct {
	Name            string            `yaml:"name"`
	Uses            string            `yaml:"uses"`
	Run             string            `yaml:"run"`
	ContinueOnError bool              `yaml:"continue-on-error"`
	With            map[string]string `yaml:"with"`
}

// loadActionSteps parses the repo root action.yml and returns its composite
// steps.
func loadActionSteps(t *testing.T) []actionStep {
	t.Helper()
	data, err := os.ReadFile("../../action.yml")
	require.NoError(t, err)

	var manifest struct {
		Runs struct {
			Steps []actionStep `yaml:"steps"`
		} `yaml:"runs"`
	}
	require.NoError(t, yaml.Unmarshal(data, &manifest))
	require.NotEmpty(t, manifest.Runs.Steps)
	return manifest.Runs.Steps
}

// cacheUploadSteps returns the action's cache-upload steps in order.
func cacheUploadSteps(steps []actionStep) []actionStep {
	var uploads []actionStep
	for _, step := range steps {
		if strings.Contains(step.Uses, "cache-upload") {
			uploads = append(uploads, step)
		}
	}
	return uploads
}

func TestHandoffNameTemplate(t *testing.T) {
	t.Parallel()
	uploads := cacheUploadSteps(loadActionSteps(t))
	require.Len(t, uploads, 1, "exactly one hand-off: a second name is a second key to collide on")

	handoff := uploads[0]
	// Exact template: non-matrix stays `go-build-<job id>.b<build>`, each matrix leg gets its own `.m<job-index>` suffix.
	assert.Equal(t, handoffNameTemplate, handoff.With["name"])
	assert.False(t, handoff.ContinueOnError,
		"the hand-off must fail loudly, never absorb a save conflict")
	assert.Equal(t, "${{ inputs.working-directory }}/build", handoff.With["path"])
}

// The per-job name and the bare `go-build` alias were racy in a multi-producer
// run; a consumer that still downloads either must migrate, not get the alias back.
func TestNoLegacyHandoffRemains(t *testing.T) {
	t.Parallel()
	for _, step := range loadActionSteps(t) {
		if name := step.With["name"]; strings.HasPrefix(name, "go-build") {
			assert.Equal(t, handoffNameTemplate, name, "step %q saves a legacy hand-off name", step.Name)
		}
		assert.NotContains(t, step.Run, "hand-off deprecation",
			"step %q: the deprecation notice went with the alias it announced", step.Name)
	}
}
