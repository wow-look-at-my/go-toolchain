package main

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnsureDirectFallback(t *testing.T) {
	// No "direct" present: append "|direct" so any proxy error falls through.
	assert.Equal(t, "https://proxy.example.com|direct", ensureDirectFallback("https://proxy.example.com"))
	// Existing "|direct" stays as-is.
	assert.Equal(t, "https://proxy.example.com|direct", ensureDirectFallback("https://proxy.example.com|direct"))
	// Trailing ",direct" upgrades to "|direct" so 503s fall through, not just 404/410.
	assert.Equal(t, "https://proxy.example.com|direct", ensureDirectFallback("https://proxy.example.com,direct"))
	assert.Equal(t, "https://a.com,https://b.com|direct", ensureDirectFallback("https://a.com,https://b.com,direct"))
	assert.Equal(t, "https://a.com,https://b.com|direct", ensureDirectFallback("https://a.com,https://b.com|direct"))
}

func TestParseProxyConfig_Valid(t *testing.T) {
	raw := `{"proxy":"https://proxy.example.com","user":"alice","password":"secret","sumdb_key":"mydb+abc123+AKeyHere"}`
	t.Setenv("GO_PROXY_CONFIG", base64.StdEncoding.EncodeToString([]byte(raw)))
	cfg := parseProxyConfig()
	require.NotNil(t, cfg)
	assert.Equal(t, "https://proxy.example.com", cfg.Proxy)
	assert.Equal(t, "alice", cfg.user())
	assert.Equal(t, "secret", cfg.password())
	assert.Equal(t, "proxy.example.com", cfg.proxyHost())
	assert.Equal(t, "mydb", cfg.sumdbName())
	assert.Equal(t, "mydb+abc123+AKeyHere https://proxy.example.com/sumdb/mydb", cfg.gosumdb())
}

func TestParseProxyConfig_UsernameField(t *testing.T) {
	raw := `{"proxy":"https://p.example.com","username":"bob","pass":"hunter2"}`
	t.Setenv("GO_PROXY_CONFIG", base64.StdEncoding.EncodeToString([]byte(raw)))
	cfg := parseProxyConfig()
	require.NotNil(t, cfg)
	assert.Equal(t, "bob", cfg.user())
	assert.Equal(t, "hunter2", cfg.password())
}

func TestParseProxyConfig_LoginField(t *testing.T) {
	raw := `{"proxy":"https://p.example.com","login":"carol","pass":"pw123"}`
	t.Setenv("GO_PROXY_CONFIG", base64.StdEncoding.EncodeToString([]byte(raw)))
	cfg := parseProxyConfig()
	require.NotNil(t, cfg)
	assert.Equal(t, "carol", cfg.user())
	assert.Equal(t, "pw123", cfg.password())
}

func TestParseProxyConfig_Unset(t *testing.T) {
	t.Setenv("GO_PROXY_CONFIG", "")
	assert.Nil(t, parseProxyConfig())
}

func TestParseProxyConfig_InvalidBase64(t *testing.T) {
	t.Setenv("GO_PROXY_CONFIG", "not-valid-base64!!!")
	assert.Nil(t, parseProxyConfig())
}

func TestParseProxyConfig_InvalidJSON(t *testing.T) {
	t.Setenv("GO_PROXY_CONFIG", base64.StdEncoding.EncodeToString([]byte("not json")))
	assert.Nil(t, parseProxyConfig())
}

func TestProxyConfig_ProxyHost(t *testing.T) {
	cfg := proxyConfig{Proxy: "https://goproxy.example.com/some/path"}
	assert.Equal(t, "goproxy.example.com", cfg.proxyHost())

	cfg2 := proxyConfig{Proxy: "goproxy.example.com"}
	assert.Equal(t, "goproxy.example.com", cfg2.proxyHost())
}

func TestProxyConfig_GosumdbNoKey(t *testing.T) {
	cfg := proxyConfig{Proxy: "https://proxy.example.com"}
	assert.Empty(t, cfg.gosumdb())
}

func TestProxyConfig_GosumdbNoProxy(t *testing.T) {
	cfg := proxyConfig{SumDBKey: "mydb+abc+AKey"}
	assert.Empty(t, cfg.gosumdb())
}

func TestProxyConfig_GosumdbTrailingSlash(t *testing.T) {
	cfg := proxyConfig{Proxy: "https://proxy.example.com/", SumDBKey: "mydb+abc+AKey"}
	assert.Equal(t, "mydb+abc+AKey https://proxy.example.com/sumdb/mydb", cfg.gosumdb())
}

func TestWriteNetrc_CreatesFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeNetrc("proxy.example.com", "alice", "secret")

	content, err := os.ReadFile(filepath.Join(home, ".netrc"))
	require.NoError(t, err)
	assert.Contains(t, string(content), "machine proxy.example.com login alice password secret")
}

