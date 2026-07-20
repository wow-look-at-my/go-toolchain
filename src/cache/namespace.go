package cache

import (
	"crypto/sha256"
	"encoding/hex"
)

// KeyNamespaceEnv is the environment variable that scopes every cache action
// key of a cacheprog process to a namespace. It exists to make cross-build
// cache poisoning STRUCTURALLY impossible for builds done with the
// gosmopolitan fork toolchain: the fork stamps a constant release version
// ("go1.26.4cosmo") into every build, so cmd/go's release rule derives
// identical tool build IDs for DIFFERENT fork toolchain builds, identical tool
// IDs collide on action IDs, and a shared cache (the org-wide web tier, or a
// warm local tier) then serves objects compiled by one toolchain build into
// links done by another — the 2026-07-20 SIGSEGV-APE incident. The matrix
// fork-build path sets this variable to a content hash of the toolchain
// actually in use (see forkToolchainCacheNamespace in src/cmd), so two
// different toolchain builds can never share cache entries no matter what
// version string they stamp.
//
// Mechanics: the cacheprog derives its store key for a request as
// hex(ActionID) + namespace — the namespace is a pure hex SUFFIX. That keeps
// every property downstream code relies on:
//
//   - Unnamespaced keys are always exactly 64 hex chars (cmd/go action IDs are
//     SHA-256), namespaced keys are strictly longer, and cmd/go action IDs are
//     fixed-length — so a namespaced key can never equal an unnamespaced key,
//     and two keys with different (canonical) namespaces can never be equal.
//   - The build-id action guard keeps working unchanged: expectedBuildIDAction
//     hex-decodes the key and reads bytes [:15], and a suffix leaves the
//     original leading bytes intact — so a compiled package's stamped action
//     is still verified against the REAL cmd/go action ID. (A hashed
//     H(namespace‖key) rewrite would break that guard and turn every
//     namespaced compiled package into a refusal.)
//   - The loose tier's 00–ff bucket fanout (first two key chars) and the pure
//     lowercase-hex key shape are preserved.
//
// A namespaced cacheprog must never proxy to the shared cache daemon
// (ProxyToDaemon is a raw byte pipe and the daemon serves unnamespaced
// clients); runCacheProg runs the standalone Server instead and applies the
// namespace via Server.SetKeyNamespace.
const KeyNamespaceEnv = "GO_TOOLCHAIN_CACHE_NAMESPACE"

// canonicalNamespaceMaxLen bounds a pass-through namespace so keys stay short
// enough for filenames, URLs, and server-side metadata.
const canonicalNamespaceMaxLen = 64

// CanonicalKeyNamespace maps a raw namespace value (typically the
// KeyNamespaceEnv variable) to its canonical key-suffix form:
//
//   - "" stays "" (no namespace — keys are byte-identical to historic ones).
//   - A lowercase-hex string of even length between 2 and 64 chars is used
//     as-is (the shape forkToolchainCacheNamespace emits).
//   - Anything else is replaced by 16 hex chars of its SHA-256, so ANY
//     non-empty value yields a valid, deterministic suffix and there is no
//     input that silently disables namespacing.
//
// Even length is required so that a namespaced key remains valid hex of whole
// bytes: expectedBuildIDAction hex-decodes the full key to derive the build-id
// expectation, and an odd-length suffix would fail that decode and weaken the
// guard to "no expectation".
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
