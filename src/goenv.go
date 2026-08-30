package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/wow-look-at-my/go-toolchain/src/cmd"
	"github.com/wow-look-at-my/go-toolchain/src/logger"
)

// proxyConfig is the JSON structure inside GO_PROXY_CONFIG (base64-encoded).
type proxyConfig struct {
	Proxy    string `json:"proxy"`
	User     string `json:"user"`
	Username string `json:"username"`
	Login    string `json:"login"`
	Password string `json:"password"`
	Pass     string `json:"pass"`
	SumDBKey string `json:"sumdb_key"`
}

// user returns the earliest non-empty user field.
func (c *proxyConfig) user() string {
	if c.User != "" {
		return c.User
	}
	if c.Username != "" {
		return c.Username
	}
	return c.Login
}

// password returns the earliest non-empty password field.
func (c *proxyConfig) password() string {
	if c.Password != "" {
		return c.Password
	}
	return c.Pass
}

// proxyHost extracts the hostname from the proxy URL.
func (c *proxyConfig) proxyHost() string {
	u := c.Proxy
	// Strip scheme.
	if i := strings.Index(u, "://"); i >= 0 {
		u = u[i+3:]
	}
	// Strip path.
	if i := strings.Index(u, "/"); i >= 0 {
		u = u[:i]
	}
	return u
}

// sumdbName extracts the verifier name from the sumdb key.
// Key format: <name>+<hash>+<base64key>
func (c *proxyConfig) sumdbName() string {
	if i := strings.Index(c.SumDBKey, "+"); i >= 0 {
		return c.SumDBKey[:i]
	}
	return ""
}

// gosumdb returns the full GOSUMDB value: "<key> <proxy>/sumdb/<name>".
func (c *proxyConfig) gosumdb() string {
	name := c.sumdbName()
	if name == "" || c.Proxy == "" {
		return ""
	}
	proxy := strings.TrimRight(c.Proxy, "/")
	return c.SumDBKey + " " + proxy + "/sumdb/" + name
}

// parseProxyConfig reads GO_PROXY_CONFIG (base64 JSON) and returns the
// decoded config. Returns nil if the env var is unset or unparseable.
func parseProxyConfig() *proxyConfig {
	raw := os.Getenv("GO_PROXY_CONFIG")
	if raw == "" {
		return nil
	}
	data, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		logger.WithSubsystem("proxy").Debug("GO_PROXY_CONFIG decode error: %v", err)
		return nil
	}
	var cfg proxyConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		logger.WithSubsystem("proxy").Debug("GO_PROXY_CONFIG parse error: %v", err)
		return nil
	}
	return &cfg
}

// writeNetrc appends a machine entry for host to ~/.netrc.
// Writes to a temp file then atomically renames to avoid partial reads.
func writeNetrc(host, user, password string) {
	if host == "" || user == "" || password == "" {
		return
	}
	proxyLog := logger.WithSubsystem("proxy")
	home, err := os.UserHomeDir()
	if err != nil {
		proxyLog.Debug("netrc: %v", err)
		return
	}
	netrcPath := filepath.Join(home, ".netrc")
	// Read existing content to avoid duplicates.
	existing, _ := os.ReadFile(netrcPath)
	if strings.Contains(string(existing), "machine "+host) {
		return
	}
	newContent := string(existing) + fmt.Sprintf("\nmachine %s login %s password %s\n", host, user, password)
	tmp, err := os.CreateTemp(home, ".netrc-tmp-*")
	if err != nil {
		proxyLog.Debug("netrc tmp: %v", err)
		return
	}
	tmpPath := tmp.Name()
	if _, err := tmp.WriteString(newContent); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		proxyLog.Debug("netrc write: %v", err)
		return
	}
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		proxyLog.Debug("netrc chmod: %v", err)
		return
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		proxyLog.Debug("netrc close: %v", err)
		return
	}
	if err := os.Rename(tmpPath, netrcPath); err != nil {
		os.Remove(tmpPath)
		proxyLog.Debug("netrc rename: %v", err)
	}
}

// ensureDirectFallback appends "|direct" so any proxy error falls through to
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

// configureGoEnv sets GOPROXY, GOSUMDB, GONOSUMDB, and GONOSUMCHECK. With
// GO_PROXY_CONFIG set, it writes ~/.netrc and defaults GOPROXY/GOSUMDB to
// the configured proxy; otherwise it falls back to the GOPROXY/GOSUMDB env
// vars, or to GOPROXY=direct with sumdb disabled if nothing is configured.
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

	// If GO_PROXY_CONFIG is set, write netrc credentials and default to
	// the configured proxy when GOPROXY/GOSUMDB aren't explicitly set.
	if cfg := parseProxyConfig(); cfg != nil {
		writeNetrc(cfg.proxyHost(), cfg.user(), cfg.password())
		if goproxy == "" && cfg.Proxy != "" {
			goproxy = cfg.Proxy
		}
		if gosumdb == "" {
			gosumdb = cfg.gosumdb()
		}
	}

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
