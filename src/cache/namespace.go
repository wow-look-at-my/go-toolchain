package cache

import (
	"crypto/sha256"
	"encoding/hex"
)

// KeyNamespaceEnv scopes cacheprog action keys per toolchain build to avoid cross-build cache poisoning; see docs/CACHE.md.
const KeyNamespaceEnv = "GO_TOOLCHAIN_CACHE_NAMESPACE"

// canonicalNamespaceMaxLen bounds a pass-through namespace so keys stay short for filenames, URLs, and metadata.
const canonicalNamespaceMaxLen = 64

// CanonicalKeyNamespace maps raw to a canonical hex suffix ("" for empty,
// SHA-256-derived otherwise). Length stays even -- callers hex-decode it
// whole for the build-id expectation.
func CanonicalKeyNamespace(raw string) string {
	if raw == "" {
		return ""
	}
	if isCanonicalNamespace(raw) {
		return raw
	}
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:8])
}

// isCanonicalNamespace reports whether s is already in canonical suffix form:
// lowercase hex, even length, 2..canonicalNamespaceMaxLen chars.
func isCanonicalNamespace(s string) bool {
	if len(s) < 2 || len(s) > canonicalNamespaceMaxLen || len(s)%2 != 0 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
