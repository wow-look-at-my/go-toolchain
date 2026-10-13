package main

import (
	"encoding/base64"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/go-toolchain/src/cmd"
)

func TestEnsureDirectFallback(t *testing.T) {
	t.Serial()
	// No "direct" present: append ",direct" so a module the proxy lacks resolves.
	assert.Equal(t, "https://proxy.example.com,direct", ensureDirectFallback("https://proxy.example.com"))
	// Existing ",direct" stays as-is.
	assert.Equal(t, "https://proxy.example.com,direct", ensureDirectFallback("https://proxy.example.com,direct"))
	assert.Equal(t, "https://proxy.example.com,direct", ensureDirectFallback("https://proxy.example.com|direct"))
	assert.Equal(t, "https://a.com,https://b.com,direct", ensureDirectFallback("https://a.com,https://b.com|direct"))
	assert.Equal(t, "https://a.com,https://b.com,direct", ensureDirectFallback("https://a.com,https://b.com,direct"))
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

	require.NoError(t, configureGoEnv())

	assert.Equal(t, "direct", os.Getenv("GOPROXY"))
	assert.Equal(t, "off", os.Getenv("GOSUMDB"))
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

	require.NoError(t, configureGoEnv())

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

	require.NoError(t, configureGoEnv())

	assert.Equal(t, "https://proxy.example.com,direct", os.Getenv("GOPROXY"))
	// No GOSUMDB → disabled.
	assert.Equal(t, "*", os.Getenv("GONOSUMDB"))
}

func TestConfigureGoEnv_ExplicitProxyAndSumDB(t *testing.T) {
	t.Serial()
	t.Setenv("GO_PROXY_CONFIG", "")
	t.Setenv("GOPROXY", "https://proxy.example.com,direct")
	// A private checksum database: configureGoEnv refuses one under golang.org (see TestConfigureGoEnvRefusesGolangOrg).
	t.Setenv("GOSUMDB", "mydb+abc123 https://proxy.example.com/sumdb/mydb")
	t.Setenv("GONOSUMDB", "leftover")
	t.Setenv("GONOSUMCHECK", "leftover")

	require.NoError(t, configureGoEnv())

	// A trailing ",direct" is kept, so a fails rather than resolving from git.
	assert.Equal(t, "https://proxy.example.com,direct", os.Getenv("GOPROXY"))
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

	require.NoError(t, configureGoEnv())

	assert.Equal(t, "direct", os.Getenv("GOPROXY"))
}

func TestConfigureGoEnv_OffPassthrough(t *testing.T) {
	t.Serial()
	t.Setenv("GO_PROXY_CONFIG", "")
	t.Setenv("GOPROXY", "off")
	t.Setenv("GOSUMDB", "")
	t.Setenv("GONOSUMDB", "")
	t.Setenv("GONOSUMCHECK", "")

	require.NoError(t, configureGoEnv())

	assert.Equal(t, "direct", os.Getenv("GOPROXY"))
}

// A GOPROXY, GOSUMDB or GOVULNDB that names golang.org, or a host under it, is
// refused in any form, and the error names the setting.
func TestConfigureGoEnvRefusesGolangOrg(t *testing.T) {
	t.Serial()
	cases := map[string][]string{
		"GOPROXY":  {"https://proxy.golang.org", "https://proxy.golang.org,direct", "direct|https://proxy.golang.org", "https://golang.org/mod"},
		"GOSUMDB":  {"sum.golang.org", "sum.golang.org+033de0ae+AkeyHere", "sum.golang.org+033de0ae https://goproxy.example.com/sumdb/sum.golang.org", "mydb+abc https://sum.golang.org/sumdb/mydb"},
		"GOVULNDB": {"https://vuln.golang.org"},
	}
	for name, values := range cases {
		for _, value := range values {
			t.Setenv("GO_PROXY_CONFIG", "")
			t.Setenv("GOPROXY", "")
			t.Setenv("GOSUMDB", "")
			t.Setenv("GOVULNDB", "")
			t.Setenv(name, value)
			err := configureGoEnv()
			require.Error(t, err, "%s=%s", name, value)
			assert.Contains(t, err.Error(), name)
			assert.Contains(t, err.Error(), "golang.org")
		}
	}
}

