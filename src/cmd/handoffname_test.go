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

// legacyPerJobHandoffName is the pre-build per-job name, kept so consumers that
// still download `go-build-<job id>` keep working; racy in multi-build jobs, saved continue-on-error.
const legacyPerJobHandoffName = "go-build-${{ github.job }}${{ matrix && format('.m{0}', strategy.job-index) || '' }}"

// legacyHandoffName is the deprecated bare alias; still racy in multi-producer runs, saved continue-on-error.
const legacyHandoffName = "go-build"

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
	require.Len(t, uploads, 3, "expected the job+build hand-off, the legacy per-job hand-off, and the bare alias")
	return uploads
}

func TestHandoffNameTemplates(t *testing.T) {
	steps := loadActionSteps(t)
	uploads := cacheUploadSteps(t, steps)

	authoritative, legacyPerJob, alias := uploads[0], uploads[1], uploads[2]

	// Exact template: non-matrix stays `go-build-<job id>.b<build>`, each matrix leg gets its own `.m<job-index>` suffix.
	assert.Equal(t, handoffNameTemplate, authoritative.With["name"])
	assert.False(t, authoritative.ContinueOnError,
		"the authoritative hand-off must fail loudly, never absorb a save conflict")

	// The legacy per-job name: kept for consumers that still download `go-build-<job id>`; racy in multi-build jobs.
	assert.Equal(t, legacyPerJobHandoffName, legacyPerJob.With["name"])
	assert.True(t, legacyPerJob.ContinueOnError,
		"the legacy per-job hand-off must not fail the job on a save conflict in a multi-build job")

	// The deprecated bare alias: tolerated-on-failure since it is racy in multi-producer runs.
	assert.Equal(t, legacyHandoffName, alias.With["name"])
	assert.True(t, alias.ContinueOnError,
		"the racy legacy alias must not fail the job on a save conflict")

	// All three hand off the same build outputs.
	assert.Equal(t, authoritative.With["path"], legacyPerJob.With["path"])
	assert.Equal(t, authoritative.With["path"], alias.With["path"])
}

func TestHandoffDeprecationNoticeNamesTheSavedHandoff(t *testing.T) {
	steps := loadActionSteps(t)

	var notice *actionStep
	for i, step := range steps {
		if strings.Contains(step.Run, "hand-off deprecation") {
			require.Nil(t, notice, "expected a single deprecation notice step")
			notice = &steps[i]
		}
	}
	require.NotNil(t, notice, "the legacy alias must keep its deprecation notice")

	// The notice must name exactly what the authoritative step saves, or the migration hint drifts from reality.
	assert.Contains(t, notice.Run, handoffNameTemplate)
}
