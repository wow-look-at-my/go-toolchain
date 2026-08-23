package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

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

// user returns the first non-empty user field.
func (c *proxyConfig) user() string {
	if c.User != "" {
		return c.User
	}
	if c.Username != "" {
		return c.Username
	}
	return c.Login
}

// password returns the first non-empty password field.
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

// ensureDirectFallback appends "|direct" to a GOPROXY value so that any
// upstream proxy error (e.g. a 503 with body "DNS cache overflow" from a
// flaky proxy) falls through to a direct download instead of failing the
// build. The pipe (|) separator falls back on any error; comma (,) only
// falls back on 404/410, which is too narrow for resilience.
//
// If the value ends with ",direct", it is upgraded to "|direct". Any
// other configuration containing "direct" is left untouched to respect
// explicit user intent.
func ensureDirectFallback(goproxy string) string {
	if strings.HasSuffix(goproxy, ",direct") {
		return strings.TrimSuffix(goproxy, ",direct") + "|direct"
	}
	if !strings.Contains(goproxy, "direct") {
		return goproxy + "|direct"
	}
	return goproxy
}

// configureGoEnv sets GOPROXY, GOSUMDB, GONOSUMDB, and GONOSUMCHECK.
//
// When GO_PROXY_CONFIG is set (base64 JSON with proxy URL, credentials, and
// optional sumdb key), it writes ~/.netrc for authentication and defaults
// GOPROXY/GOSUMDB to the configured proxy if not already set.
//
// Without GO_PROXY_CONFIG, falls back to GOPROXY/GOSUMDB env vars.
// If nothing is configured, defaults to GOPROXY=direct with sumdb disabled.
// proxyEnvVars are the Go environment variables that configureGoEnv manages.
var proxyEnvVars = []string{"GOPROXY", "GOSUMDB", "GONOSUMDB", "GONOSUMCHECK"}

// PublicSumDB is the checksum database this toolchain refuses to talk to.
const PublicSumDB = "sum.golang.org"

// usesPublicSumDB reports whether a GOSUMDB value would have Go contact the
// public checksum database ITSELF.
//
// GOSUMDB is "<name>", "<name>+<key>", or "<name>+<key> <url>". Only the
// last form redirects the lookups somewhere else, so a value naming
// sum.golang.org WITH a proxy URL — the standard "<proxy>/sumdb/<name>"
// mirror — is fine and stays allowed: the request goes to the org's proxy,
// nothing is disclosed to a third party. What is refused is the bare name,
// or a URL that points back at the public host anyway.
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
		goproxy = ensureDirectFallback(goproxy)
	} else {
		goproxy = "direct"
	}
	os.Setenv("GOPROXY", goproxy)
	persistGoEnv(map[string]string{"GOPROXY": goproxy})

	// GOSUMDB: use configured value (full "<key> <url>" form or short name),
	// or disable sumdb phone-home.
	if gosumdb != "" {
		// The PUBLIC checksum database is never an option here. It cannot
		// contain a private module, so it can only ever fail on one; and
		// asking it about a module announces that module's path to a third
		// party, which for a private path is the leak itself. Refused
		// LOUDLY rather than silently ignored: a build that quietly did
		// something other than the configured thing is how a setting gets
		// believed for months.
		if usesPublicSumDB(gosumdb) {
			proxyLog.Error("GOSUMDB=%q names the public checksum database directly.", gosumdb)
			proxyLog.Error("sum.golang.org can never hold a private module, and querying it discloses the module path.")
			proxyLog.Error("Point GOSUMDB at the org proxy's /sumdb/ mirror, or leave it unset to disable sumdb entirely.")
			os.Exit(1)
		}
		os.Setenv("GOSUMDB", gosumdb)
		os.Unsetenv("GONOSUMDB")
		os.Unsetenv("GONOSUMCHECK")
		persistGoEnv(map[string]string{"GOSUMDB": gosumdb, "GONOSUMDB": ""})
		return
	}

	// Default: disable sumdb phone-home for all modules.
	// Use GONOSUMDB instead of GOSUMDB=off so toolchain auto-downloads still work.
	os.Setenv("GONOSUMDB", "*")
	os.Setenv("GONOSUMCHECK", "*")
	// GONOSUMCHECK has no "go env -w" equivalent (go rejects it as an
	// unknown command variable) -- GONOSUMDB alone is what a later, separate
	// `go` invocation in the same job needs to skip the sumdb for a private
	// module, since GOENV persistence is this process's only channel to it.
	persistGoEnv(map[string]string{"GONOSUMDB": "*"})
}

// persistGoEnv writes vars to Go's persistent env config file (GOENV) via
// "go env -w", so a LATER, separate `go` invocation in the same job -- one
// that never runs through this process, such as a workflow step's own `go
// install` after this action's own step finishes -- inherits the same
// GOPROXY/GOSUMDB/GONOSUMDB this process just resolved. os.Setenv alone only
// reaches this process and its children; GOENV is the one channel that
// survives a process boundary within the same $HOME. An empty value clears
// a previously persisted override, matching os.Unsetenv's role above.
// Best-effort: a write failure still leaves this process itself correctly
// configured via its own env, so it only degrades a later step's routing.
func persistGoEnv(vars map[string]string) {
	proxyLog := logger.WithSubsystem("proxy")
	for k, v := range vars {
		args := []string{"env", "-w", k + "=" + v}
		if out, err := exec.Command("go", args...).CombinedOutput(); err != nil {
			proxyLog.Debug("go env -w %s: %v: %s", k, err, out)
		}
	}
}
