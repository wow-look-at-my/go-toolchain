package testifycast

import (
	"testing"
	. "time"

	"github.com/stretchr/testify/assert"
)

func getDot() Duration { return 0 }

// CaseDotImport uses a dot-imported numeric named type. The inserted conversion
// must be spelled unqualified (Duration), not with a "." qualifier.
func CaseDotImport(t *testing.T) {
	assert.Equal(t, 0, getDot())
}
