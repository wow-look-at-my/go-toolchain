package main

import (
	"os"
	"strings"
)

const (
	pazerSumDBName = "gosumdb.pazer.io"
	pazerSumDBKey  = "ffd608f1+Aeqpm5IuCkvaKEZ9HevpTL7hMf/LdLXOTow5rSw2dHeR"
	pazerProxyHost = "goproxy.pazer.io"
)

// pazerSumDBFull returns the fully-expanded GOSUMDB value for pazer.io,
// including the verifier name, public key, and the sumdb proxy URL.
//
// Format: <name>+<key> <url>
// The URL routes through the Athens proxy's /sumdb/<name>/ endpoint so that
// lookups hit /sumdb/gosumdb.pazer.io/lookup/... instead of bare /lookup/...
func pazerSumDBFull() string {
	return pazerSumDBName + "+" + pazerSumDBKey +
		" https://" + pazerProxyHost + "/sumdb/" + pazerSumDBName
}

// expandPazerSumDB checks whether gosumdb is a short-form pazer.io domain
// and returns the expanded full form. Returns ("", false) for non-pazer values.
func expandPazerSumDB(gosumdb string) (string, bool) {
	gosumdb = strings.TrimSpace(gosumdb)
	switch gosumdb {
	case pazerSumDBName, pazerProxyHost:
		return pazerSumDBFull(), true
	default:
		// Already expanded — starts with "gosumdb.pazer.io+" (has key inline).
		if strings.HasPrefix(gosumdb, pazerSumDBName+"+") {
			return gosumdb, true
		}
		return "", false
	}
}

// isUserProxy reports whether the GOPROXY value references a pazer.io proxy.
func isUserProxy(goproxy string) bool {
	for _, entry := range strings.Split(goproxy, ",") {
		entry = strings.TrimSpace(entry)
		if strings.Contains(entry, pazerProxyHost) {
			return true
		}
	}
	return false
}

// expandPazerProxy normalizes a GOPROXY value that references pazer.io:
//   - Adds "https://" scheme if missing
//   - Appends ",direct" fallback if missing
//
// Returns the input unchanged if it doesn't reference pazer.io.
func expandPazerProxy(goproxy string) string {
	if !isUserProxy(goproxy) {
		return goproxy
	}

	// Normalize each entry: add https:// to bare hostnames.
	parts := strings.Split(goproxy, ",")
	for i, p := range parts {
		p = strings.TrimSpace(p)
		if strings.Contains(p, pazerProxyHost) && !strings.Contains(p, "://") {
			p = "https://" + p
		}
		parts[i] = p
	}

	result := strings.Join(parts, ",")

	// Ensure ",direct" fallback is present.
	if !strings.Contains(result, "direct") {
		result += ",direct"
	}

	return result
}

// configureGoEnv sets GOPROXY, GOSUMDB, GONOSUMDB, and GONOSUMCHECK based on
// the user's existing environment. It respects user-configured proxies and
// sumdb settings instead of unconditionally disabling them.
func configureGoEnv() {
	goproxy := os.Getenv("GOPROXY")
	gosumdb := os.Getenv("GOSUMDB")

	// GOPROXY: expand pazer.io short forms, or default to "direct".
	if isUserProxy(goproxy) {
		os.Setenv("GOPROXY", expandPazerProxy(goproxy))
	} else {
		os.Setenv("GOPROXY", "direct")
	}

	// GOSUMDB: when the user sets a pazer.io sumdb, the private sumdb only
	// indexes private modules and returns 404 for public ones (e.g.
	// gopkg.in/yaml.v3). We can't use it as the global GOSUMDB because
	// child "go mod tidy" processes would fail verifying public transitive
	// dependencies.
	//
	// Instead, keep the default sum.golang.org for public module verification
	// and skip sumdb+checksum enforcement for private modules (which won't
	// appear on sum.golang.org). The private proxy (GOPROXY) still handles
	// downloading all modules; authentication is via ~/.netrc.
	if _, ok := expandPazerSumDB(gosumdb); ok {
		os.Unsetenv("GOSUMDB") // use default sum.golang.org
		os.Setenv("GONOSUMDB", "github.com/wow-look-at-my/*")
		os.Setenv("GONOSUMCHECK", "github.com/wow-look-at-my/*")
		return
	}

	// Default: disable sumdb phone-home for all modules.
	// Use GONOSUMDB instead of GOSUMDB=off so toolchain auto-downloads still work.
	os.Setenv("GONOSUMDB", "*")
	os.Setenv("GONOSUMCHECK", "*")
}
