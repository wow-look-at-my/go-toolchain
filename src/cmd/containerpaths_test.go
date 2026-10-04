package cmd

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// In a job container the runner.temp and github.action_path expressions name
// host paths, which a step in the container cannot see. Only the RUNNER_TEMP
// and GITHUB_ACTION_PATH variables name the mounted ones.
func TestActionAvoidsHostPathExpressions(t *testing.T) {
	t.Serial()
	hostPaths := map[string]string{
		"${{runner.temp}}":         "$RUNNER_TEMP",
		"${{github.action_path}}": "$GITHUB_ACTION_PATH",
	}
	for _, step := range loadActionSteps(t) {
		values := []string{step.Run}
		for _, v := range step.With {
			values = append(values, v)
		}
		for _, v := range step.Env {
			values = append(values, v)
		}
		for _, v := range values {
			for expr, variable := range hostPaths {
				assert.False(t, strings.Contains(strings.ReplaceAll(v, " ", ""), expr),
					"step %q uses %s, a host path in a container job; use %s", step.Name, expr, variable)
			}
		}
	}
}
