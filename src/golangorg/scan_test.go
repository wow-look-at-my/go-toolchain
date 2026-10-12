package golangorg

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/go-containers/set"
)

// endpointPatterns match a line that would send the go command, a workflow
// step or this program to golang.org. A module path in an import is a name
// and matches none of them.
var endpointPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b(proxy|sum|vuln|telemetry)\.golang\.org\b`),
	regexp.MustCompile(`(?i)https?://([a-z0-9-]+\.)*golang\.org\b`),
	regexp.MustCompile(`(?i)\b(google\.)?golang\.org/[\w./-]+@(v[0-9]|latest|master)`),
	regexp.MustCompile(`GOTOOLCHAIN\W{0,4}auto\b`),
}

// scannedExt names the files the scan reads: source, workflows, scripts and docs.
var scannedExt = set.Of[string](".go", ".yml", ".yaml", ".sh", ".mjs", ".md", ".dats")

// No committed line outside a test names a golang.org endpoint or sets
// GOTOOLCHAIN to auto. A test names them on purpose, to prove each is refused.
func TestNoLineReachesGolangOrg(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)
	require.FileExists(t, filepath.Join(root, "action.yml"))
	scanned := 0
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := entry.Name()
		if entry.IsDir() {
			if path != root && (strings.HasPrefix(name, ".") && name != ".github" || name == "_gosmopolitan" || name == "build" || name == "node_modules" || name == "testdata") {
				return filepath.SkipDir
			}
			return nil
		}
		if !scannedExt.Contains(filepath.Ext(name)) || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		scanned++
		rel, _ := filepath.Rel(root, path)
		for num, line := range strings.Split(string(data), "\n") {
			for _, pattern := range endpointPatterns {
				assert.False(t, pattern.MatchString(line), "%s:%d reaches golang.org (%s): %s", rel, num+1, pattern, strings.TrimSpace(line))
			}
		}
		return nil
	})
	require.NoError(t, err)
	assert.Greater(t, scanned, 100, "the scan read too few files to mean anything")
}

// Each pattern matches the line it exists to catch.
func TestEndpointPatternsCatchEachForm(t *testing.T) {
	for _, line := range []string{
		`export GOPROXY=https://proxy.golang.org,direct`,
		`GOSUMDB=sum.golang.org`,
		`curl https://golang.org/x/text?go-get=1`,
		`go install golang.org/x/tools/cmd/stringer@v0.49.0`,
		`go install google.golang.org/protobuf/cmd/protoc-gen-go@latest`,
		`GOTOOLCHAIN=auto go install ./src`,
		`os.Setenv("GOTOOLCHAIN", "auto")`,
	} {
		caught := false
		for _, pattern := range endpointPatterns {
			caught = caught || pattern.MatchString(line)
		}
		assert.True(t, caught, line)
	}
	for _, line := range []string{`"golang.org/x/mod/modfile"`, `replace golang.org/x/tools => github.com/wow-look-at-my/gosmopolitan_tools v0.0.0`, `os.Setenv("GOTOOLCHAIN", "local")`} {
		for _, pattern := range endpointPatterns {
			assert.False(t, pattern.MatchString(line), "%s matched %s", pattern, line)
		}
	}
}
