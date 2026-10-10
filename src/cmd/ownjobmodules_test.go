package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// teslamWorkflow has the shape of wow-look-at-my/teslam's CI: the root job
// sweeps the repository, and a second job builds the depthgen module with cgo.
const teslamWorkflow = `name: CI
on:
  push:
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: wow-look-at-my/go-toolchain@master
  depthgen:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - name: Build and test depthgen
        uses: wow-look-at-my/go-toolchain@master
        with:
          working-directory: depthgen
          cgo: 'true'
          cosmo-platforms: linux/amd64
  each:
    runs-on: ubuntu-latest
    strategy:
      matrix:
        dir: [tool]
    steps:
      - uses: wow-look-at-my/go-toolchain@master
        with:
          working-directory: ${{ matrix.dir }}
  other:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/setup-node@v4
        with:
          working-directory: tool
`

// writeTree creates each file of files under dir, with its parent directories.
func writeTree(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
	}
}

func teslamTree(t *testing.T) string {
	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, ".git"), 0o755))
	writeTree(t, dir, map[string]string{
		"go.mod":                   "module example.com/teslam\ngo 1.26\n",
		"depthgen/go.mod":          "module example.com/teslam/depthgen\ngo 1.26\n",
		"tool/go.mod":              "module example.com/teslam/tool\ngo 1.26\n",
		".github/workflows/ci.yml": teslamWorkflow,
	})
	return dir
}

func TestSweptModules_SkipsModuleBuiltByItsOwnStep(t *testing.T) {
	t.Serial()
	t.Chdir(teslamTree(t))

	modules, err := sweptModules()
	require.NoError(t, err)
	assert.Equal(t, []string{".", "tool"}, modules, "depthgen has its own cgo step; an expression and another action name no module")
}

func TestSweptModules_TheStepItselfBuildsItsModule(t *testing.T) {
	t.Serial()
	dir := teslamTree(t)
	t.Chdir(filepath.Join(dir, "depthgen"))

	modules, err := sweptModules()
	require.NoError(t, err)
	assert.Equal(t, []string{"."}, modules)
}

func TestSweptModules_PathsAreFromTheRepositoryRoot(t *testing.T) {
	t.Serial()
	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, ".git"), 0o755))
	writeTree(t, dir, map[string]string{
		"svc/web/go.mod":    "module example.com/web\n",
		"svc/worker/go.mod": "module example.com/worker\n",
		".github/workflows/worker.yaml": "jobs:\n  worker:\n    steps:\n" +
			"      - uses: wow-look-at-my/go-toolchain@master\n" +
			"        with:\n          working-directory: ./svc/worker/\n",
	})
	t.Chdir(filepath.Join(dir, "svc"))

	modules, err := sweptModules()
	require.NoError(t, err)
	assert.Equal(t, []string{"web"}, modules)
}

func TestSweptModules_NoWorkflowsKeepsEveryModule(t *testing.T) {
	t.Serial()
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"go.mod":      "module example.com/root\n",
		"tool/go.mod": "module example.com/root/tool\n",
	})
	t.Chdir(dir)

	modules, err := sweptModules()
	require.NoError(t, err)
	assert.Equal(t, []string{".", "tool"}, modules)
}

func TestSweptModules_UnreadableWorkflowFails(t *testing.T) {
	t.Serial()
	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, ".git"), 0o755))
	writeTree(t, dir, map[string]string{
		"go.mod":                    "module example.com/root\n",
		".github/workflows/bad.yml": "jobs: [\n",
	})
	t.Chdir(dir)

	_, err := sweptModules()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bad.yml")
}

func TestRepoRootOf(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".git"), []byte("gitdir: elsewhere\n"), 0o644))
	deep := filepath.Join(dir, "a", "b")
	require.NoError(t, os.MkdirAll(deep, 0o755))

	assert.Equal(t, dir, repoRootOf(deep), "a .git file marks a root as a .git directory does")
}
