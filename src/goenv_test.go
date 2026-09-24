package main

import (
	"encoding/base64"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/wow-look-at-my/go-toolchain/src/cmd"
)

func TestEnsureDirectFallback(t *testing.T) {
	t.Serial()
	// No "direct" present: append "|direct" so any proxy error falls through.
	assert.Equal(t, "https://proxy.example.com|direct", ensureDirectFallback("https://proxy.example.com"))
	// Existing "|direct" stays as-is.
	assert.Equal(t, "https://proxy.example.com|direct", ensureDirectFallback("https://proxy.example.com|direct"))
	// Trailing ",direct" upgrades to "|direct" so a server error falls through, not just a missing module.
	assert.Equal(t, "https://proxy.example.com|direct", ensureDirectFallback("https://proxy.example.com,direct"))
	assert.Equal(t, "https://a.com,https://b.com|direct", ensureDirectFallback("https://a.com,https://b.com,direct"))
	assert.Equal(t, "https://a.com,https://b.com|direct", ensureDirectFallback("https://a.com,https://b.com|direct"))
}

// The org secret still carries GO_PROXY_CONFIG, and the proxy it names is
// gone. A run that read it would fail every checksum lookup.
func TestConfigureGoEnvIgnoresGOProxyConfig(t *testing.T) {
	t.Serial()
	raw := `{"proxy":"https://proxy.example.com","user":"alice","password":"secret","sumdb_key":"mydb+abc123+AKeyHere"}`
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("GO_PROXY_CONFIG", base64.StdEncoding.EncodeToString([]byte(raw)))
	t.Setenv("GOPROXY", "")
	t.Setenv("GOSUMDB", "")
	t.Setenv("GONOSUMDB", "")
	t.Setenv("GONOSUMCHECK", "")

	configureGoEnv()

	assert.Equal(t, "direct", os.Getenv("GOPROXY"))
	assert.Empty(t, os.Getenv("GOSUMDB"))
	assert.Equal(t, "*", os.Getenv("GONOSUMDB"))
	assert.NoFileExists(t, home+"/.netrc")
}

func TestConfigureGoEnv_Default(t *testing.T) {
	t.Serial()
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
	t.Serial()
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
	t.Serial()
	t.Setenv("GO_PROXY_CONFIG", "")
	t.Setenv("GOPROXY", "https://proxy.example.com,direct")
	// A private checksum database: configureGoEnv refuses the public database outright (see TestUsesPublicSumDB).
	t.Setenv("GOSUMDB", "mydb+abc123 https://proxy.example.com/sumdb/mydb")
	t.Setenv("GONOSUMDB", "leftover")
	t.Setenv("GONOSUMCHECK", "leftover")

	configureGoEnv()

	// Trailing ",direct" is upgraded to "|direct" so 503s fall through.
	assert.Equal(t, "https://proxy.example.com|direct", os.Getenv("GOPROXY"))
	assert.Equal(t, "mydb+abc123 https://proxy.example.com/sumdb/mydb", os.Getenv("GOSUMDB"))
	assert.Equal(t, "github.com/wow-look-at-my/*", os.Getenv("GONOSUMDB"))
	assert.Equal(t, "github.com/wow-look-at-my/*", os.Getenv("GONOSUMCHECK"))
}

func TestConfigureGoEnv_DirectPassthrough(t *testing.T) {
	t.Serial()
	t.Setenv("GO_PROXY_CONFIG", "")
	t.Setenv("GOPROXY", "direct")
	t.Setenv("GOSUMDB", "")
	t.Setenv("GONOSUMDB", "")
	t.Setenv("GONOSUMCHECK", "")

	configureGoEnv()

	assert.Equal(t, "direct", os.Getenv("GOPROXY"))
}

func TestConfigureGoEnv_OffPassthrough(t *testing.T) {
	t.Serial()
	t.Setenv("GO_PROXY_CONFIG", "")
	t.Setenv("GOPROXY", "off")
	t.Setenv("GOSUMDB", "")
	t.Setenv("GONOSUMDB", "")
	t.Setenv("GONOSUMCHECK", "")

	configureGoEnv()

	assert.Equal(t, "direct", os.Getenv("GOPROXY"))
}

// The public checksum database is never an option: it cannot hold a private
// module, so it can only ever fail on such a module, and asking it about a
// module announces that module's path to an outside party.
func TestUsesPublicSumDB(t *testing.T) {
	t.Serial()
	// Refused: Go would contact sum.golang.org itself.
	assert.True(t, usesPublicSumDB("sum.golang.org"))
	assert.True(t, usesPublicSumDB("sum.golang.org+033de0ae+AkeyHere"))
	assert.True(t, usesPublicSumDB("sum.golang.org+033de0ae https://sum.golang.org"))
	// Refused even under another name, if the URL points back at the public host.
	assert.True(t, usesPublicSumDB("mydb+abc https://sum.golang.org/sumdb/mydb"))

	// Allowed: the org proxy's mirror, since the request goes to the proxy and discloses nothing to an outside party.
	assert.False(t, usesPublicSumDB("sum.golang.org+033de0ae https://goproxy.example.com/sumdb/sum.golang.org"))
	assert.False(t, usesPublicSumDB("mydb+abc123 https://goproxy.example.com/sumdb/mydb"))
	assert.False(t, usesPublicSumDB("mydb+abc123"))
	assert.False(t, usesPublicSumDB(""))
	assert.False(t, usesPublicSumDB("off"))
}

func TestSumDBURLHost(t *testing.T) {
	t.Serial()
	assert.Equal(t, "sum.golang.org", sumDBURLHost("https://sum.golang.org/sumdb/x"))
	assert.Equal(t, "sum.golang.org", sumDBURLHost("sum.golang.org"))
	assert.Equal(t, "proxy.example.com", sumDBURLHost("http://proxy.example.com:8080/sumdb/x"))
}

// With nothing configured the default already disables sumdb phone-home, and
// it must keep doing so via GONOSUMDB rather than GOSUMDB=off (which would
// also break toolchain auto-downloads).
func TestConfigureGoEnvDefaultDisablesSumDB(t *testing.T) {
	t.Serial()
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

// A checksum database holds public modules only, so a lookup of an org module
// is refused and the build dies in `go mod tidy`. Every org prefix must
// therefore be exempt whenever a database is configured.
func TestConfigureGoEnvExemptsOrgModulesFromSumDB(t *testing.T) {
	t.Serial()
	t.Setenv("GO_PROXY_CONFIG", "")
	t.Setenv("GOPROXY", "https://proxy.example.com")
	t.Setenv("GOSUMDB", "mydb+abc123 https://proxy.example.com/sumdb/mydb")
	t.Setenv("GONOSUMDB", "")
	t.Setenv("GONOSUMCHECK", "")

	configureGoEnv()

	for _, prefix := range cmd.OrgModulePrefixes {
		assert.Contains(t, os.Getenv("GONOSUMDB"), strings.TrimSuffix(prefix, "/")+"/*")
		assert.Contains(t, os.Getenv("GONOSUMCHECK"), strings.TrimSuffix(prefix, "/")+"/*")
	}
	// The exemption is per prefix: everything else stays verified.
	assert.NotEqual(t, "*", os.Getenv("GONOSUMDB"))
	// GOPRIVATE would take the module off the proxy as well, which is not wanted.
	assert.Empty(t, os.Getenv("GOPRIVATE"))
}
