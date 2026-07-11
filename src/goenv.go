package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
		fmt.Fprintf(os.Stderr, "proxy: GO_PROXY_CONFIG decode error: %v\n", err)
		return nil
	}
	var cfg proxyConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		fmt.Fprintf(os.Stderr, "proxy: GO_PROXY_CONFIG parse error: %v\n", err)
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
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "proxy: netrc: %v\n", err)
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
		fmt.Fprintf(os.Stderr, "proxy: netrc tmp: %v\n", err)
		return
	}
	tmpPath := tmp.Name()
	if _, err := tmp.WriteString(newContent); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		fmt.Fprintf(os.Stderr, "proxy: netrc write: %v\n", err)
		return
	}
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		fmt.Fprintf(os.Stderr, "proxy: netrc chmod: %v\n", err)
		return
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		fmt.Fprintf(os.Stderr, "proxy: netrc close: %v\n", err)
		return
	}
	if err := os.Rename(tmpPath, netrcPath); err != nil {
		os.Remove(tmpPath)
		fmt.Fprintf(os.Stderr, "proxy: netrc rename: %v\n", err)
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

func configureGoEnv() {
	defer func() {
		for _, k := range proxyEnvVars {
			if v := os.Getenv(k); v != "" {
				fmt.Fprintf(os.Stderr, "proxy: %s=%s\n", k, v)
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
		os.Setenv("GOSUMDB", gosumdb)
		os.Unsetenv("GONOSUMDB")
		os.Unsetenv("GONOSUMCHECK")
		return
	}

	// Default: disable sumdb phone-home for all modules.
	// Use GONOSUMDB instead of GOSUMDB=off so toolchain auto-downloads still work.
	os.Setenv("GONOSUMDB", "*")
	os.Setenv("GONOSUMCHECK", "*")
}
