package cmd

import (
	"testing"

	"github.com/wow-look-at-my/testify/assert"
	"github.com/wow-look-at-my/testify/require"
)

func TestInitTimeline(t *testing.T) {
	old := pipelineTimeline
	defer func() { pipelineTimeline = old }()

	pipelineTimeline = nil
	assert.Nil(t, GetTimeline())

	InitTimeline()
	require.NotNil(t, GetTimeline())
}

func TestGetTimelineReturnsNilBeforeInit(t *testing.T) {
	old := pipelineTimeline
	defer func() { pipelineTimeline = old }()

	pipelineTimeline = nil
	assert.Nil(t, GetTimeline())
}

func TestGetTimelineReturnsSameInstance(t *testing.T) {
	old := pipelineTimeline
	defer func() { pipelineTimeline = old }()

	InitTimeline()
	tl1 := GetTimeline()
	tl2 := GetTimeline()
	assert.Same(t, tl1, tl2)
}
