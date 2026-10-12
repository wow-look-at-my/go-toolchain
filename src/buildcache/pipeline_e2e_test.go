package buildcache

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/go-containers/set"
	"github.com/wow-look-at-my/go-s3-server/cacheclient"
)

// A whole pipeline run starts go commands side by side: tidy, vet, the tests
// and the build. Every one of them must reach the store through the
// pipeline. The server sees a single client and at most a single download of
// the key index for the whole run.
func TestPipelineIsTheOnlyStoreClient(t *testing.T) {
	t.Serial()
	srv := newCacheServer(t)
	module := writeFixtureModule(t)
	gocache := filepath.Join(t.TempDir(), "gocache")
	scratch := t.TempDir()

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	run := exec.CommandContext(ctx, pipelineBinary(t), "--no-benchmark", "--no-profile")
	run.Dir = module
	run.Env = pipelineEnv(map[string]string{
		"CI":                          "1",
		cacheclient.ConfigEnv:         srv.config(t),
		"GOCACHE":                     gocache,
		"TMPDIR":                      scratch,
		"TMP":                         scratch,
		"TEMP":                        scratch,
		"GIT_CEILING_DIRECTORIES":     filepath.Dir(module),
		"GO_TOOLCHAIN_BUILDHOST_URL":  "http://127.0.0.1:1",
		"GO_TOOLCHAIN_GITHUB_API_URL": "http://127.0.0.1:1",
		"GO_TOOLCHAIN_NO_WATCHDOG":    "1",
	})
	var output bytes.Buffer
	run.Stdout = &output
	run.Stderr = &output
	run.WaitDelay = 10 * time.Second
	err := run.Run()
	require.NoError(t, err, "pipeline run:\n%s", output.String())

	indexGets, requests, clients := srv.snapshot()
	t.Logf("store: %d requests from %d client(s), %d key index download(s)", requests, clients, indexGets)
	assert.LessOrEqual(t, indexGets, 1, "key index downloads in a single run:\n%s", output.String())
	assert.Positive(t, requests, "the run never reached the store:\n%s", output.String())
	assert.Equal(t, 1, clients, "processes that reached the store themselves:\n%s", output.String())
	assert.Contains(t, output.String(), "cache: serving the build cache as", "the pipeline did not own the cache")
}

// writeFixtureModule writes a module whose run vets, tests and builds a
// package no cache has seen, so every go command of the run misses.
func writeFixtureModule(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "brokerowner")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	unique := time.Now().Format("20060102150405.000000000")
	files := map[string]string{
		"go.mod": "module example.com/brokerowner\n\ngo 1.24\n\nrequire github.com/stretchr/testify v1.12.1\n",
		"main.go": "package main\n\nimport \"fmt\"\n\n" +
			"const stamp = \"" + unique + "\"\n\n" +
			"func greeting(name string) string {\n\treturn \"hello, \" + name + \" \" + stamp\n}\n\n" +
			"func main() {\n\tfmt.Println(greeting(\"owner\"))\n}\n",
		"main_test.go": "package main\n\nimport (\n\t\"testing\"\n\n\t\"github.com/stretchr/testify/require\"\n)\n\n" +
			"func TestGreeting(t *testing.T) {\n\trequire.Equal(t, \"hello, owner \"+stamp, greeting(\"owner\"))\n}\n",
	}
	for name, body := range files {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644))
	}
	return dir
}

// pipelineBinary answers a path that starts this build of the pipeline. The
// pipeline running these tests put its go link on PATH and named it in
// GOCOMMAND. The link resolves to the pipeline, which runs as the go command
// under the name go and as the pipeline under any other.
func pipelineBinary(t *testing.T) string {
	t.Helper()
	goLink := os.Getenv("GOCOMMAND")
	if goLink == "" || strings.Contains(goLink, "\n") {
		found, err := exec.LookPath("go")
		require.NoError(t, err, "no go link on PATH: run these tests through the pipeline")
		goLink = found
	}
	exe, err := filepath.EvalSymlinks(goLink)
	require.NoError(t, err)
	if strings.TrimSuffix(filepath.Base(exe), ".exe") != "go" {
		return exe
	}
	named := filepath.Join(t.TempDir(), "go-toolchain"+filepath.Ext(exe))
	copyExecutable(t, exe, named)
	return named
}

func copyExecutable(t *testing.T, from, to string) {
	t.Helper()
	src, err := os.Open(from)
	require.NoError(t, err)
	defer src.Close()
	dst, err := os.OpenFile(to, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	require.NoError(t, err)
	_, err = io.Copy(dst, src)
	require.NoError(t, err)
	require.NoError(t, dst.Close())
}

// inheritedPlumbing names variables of the run executing these tests that
// would point the pipeline under test at that run's cache, coverage, step
// summary or commit.
var inheritedPlumbing = set.Of(
	cacheclient.BrokerEnv,
	"GO_BUILDCACHE_BROKER_OFF",
	cacheclient.ConfigEnv,
	cacheLogEnv,
	"GOCACHEDEBUG",
	"GOCACHE",
	"GOCOVERDIR",
	"GOFLAGS",
	"GOROOT",
	"CGO_ENABLED",
	"GO_TOOLCHAIN_DEPGEN_REEXEC",
)

// pipelineEnv answers this process's environment without the inherited
// plumbing and the GitHub Actions variables, with extra added.
func pipelineEnv(extra map[string]string) []string {
	var env []string
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if inheritedPlumbing.Contains(name) || strings.HasPrefix(name, "GITHUB_") || extra[name] != "" {
			continue
		}
		env = append(env, entry)
	}
	for name, value := range extra {
		env = append(env, name+"="+value)
	}
	return env
}
