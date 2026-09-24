package main

import (
	"os"
	"strings"

	"github.com/wow-look-at-my/go-toolchain/src/cmd"
	"github.com/wow-look-at-my/go-toolchain/src/logger"
)

// a direct download: pipe falls back on any error, comma only on not-found. A
// trailing ",direct" is upgraded; any other "direct" value is untouched.
func ensureDirectFallback(goproxy string) string {
	if strings.HasSuffix(goproxy, ",direct") {
		return strings.TrimSuffix(goproxy, ",direct") + "|direct"
	}
	if !strings.Contains(goproxy, "direct") {
		return goproxy + "|direct"
	}
	return goproxy
}

// proxyEnvVars are the Go environment variables that configureGoEnv manages.
var proxyEnvVars = []string{"GOPROXY", "GOSUMDB", "GONOSUMDB", "GONOSUMCHECK"}

// PublicSumDB is the checksum database this toolchain refuses to talk to.
const PublicSumDB = "sum.golang.org"

// usesPublicSumDB reports whether a GOSUMDB value would have Go contact the
// public checksum database ITSELF. GOSUMDB is "<name>", "<name>+<key>", or
// "<name>+<key> <url>"; only the URL form redirects lookups elsewhere, so
// sum.golang.org named WITH a proxy URL (the org's "<proxy>/sumdb/<name>"
// mirror) stays allowed. Refused: the bare name, or a URL pointing back at
// the public host anyway.
func usesPublicSumDB(gosumdb string) bool {
	fields := strings.Fields(gosumdb)
	if len(fields) == 0 {
		return false
	}
	name, _, _ := strings.Cut(fields[0], "+")
	if name != PublicSumDB {
		// A URL pointing at the public host counts even under another name.
		return len(fields) > 1 && sumDBURLHost(fields[1]) == PublicSumDB
	}
	if len(fields) == 1 {
		return true // bare name: Go contacts sum.golang.org directly
	}
	return sumDBURLHost(fields[1]) == PublicSumDB
}

// sumDBURLHost extracts the host from a GOSUMDB proxy URL, which may or may
// not carry a scheme.
func sumDBURLHost(raw string) string {
	s := strings.TrimPrefix(strings.TrimPrefix(raw, "https://"), "http://")
	host, _, _ := strings.Cut(s, "/")
	host, _, _ = strings.Cut(host, ":")
	return host
}

// configureGoEnv sets GOPROXY, GOSUMDB, GONOSUMDB, and GONOSUMCHECK from the
// GOPROXY/GOSUMDB env vars. With neither set, it uses GOPROXY=direct with
// sumdb disabled. GO_PROXY_CONFIG is ignored: the org proxy it names is gone.
func configureGoEnv() {
	proxyLog := logger.WithSubsystem("proxy")
	defer func() {
		for _, k := range proxyEnvVars {
			if v := os.Getenv(k); v != "" {
				proxyLog.Debug("%s=%s", k, v)
			}
		}
	}()

	goproxy := os.Getenv("GOPROXY")
	gosumdb := os.Getenv("GOSUMDB")

	// GOPROXY: use configured value with "|direct" fallback, or default to "direct".
	if goproxy != "" && goproxy != "direct" && goproxy != "off" {
		os.Setenv("GOPROXY", ensureDirectFallback(goproxy))
	} else {
		os.Setenv("GOPROXY", "direct")
	}

	// GOSUMDB: use configured value (full "<key> <url>" form or short name),
	// or disable sumdb phone-home.
	if gosumdb != "" {
		// The PUBLIC checksum database is never an option: querying it for a
		// private module announces that module's path to an outside party. This
		// is refused LOUDLY, not silently, so a misconfigured setting is not
		// believed for months.
		if usesPublicSumDB(gosumdb) {
			proxyLog.Error("GOSUMDB=%q names the public checksum database directly.", gosumdb)
			proxyLog.Error("sum.golang.org can never hold a private module, and querying it discloses the module path.")
			proxyLog.Error("Point GOSUMDB at the org proxy's /sumdb/ mirror, or leave it unset to disable sumdb entirely.")
			os.Exit(1)
		}
		os.Setenv("GOSUMDB", gosumdb)
		// A sumdb holds public modules only, so it refuses an org module.
		os.Setenv("GONOSUMDB", orgSumDBExemptions())
		os.Setenv("GONOSUMCHECK", orgSumDBExemptions())
		return
	}

	// A GOSUMDB this run declined still sits in the environment every child
	// reads, so it goes rather than staying as the setting nobody chose.
	os.Unsetenv("GOSUMDB")
	// GONOSUMDB, not GOSUMDB=off, so toolchain auto-downloads still work.
	os.Setenv("GONOSUMDB", "*")
	os.Setenv("GONOSUMCHECK", "*")
}

// orgSumDBExemptions is the GONOSUMDB glob list covering every org module path.
// GONOSUMDB and not GOPRIVATE: GOPRIVATE would also take the module off the
// proxy and send the fetch straight to git.
func orgSumDBExemptions() string {
	globs := make([]string, 0, len(cmd.OrgModulePrefixes))
	for _, prefix := range cmd.OrgModulePrefixes {
		globs = append(globs, strings.TrimSuffix(prefix, "/")+"/*")
	}
	return strings.Join(globs, ",")
}
