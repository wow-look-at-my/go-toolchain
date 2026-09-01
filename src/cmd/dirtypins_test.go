package cmd

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

const headGoMod = `module example.com/consumer

go 1.25.0

require (
	github.com/stretchr/testify v1.11.1
	github.com/wow-look-at-my/common-ai-api/go/client v0.0.0-20260101000000-000000000000 // go-toolchain:branch=master
)
`

func TestTrackedPinMovementForgivesTheVersionItOwns(t *testing.T) {
	t.Parallel()
	work := `module example.com/consumer

go 1.25.0

require (
	github.com/stretchr/testify v1.11.1
	github.com/wow-look-at-my/common-ai-api/go/client v0.0.0-20260812203640-d8426ef8d505 // go-toolchain:branch=master
)
`
	moved, ok := trackedPinMovement([]byte(headGoMod), []byte(work))
	assert.True(t, ok)
	assert.Equal(t, []string{"github.com/wow-look-at-my/common-ai-api/go/client"}, moved)
}

func TestTrackedPinMovementReportsAnUntrackedVersionChange(t *testing.T) {
	t.Parallel()
	work := `module example.com/consumer

go 1.25.0

require (
	github.com/stretchr/testify v1.12.0
	github.com/wow-look-at-my/common-ai-api/go/client v0.0.0-20260101000000-000000000000 // go-toolchain:branch=master
)
`
	_, ok := trackedPinMovement([]byte(headGoMod), []byte(work))
	assert.False(t, ok, "a dependency with no marker is a version somebody chose")
}

// The exclusion covers the version token and nothing else: a require the run
// added alongside a moved pin is new content, and a new content line is a
// commit somebody makes.
func TestTrackedPinMovementReportsAnAddedRequire(t *testing.T) {
	t.Parallel()
	work := `module example.com/consumer

go 1.25.0

require (
	github.com/stretchr/testify v1.11.1
	github.com/wow-look-at-my/common-ai-api/go/client v0.0.0-20260812203640-d8426ef8d505 // go-toolchain:branch=master
	github.com/wow-look-at-my/common-ai-api/go/core v0.0.0-20260812203640-d8426ef8d505 // go-toolchain:branch=master
)
`
	_, ok := trackedPinMovement([]byte(headGoMod), []byte(work))
	assert.False(t, ok)
}

func TestTrackedPinMovementReportsAMarkerThatAppeared(t *testing.T) {
	t.Parallel()
	work := `module example.com/consumer

go 1.25.0

require (
	github.com/stretchr/testify v1.11.1 // go-toolchain:branch=master
	github.com/wow-look-at-my/common-ai-api/go/client v0.0.0-20260812203640-d8426ef8d505 // go-toolchain:branch=master
)
`
	_, ok := trackedPinMovement([]byte(headGoMod), []byte(work))
	assert.False(t, ok, "which branch a dependency follows is a declaration, not a resolution")
}

func TestTrackedPinMovementIsFalseWithNothingMoved(t *testing.T) {
	t.Parallel()
	_, ok := trackedPinMovement([]byte(headGoMod), []byte(headGoMod))
	assert.False(t, ok)
}

func TestTrackedPinMovementIsFalseOnUnparseableInput(t *testing.T) {
	t.Parallel()
	_, ok := trackedPinMovement([]byte(headGoMod), []byte("not a go.mod {{{"))
	assert.False(t, ok)
}

func TestTrackedPinMovementFollowsAMarkerOnAReplace(t *testing.T) {
	t.Parallel()
	head := `module example.com/consumer

go 1.25.0

require charm.land/bubbletea/v2 v2.0.8

replace charm.land/bubbletea/v2 => github.com/wow-look-at-my/bubbletea/v2 v2.0.0-20260101000000-000000000000 // go-toolchain:branch=master
`
	work := `module example.com/consumer

go 1.25.0

require charm.land/bubbletea/v2 v2.0.8

replace charm.land/bubbletea/v2 => github.com/wow-look-at-my/bubbletea/v2 v2.0.0-20260812203640-351d2159f8d8 // go-toolchain:branch=master
`
	moved, ok := trackedPinMovement([]byte(head), []byte(work))
	assert.True(t, ok)
	assert.Equal(t, []string{"github.com/wow-look-at-my/bubbletea/v2"}, moved)
}

func TestSumDiffOnlyTouchesMovedModules(t *testing.T) {
	t.Parallel()
	const mod = "github.com/wow-look-at-my/common-ai-api/go/client"
	head := mod + " v0.0.0-20260101000000-000000000000 h1:old=\n" +
		mod + " v0.0.0-20260101000000-000000000000/go.mod h1:oldmod=\n" +
		"github.com/stretchr/testify v1.11.1 h1:t=\n"
	work := mod + " v0.0.0-20260812203640-d8426ef8d505 h1:new=\n" +
		mod + " v0.0.0-20260812203640-d8426ef8d505/go.mod h1:newmod=\n" +
		"github.com/stretchr/testify v1.11.1 h1:t=\n"

	assert.True(t, sumDiffOnlyTouches(head, work, []string{mod}))
	assert.False(t, sumDiffOnlyTouches(head, work, []string{"github.com/other/thing"}))
}

func TestSumDiffOnlyTouchesReportsAnUnrelatedHash(t *testing.T) {
	t.Parallel()
	const mod = "github.com/wow-look-at-my/common-ai-api/go/client"
	head := mod + " v0.0.0-20260101000000-000000000000 h1:old=\n"
	work := mod + " v0.0.0-20260812203640-d8426ef8d505 h1:new=\n" +
		"github.com/stretchr/testify v1.11.1 h1:t=\n"

	assert.False(t, sumDiffOnlyTouches(head, work, []string{mod}), "a hash the pin movement does not account for is dirt")
}

func TestStatusLinePath(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "go.mod", statusLinePath(" M go.mod"))
	assert.Equal(t, "go/go.sum", statusLinePath("?? go/go.sum"))
	assert.Equal(t, "new.go", statusLinePath("R  old.go -> new.go"))
	assert.Equal(t, "a b.go", statusLinePath(" M \"a b.go\""))
	assert.Equal(t, "", statusLinePath(""))
}
