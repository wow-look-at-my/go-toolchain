package main

import (
	"os"
	"testing"

	"github.com/wow-look-at-my/testify/assert"
)

func TestExpandPazerSumDB_ShortName(t *testing.T) {
	expanded, ok := expandPazerSumDB("gosumdb.pazer.io")
	assert.True(t, ok)
	assert.Equal(t, pazerSumDBFull(), expanded)
}

func TestExpandPazerSumDB_ProxyHost(t *testing.T) {
	expanded, ok := expandPazerSumDB("goproxy.pazer.io")
	assert.True(t, ok)
	assert.Equal(t, pazerSumDBFull(), expanded)
}

func TestExpandPazerSumDB_AlreadyExpanded(t *testing.T) {
	full := pazerSumDBFull()
	expanded, ok := expandPazerSumDB(full)
	assert.True(t, ok)
	assert.Equal(t, full, expanded)
}

func TestExpandPazerSumDB_Empty(t *testing.T) {
	_, ok := expandPazerSumDB("")
	assert.False(t, ok)
}

func TestExpandPazerSumDB_Default(t *testing.T) {
	_, ok := expandPazerSumDB("sum.golang.org")
	assert.False(t, ok)
}

func TestExpandPazerSumDB_Off(t *testing.T) {
	_, ok := expandPazerSumDB("off")
	assert.False(t, ok)
}

func TestIsUserProxy_Empty(t *testing.T) {
	assert.False(t, isUserProxy(""))
}

func TestIsUserProxy_Direct(t *testing.T) {
	assert.False(t, isUserProxy("direct"))
}

func TestIsUserProxy_PazerWithScheme(t *testing.T) {
	assert.True(t, isUserProxy("https://goproxy.pazer.io,direct"))
}

func TestIsUserProxy_PazerBare(t *testing.T) {
	assert.True(t, isUserProxy("goproxy.pazer.io"))
}

func TestIsUserProxy_GolangProxy(t *testing.T) {
	assert.False(t, isUserProxy("https://proxy.golang.org,direct"))
}

func TestExpandPazerProxy_Bare(t *testing.T) {
	assert.Equal(t, "https://goproxy.pazer.io,direct", expandPazerProxy("goproxy.pazer.io"))
}

func TestExpandPazerProxy_WithScheme(t *testing.T) {
	assert.Equal(t, "https://goproxy.pazer.io,direct", expandPazerProxy("https://goproxy.pazer.io"))
}

func TestExpandPazerProxy_AlreadyHasDirect(t *testing.T) {
	assert.Equal(t, "https://goproxy.pazer.io,direct", expandPazerProxy("https://goproxy.pazer.io,direct"))
}

func TestExpandPazerProxy_Unrelated(t *testing.T) {
	assert.Equal(t, "https://proxy.golang.org,direct", expandPazerProxy("https://proxy.golang.org,direct"))
}

func TestConfigureGoEnv_Default(t *testing.T) {
	t.Setenv("GOPROXY", "")
	t.Setenv("GOSUMDB", "")
	t.Setenv("GONOSUMDB", "")
	t.Setenv("GONOSUMCHECK", "")

	configureGoEnv()

	assert.Equal(t, "direct", os.Getenv("GOPROXY"))
	assert.Equal(t, "*", os.Getenv("GONOSUMDB"))
	assert.Equal(t, "*", os.Getenv("GONOSUMCHECK"))
}

func TestConfigureGoEnv_PazerSumDBOnly(t *testing.T) {
	t.Setenv("GOPROXY", "")
	t.Setenv("GOSUMDB", "gosumdb.pazer.io")
	t.Setenv("GONOSUMDB", "leftover")
	t.Setenv("GONOSUMCHECK", "leftover")

	configureGoEnv()

	assert.Equal(t, "direct", os.Getenv("GOPROXY"))
	// GOSUMDB cleared so public modules verify against default sum.golang.org.
	assert.Empty(t, os.Getenv("GOSUMDB"))
	// Private modules skip sumdb (not on sum.golang.org).
	assert.Equal(t, "github.com/wow-look-at-my/*", os.Getenv("GONOSUMDB"))
	assert.Equal(t, "github.com/wow-look-at-my/*", os.Getenv("GONOSUMCHECK"))
}

func TestConfigureGoEnv_PazerProxyAndSumDB(t *testing.T) {
	t.Setenv("GOPROXY", "goproxy.pazer.io")
	t.Setenv("GOSUMDB", "gosumdb.pazer.io")
	t.Setenv("GONOSUMDB", "")
	t.Setenv("GONOSUMCHECK", "")

	configureGoEnv()

	assert.Equal(t, "https://goproxy.pazer.io,direct", os.Getenv("GOPROXY"))
	assert.Empty(t, os.Getenv("GOSUMDB"))
	assert.Equal(t, "github.com/wow-look-at-my/*", os.Getenv("GONOSUMDB"))
	assert.Equal(t, "github.com/wow-look-at-my/*", os.Getenv("GONOSUMCHECK"))
}

func TestConfigureGoEnv_PazerProxyNoSumDB(t *testing.T) {
	t.Setenv("GOPROXY", "https://goproxy.pazer.io,direct")
	t.Setenv("GOSUMDB", "")
	t.Setenv("GONOSUMDB", "")
	t.Setenv("GONOSUMCHECK", "")

	configureGoEnv()

	// Proxy is preserved.
	assert.Equal(t, "https://goproxy.pazer.io,direct", os.Getenv("GOPROXY"))
	// No sumdb configured → disable.
	assert.Equal(t, "*", os.Getenv("GONOSUMDB"))
	assert.Equal(t, "*", os.Getenv("GONOSUMCHECK"))
}
