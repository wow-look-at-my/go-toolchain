package cmd

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// In a job container the runner.temp expression names the host path, which a
// node step cannot see. Only the RUNNER_TEMP variable names the mounted one.
func TestActionAvoidsHostTempExpression(t *testing.T) {
	t.Serial()
	for _, step := range loadActionSteps(t) {
		values := []string{step.Run}
		for _, v := range step.With {
			values = append(values, v)
		}
		for _, v := range step.Env {
			values = append(values, v)
		}
		for _, v := range values {
			assert.False(t, strings.Contains(strings.ReplaceAll(v, " ", ""), "${{runner.temp}}"),
				"step %q uses the runner.temp expression; use $RUNNER_TEMP or the step's default path", step.Name)
		}
	}
}
