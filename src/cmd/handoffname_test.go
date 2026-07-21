package cmd

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// The action's build-output hand-off names are load-bearing strings: the
// cache key cache-upload derives from them (cache-xfer-<run_id>-<name>-
// <run_attempt>) must be distinct for every producer in a run, and the
// non-matrix name must never change shape (or every downstream
// cache-download naming `go-build-<job id>` -- this repo's own smoke and
// publish jobs included -- goes stale at once). These tests pin the exact
// templates in action.yml so any reshaping is a deliberate, test-visible
// decision.
//
// handoffNameTemplate is the per-job/per-leg hand-off name:
//   - non-matrix job: `matrix` is null inside composite steps, the gated
//     suffix collapses to '', and the name is byte-identical to the
//     pre-matrix shape `go-build-<job id>`.
//   - matrix leg: `matrix` is truthy, so the leg's strategy.job-index is
//     appended as `.m<index>` -- distinct per leg (job-index is unique
//     within the job) and stable across re-run attempts (the matrix
//     expansion, and with it each leg's index, is deterministic). The dot
//     keeps the suffix collision-proof against other job ids: job ids
//     cannot contain dots, so `go-build-<jobA>.m<i>` can never equal, or
//     restore-prefix-shadow, any `go-build-<jobB>`.
//
// (That matrix/strategy are evaluable inside composite action steps is the
// runner's documented manifest schema -- string-steps-context lists both --
// and cannot be exercised from a unit test; distinctness and stability
// above are GitHub's strategy.job-index semantics. What IS mechanically
// checkable is that action.yml carries exactly these templates, which is
// what these tests enforce.)
const handoffNameTemplate = "go-build-${{ github.job }}${{ matrix && format('.m{0}', strategy.job-index) || '' }}"

// legacyHandoffName is the deprecated bare alias, unchanged by the matrix
// suffix work: it stays racy in multi-producer runs and is saved
// continue-on-error until its consumers migrate.
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
	require.Len(t, uploads, 2, "expected exactly the per-job hand-off and the legacy alias")
	return uploads
}

func TestHandoffNameTemplates(t *testing.T) {
	steps := loadActionSteps(t)
	uploads := cacheUploadSteps(t, steps)

	perJob, alias := uploads[0], uploads[1]

	// The authoritative per-job/per-leg hand-off: exact template, so a
	// non-matrix job's name stays byte-identical to `go-build-<job id>` and
	// every matrix leg gets its own `.m<job-index>` suffix.
	assert.Equal(t, handoffNameTemplate, perJob.With["name"])
	assert.False(t, perJob.ContinueOnError,
		"the authoritative hand-off must fail loudly, never absorb a save conflict")

	// The deprecated bare alias: unchanged, and tolerated-on-failure because
	// the bare key is inherently racy in multi-producer runs.
	assert.Equal(t, legacyHandoffName, alias.With["name"])
	assert.True(t, alias.ContinueOnError,
		"the racy legacy alias must not fail the job on a save conflict")

	// Both hand off the same build outputs.
	assert.Equal(t, perJob.With["path"], alias.With["path"])
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

	// The notice tells consumers which per-job hand-off replaced the bare
	// alias; it must name exactly what the per-job step saves, or the
	// migration hint drifts from reality.
	assert.Contains(t, notice.Run, handoffNameTemplate)
}