func TestWriteNetrc_SkipsDuplicate(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	netrcPath := filepath.Join(home, ".netrc")
	os.WriteFile(netrcPath, []byte("machine proxy.example.com login alice password secret\n"), 0600)

	writeNetrc("proxy.example.com", "alice", "newsecret")

	content, err := os.ReadFile(netrcPath)
	require.NoError(t, err)
	// Should not have duplicated the entry.
	assert.Equal(t, "machine proxy.example.com login alice password secret\n", string(content))
}

func TestWriteNetrc_EmptyCredentials(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeNetrc("proxy.example.com", "", "secret")
	writeNetrc("proxy.example.com", "alice", "")
	writeNetrc("", "alice", "secret")

	_, err := os.Stat(filepath.Join(home, ".netrc"))
	assert.True(t, os.IsNotExist(err), "netrc should not be created with empty credentials")
}

func TestConfigureGoEnv_Default(t *testing.T) {
	t.Setenv("GO_PROXY_CONFIG", "")
	t.Setenv("GOPROXY", "")
	t.Setenv("GOSUMDB", "")
	t.Setenv("GONOSUMDB", "")
	t.Setenv("GONOSUMCHECK", "")

	configureGoEnv()

	assert.Equal(t, "direct", os.Getenv("GOPROXY"))
	assert.Equal(t, "*", os.Getenv("GONOSUMDB"))
	assert.Equal(t, "*", os.Getenv("GONOSUMCHECK"))
}

func TestConfigureGoEnv_ExplicitProxy(t *testing.T) {
	t.Setenv("GO_PROXY_CONFIG", "")
	t.Setenv("GOPROXY", "https://proxy.example.com")
	t.Setenv("GOSUMDB", "")
	t.Setenv("GONOSUMDB", "")
	t.Setenv("GONOSUMCHECK", "")

	configureGoEnv()

	assert.Equal(t, "https://proxy.example.com|direct", os.Getenv("GOPROXY"))
	// No GOSUMDB → disabled.
	assert.Equal(t, "*", os.Getenv("GONOSUMDB"))
}

func TestConfigureGoEnv_ExplicitProxyAndSumDB(t *testing.T) {
	t.Setenv("GO_PROXY_CONFIG", "")
	t.Setenv("GOPROXY", "https://proxy.example.com,direct")
	// A private checksum database: configureGoEnv refuses the public one outright (see TestUsesPublicSumDB).
	t.Setenv("GOSUMDB", "mydb+abc123 https://proxy.example.com/sumdb/mydb")
	t.Setenv("GONOSUMDB", "leftover")
	t.Setenv("GONOSUMCHECK", "leftover")

	configureGoEnv()

	// Trailing ",direct" is upgraded to "|direct" so 503s fall through.
	assert.Equal(t, "https://proxy.example.com|direct", os.Getenv("GOPROXY"))
	assert.Equal(t, "mydb+abc123 https://proxy.example.com/sumdb/mydb", os.Getenv("GOSUMDB"))
	assert.Empty(t, os.Getenv("GONOSUMDB"))
	assert.Empty(t, os.Getenv("GONOSUMCHECK"))
}

func TestConfigureGoEnv_GOProxyConfig(t *testing.T) {
	raw := `{"proxy":"https://proxy.example.com","user":"alice","password":"secret","sumdb_key":"mydb+abc123+AKeyHere"}`
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GO_PROXY_CONFIG", base64.StdEncoding.EncodeToString([]byte(raw)))
	t.Setenv("GOPROXY", "")
	t.Setenv("GOSUMDB", "")
	t.Setenv("GONOSUMDB", "")
	t.Setenv("GONOSUMCHECK", "")

	configureGoEnv()

	assert.Equal(t, "https://proxy.example.com|direct", os.Getenv("GOPROXY"))
	assert.Equal(t, "mydb+abc123+AKeyHere https://proxy.example.com/sumdb/mydb", os.Getenv("GOSUMDB"))
	assert.Empty(t, os.Getenv("GONOSUMDB"))
	assert.Empty(t, os.Getenv("GONOSUMCHECK"))

	// Netrc should be written.
	content, err := os.ReadFile(filepath.Join(home, ".netrc"))
	require.NoError(t, err)
	assert.Contains(t, string(content), "machine proxy.example.com login alice password secret")
}

