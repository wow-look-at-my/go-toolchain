package main

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNeedsGo(t *testing.T) {
	orig := os.Args
	defer func() { os.Args = orig }()

	cases := []struct {
		args []string
		want bool
	}{
		{[]string{}, true},
		{[]string{"--help"}, false},
		{[]string{"-h"}, false},
		{[]string{"help"}, false},
		{[]string{"version"}, false},
		{[]string{"version", "raw"}, false},
		{[]string{"cacheprog"}, false},
		{[]string{"matrix"}, true},
		{[]string{"install"}, true},
		{[]string{"--", "--help"}, true},
	}
	for _, c := range cases {
		os.Args = append([]string{"go-toolchain"}, c.args...)
		assert.Equal(t, c.want, needsGo(), "args: %v", c.args)
	}
}
