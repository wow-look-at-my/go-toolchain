package cmd

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// Authoritative hand-off name: go-build-<job id>[.m<index>].b<build>, distinct
// per job, per matrix leg, AND per build (working-directory); pins action.yml's template.
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
func cacheUploadSteps(t *testing.T, steps []actionStep) []actionStep {
	t.Helper()
	var uploads []actionStep
	for _, step := range steps {
		if strings.Contains(step.Uses, "cache-upload") {
			uploads = append(uploads, step)
		}
	}
	require.Len(t, uploads, 1, "one hand-off: the job+build name")
	return uploads
}

func TestHandoffNameTemplates(t *testing.T) {
	t.Parallel()
	steps := loadActionSteps(t)
	uploads := cacheUploadSteps(t, steps)

	authoritative := uploads[0]

	// Exact template: non-matrix stays `go-build-<job id>.b<build>`, each matrix leg gets its own `.m<job-index>` suffix.
	assert.Equal(t, handoffNameTemplate, authoritative.With["name"])
	assert.False(t, authoritative.ContinueOnError,
		"the hand-off must fail loudly, never absorb a save conflict")
	assert.Equal(t, "${{ inputs.working-directory }}/build", authoritative.With["path"],
		"the hand-off carries the build outputs")
}
