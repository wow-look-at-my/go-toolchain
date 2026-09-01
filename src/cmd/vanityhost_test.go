package cmd

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsVanityHostReachableWithChecker(t *testing.T) {
	old := vanityHostChecker
	defer func() { vanityHostChecker = old }()

	vanityHostChecker = func(host string) bool {
		return host == "reachable.example.com"
	}

	assert.True(t, isVanityHostReachable("reachable.example.com"))
	assert.False(t, isVanityHostReachable("unreachable.example.com"))
}
