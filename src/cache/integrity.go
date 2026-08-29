package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// outputIDMatches verifies body's sha256 against outputID (the GOCACHEPROG
// contract), so remote corruption is never cached and served as valid.
func outputIDMatches(outputID string, body []byte) (got string, ok bool) {
	sum := sha256.Sum256(body)
	got = hex.EncodeToString(sum[:])
	return got, strings.EqualFold(got, outputID)
}