func TestConfigureGoEnv_GOProxyConfigExplicitOverride(t *testing.T) {
	raw := `{"proxy":"https://proxy.example.com","user":"alice","password":"secret","sumdb_key":"mydb+abc123+AKeyHere"}`
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GO_PROXY_CONFIG", base64.StdEncoding.EncodeToString([]byte(raw)))
	t.Setenv("GOPROXY", "https://other-proxy.example.com,direct")
	// A private sumdb, since the public one is refused outright; precedence is exercised with an accepted value.
	t.Setenv("GOSUMDB", "otherdb+def456 https://other-proxy.example.com/sumdb/otherdb")
	t.Setenv("GONOSUMDB", "")
	t.Setenv("GONOSUMCHECK", "")

	configureGoEnv()

	// Explicit GOPROXY/GOSUMDB take precedence over GO_PROXY_CONFIG; ",direct" becomes "|direct" so 503s fall through.
	assert.Equal(t, "https://other-proxy.example.com|direct", os.Getenv("GOPROXY"))
	assert.Equal(t, "otherdb+def456 https://other-proxy.example.com/sumdb/otherdb", os.Getenv("GOSUMDB"))
}

func TestConfigureGoEnv_GOProxyConfigNoSumDBKey(t *testing.T) {
	raw := `{"proxy":"https://proxy.example.com","user":"alice","password":"secret"}`
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GO_PROXY_CONFIG", base64.StdEncoding.EncodeToString([]byte(raw)))
	t.Setenv("GOPROXY", "")
	t.Setenv("GOSUMDB", "")
	t.Setenv("GONOSUMDB", "")
	t.Setenv("GONOSUMCHECK", "")

	configureGoEnv()

	// Proxy from config, but no sumdb key → sumdb disabled.
	assert.Equal(t, "https://proxy.example.com|direct", os.Getenv("GOPROXY"))
	assert.Equal(t, "*", os.Getenv("GONOSUMDB"))
	assert.Equal(t, "*", os.Getenv("GONOSUMCHECK"))
}

func TestConfigureGoEnv_DirectPassthrough(t *testing.T) {
	t.Setenv("GO_PROXY_CONFIG", "")
	t.Setenv("GOPROXY", "direct")
	t.Setenv("GOSUMDB", "")
	t.Setenv("GONOSUMDB", "")
	t.Setenv("GONOSUMCHECK", "")

	configureGoEnv()

	assert.Equal(t, "direct", os.Getenv("GOPROXY"))
}

func TestConfigureGoEnv_OffPassthrough(t *testing.T) {
	t.Setenv("GO_PROXY_CONFIG", "")
	t.Setenv("GOPROXY", "off")
	t.Setenv("GOSUMDB", "")
	t.Setenv("GONOSUMDB", "")
	t.Setenv("GONOSUMCHECK", "")

	configureGoEnv()

	assert.Equal(t, "direct", os.Getenv("GOPROXY"))
}

// The public checksum database is never an option: it cannot hold a private
// module, so it can only ever fail on one, and asking it about a module
// announces that module's path to a third party.
func TestUsesPublicSumDB(t *testing.T) {
	// Refused: Go would contact sum.golang.org itself.
	assert.True(t, usesPublicSumDB("sum.golang.org"))
	assert.True(t, usesPublicSumDB("sum.golang.org+033de0ae+AkeyHere"))
	assert.True(t, usesPublicSumDB("sum.golang.org+033de0ae https://sum.golang.org"))
	// Refused even under another name, if the URL points back at the public host.
	assert.True(t, usesPublicSumDB("mydb+abc https://sum.golang.org/sumdb/mydb"))

	// Allowed: the org proxy's mirror, since the request goes to the proxy and discloses nothing to a third party.
	assert.False(t, usesPublicSumDB("sum.golang.org+033de0ae https://goproxy.example.com/sumdb/sum.golang.org"))
	assert.False(t, usesPublicSumDB("mydb+abc123 https://goproxy.example.com/sumdb/mydb"))
	assert.False(t, usesPublicSumDB("mydb+abc123"))
	assert.False(t, usesPublicSumDB(""))
	assert.False(t, usesPublicSumDB("off"))
}

func TestSumDBURLHost(t *testing.T) {
	assert.Equal(t, "sum.golang.org", sumDBURLHost("https://sum.golang.org/sumdb/x"))
	assert.Equal(t, "sum.golang.org", sumDBURLHost("sum.golang.org"))
	assert.Equal(t, "proxy.example.com", sumDBURLHost("http://proxy.example.com:8080/sumdb/x"))
}

// With nothing configured the default already disables sumdb phone-home, and
// it must keep doing so via GONOSUMDB rather than GOSUMDB=off (which would
// also break toolchain auto-downloads).
func TestConfigureGoEnvDefaultDisablesSumDB(t *testing.T) {
	for _, k := range proxyEnvVars {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
	t.Setenv("GO_PROXY_CONFIG", "")
	os.Unsetenv("GO_PROXY_CONFIG")

	configureGoEnv()

	assert.Equal(t, "*", os.Getenv("GONOSUMDB"))
	assert.Equal(t, "*", os.Getenv("GONOSUMCHECK"))
	assert.Empty(t, os.Getenv("GOSUMDB"), "never set to the public database")
}
