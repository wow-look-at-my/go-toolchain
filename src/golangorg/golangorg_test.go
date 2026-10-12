package golangorg

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsHost(t *testing.T) {
	for _, host := range []string{"golang.org", "GOLANG.ORG", "golang.org.", "proxy.golang.org", "sum.golang.org", "google.golang.org", "a.b.golang.org"} {
		assert.True(t, IsHost(host), host)
	}
	for _, host := range []string{"", "go.dev", "github.com", "notgolang.org", "golang.org.example.com", "golang.orgx", "go.googlesource.com"} {
		assert.False(t, IsHost(host), host)
	}
}

func TestNamedHost(t *testing.T) {
	named := map[string]string{
		"https://proxy.golang.org,direct":                         "proxy.golang.org",
		"direct|https://proxy.golang.org":                         "proxy.golang.org",
		"proxy.golang.org":                                        "proxy.golang.org",
		"sum.golang.org":                                          "sum.golang.org",
		"sum.golang.org+033de0ae+Ac4zctda0e5eza":                  "sum.golang.org",
		"sum.golang.org+033de0ae https://goproxy.example/sumdb/x": "sum.golang.org",
		"mydb+abc https://sum.golang.org/sumdb/mydb":              "sum.golang.org",
		"https://user:pass@GOLANG.org:443/mod":                    "GOLANG.org",
		"https://vuln.golang.org":                                 "vuln.golang.org",
	}
	for value, host := range named {
		assert.Equal(t, host, NamedHost(value), value)
	}
	for _, value := range []string{"", "direct", "off", "https://proxy.example.com,direct", "mydb+abc123 https://goproxy.example.com/sumdb/mydb", "https://vuln.go.dev", "file:///srv/proxy"} {
		assert.Empty(t, NamedHost(value), value)
	}
}

func TestCheckEnvRefusesEachSetting(t *testing.T) {
	for _, name := range Settings {
		t.Run(name, func(t *testing.T) {
			for _, other := range Settings {
				t.Setenv(other, "")
			}
			require.NoError(t, CheckEnv())
			t.Setenv(name, "https://proxy.golang.org,direct")
			err := CheckEnv()
			require.Error(t, err)
			assert.Contains(t, err.Error(), name)
			assert.Contains(t, err.Error(), "proxy.golang.org")
		})
	}
}

// recorder answers every request it sees and remembers its host.
type recorder struct {
	hosts []string
}

func (rec *recorder) RoundTrip(req *http.Request) (*http.Response, error) {
	rec.hosts = append(rec.hosts, req.URL.Host)
	return nil, errors.New("recorder sends nothing")
}

// The guard refuses a request under golang.org before the next transport
// sees it, and hands every other request on.
func TestGuardRefusesBeforeTheConnection(t *testing.T) {
	rec := &recorder{}
	client := &http.Client{Transport: guard{next: rec}}
	for _, raw := range []string{"https://golang.org/x/text?go-get=1", "https://proxy.golang.org/golang.org/x/mod/@v/list", "https://sum.golang.org/latest", "https://google.golang.org/grpc?go-get=1"} {
		_, err := client.Get(raw)
		require.Error(t, err, raw)
		assert.Contains(t, err.Error(), "never contacts golang.org", raw)
	}
	assert.Empty(t, rec.hosts, "a refused request reached the next transport")

	_, err := client.Get("https://github.com/golang/text")
	require.Error(t, err)
	assert.Equal(t, []string{"github.com"}, rec.hosts)
}

// A redirect to golang.org is a new request, and the guard refuses it.
func TestGuardRefusesARedirect(t *testing.T) {
	server := httptest.NewServer(http.RedirectHandler("https://golang.org/x/tools?go-get=1", http.StatusFound))
	defer server.Close()
	client := &http.Client{Transport: guard{next: http.DefaultTransport}}
	_, err := client.Get(server.URL)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "never contacts golang.org")
}

func TestInstallWrapsTheDefaultTransportOnce(t *testing.T) {
	saved := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = saved })
	Install()
	Install()
	outer, ok := http.DefaultTransport.(guard)
	require.True(t, ok)
	_, nested := outer.next.(guard)
	assert.False(t, nested, "a second Install wrapped the guard again")
	_, err := http.Get("https://proxy.golang.org/")
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "never contacts golang.org"))
}
