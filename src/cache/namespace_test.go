package cache

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCanonicalKeyNamespace(t *testing.T) {
	t.Serial()
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want string // "" means: expect the hex hash of the input
	}{
		{"empty stays empty", "", ""},
		{"canonical 16-hex passes through", "0123456789abcdef", "0123456789abcdef"},
		{"canonical 2-hex passes through", "ab", "ab"},
		{"canonical 64-hex passes through", strings.Repeat("ab", 32), strings.Repeat("ab", 32)},
		{"odd length is hashed", "abc", ""},
		{"single char is hashed", "a", ""},
		{"uppercase is hashed", "ABCDEF12", ""},
		{"non-hex is hashed", "toolchain-v178", ""},
		{"over 64 chars is hashed", strings.Repeat("ab", 33), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := CanonicalKeyNamespace(tc.in)
			want := tc.want
			if want == "" && tc.in != "" {
				sum := sha256.Sum256([]byte(tc.in))
				want = fmt.Sprintf("%x", sum[:8])
			}
			assert.Equal(t, want, got)
			// Every non-empty result must itself be canonical (idempotent).
			if tc.in != "" {
				assert.Equal(t, got, CanonicalKeyNamespace(got), "canonicalization must be idempotent")
				assert.True(t, isCanonicalNamespace(got), "result must be canonical")
			}
		})
	}
}