// GOTOOLCHAIN is local and telemetry is off whatever the caller set, so the
// go command downloads no toolchain and uploads nothing.
func TestConfigureGoEnvPinsToolchainAndTelemetry(t *testing.T) {
	t.Serial()
	t.Setenv("GO_PROXY_CONFIG", "")
	t.Setenv("GOPROXY", "")
	t.Setenv("GOSUMDB", "")
	t.Setenv("GOTOOLCHAIN", "auto")
	t.Setenv("GOTELEMETRY", "on")

	require.NoError(t, configureGoEnv())

	assert.Equal(t, "local", os.Getenv("GOTOOLCHAIN"))
	assert.Equal(t, "off", os.Getenv("GOTELEMETRY"))
}

// A build names its own run so every go command it starts shares one org-head
// resolution. The name is the fork's owner/repo/run-id/attempt shape.
func TestConfigureGoEnvNamesALocalRun(t *testing.T) {
	t.Serial()
	t.Setenv("GO_PROXY_CONFIG", "")
	t.Setenv("GOPROXY", "")
	t.Setenv("GOSUMDB", "")
	t.Setenv("GONOSUMDB", "")
	t.Setenv("GONOSUMCHECK", "")
	t.Setenv(runEnv, "")
	t.Setenv("GITHUB_RUN_ID", "")
	t.Setenv("GITHUB_RUN_ATTEMPT", "")

	require.NoError(t, configureGoEnv())

	parts := strings.Split(os.Getenv(runEnv), "/")
	assert.Len(t, parts, 4)
	assert.NotContains(t, parts, "")
	assert.Equal(t, "1", parts[3])
}

// A run the caller already named is the one every go command must share.
func TestConfigureGoEnvKeepsANamedRun(t *testing.T) {
	t.Serial()
	t.Setenv("GO_PROXY_CONFIG", "")
	t.Setenv("GOPROXY", "")
	t.Setenv("GOSUMDB", "")
	t.Setenv("GONOSUMDB", "")
	t.Setenv("GONOSUMCHECK", "")
	t.Setenv(runEnv, "wow-look-at-my/consumer/4242/1")

	require.NoError(t, configureGoEnv())

	assert.Equal(t, "wow-look-at-my/consumer/4242/1", os.Getenv(runEnv))
}

// A CI run takes its identity from the GitHub variables, which name the run of
// every job. Naming a local run would replace none of that.
func TestConfigureGoEnvLeavesTheCIRunAlone(t *testing.T) {
	t.Serial()
	t.Setenv("GO_PROXY_CONFIG", "")
	t.Setenv("GOPROXY", "")
	t.Setenv("GOSUMDB", "")
	t.Setenv("GONOSUMDB", "")
	t.Setenv("GONOSUMCHECK", "")
	t.Setenv(runEnv, "")
	t.Setenv("GITHUB_RUN_ID", "7")
	t.Setenv("GITHUB_RUN_ATTEMPT", "2")

	require.NoError(t, configureGoEnv())

	assert.Empty(t, os.Getenv(runEnv))
}

// A new invocation is a new run, so the heads resolve again.
func TestLocalRunNameIsUniquePerInvocation(t *testing.T) {
	assert.NotEqual(t, localRunName(), localRunName())
}

// With nothing configured, the checksum database is off.
func TestConfigureGoEnvDefaultDisablesSumDB(t *testing.T) {
	t.Serial()
	for _, k := range proxyEnvVars {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
	t.Setenv("GO_PROXY_CONFIG", "")
	os.Unsetenv("GO_PROXY_CONFIG")

	require.NoError(t, configureGoEnv())

	assert.Equal(t, "*", os.Getenv("GONOSUMDB"))
	assert.Equal(t, "*", os.Getenv("GONOSUMCHECK"))
	assert.Equal(t, "off", os.Getenv("GOSUMDB"))
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

	require.NoError(t, configureGoEnv())

	for _, prefix := range cmd.OrgModulePrefixes {
		assert.Contains(t, os.Getenv("GONOSUMDB"), strings.TrimSuffix(prefix, "/")+"/*")
		assert.Contains(t, os.Getenv("GONOSUMCHECK"), strings.TrimSuffix(prefix, "/")+"/*")
	}
	// The exemption is per prefix: everything else stays verified.
	assert.NotEqual(t, "*", os.Getenv("GONOSUMDB"))
	// GOPRIVATE would take the module off the proxy as well, which is not wanted.
	assert.Empty(t, os.Getenv("GOPRIVATE"))
}
