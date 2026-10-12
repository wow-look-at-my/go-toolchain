// Package golangorg keeps this program and the go command it runs away from
// golang.org and every host under it. No flag, variable or setting turns the
// block off.
package golangorg

import (
	"fmt"
	"net/http"
	"os"
	"strings"
)

// Domain is the host that nothing here contacts, with every host under it.
const Domain = "golang.org"

// IsHost reports whether host is Domain or a host under it.
func IsHost(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	return host == Domain || strings.HasSuffix(host, "."+Domain)
}

// NamedHost answers the first host under Domain that a setting value names,
// or "" when it names none. A value is a list of URLs, host paths or
// checksum database names, split by commas, bars or blanks.
func NamedHost(value string) string {
	for _, field := range strings.FieldsFunc(value, func(char rune) bool {
		return char == ',' || char == '|' || char == ' ' || char == '\t' || char == '\n'
	}) {
		if host := fieldHost(field); IsHost(host) {
			return host
		}
	}
	return ""
}

// fieldHost reads the host of one field: a URL, a host path, or a checksum
// database name with its key after a plus sign.
func fieldHost(field string) string {
	if _, rest, ok := strings.Cut(field, "://"); ok {
		field = rest
	}
	if at := strings.LastIndex(field, "@"); at >= 0 {
		field = field[at+1:]
	}
	host, _, _ := strings.Cut(field, "/")
	host, _, _ = strings.Cut(host, "+")
	host, _, _ = strings.Cut(host, "?")
	if strings.HasPrefix(host, "[") {
		return host
	}
	host, _, _ = strings.Cut(host, ":")
	return host
}

// Settings are the environment variables that name a network endpoint for the go command or a tool it starts.
var Settings = []string{"GOPROXY", "GOSUMDB", "GOVULNDB"}

// CheckEnv refuses an environment in which of Settings names a host under
// Domain. The error names the setting and the host.
func CheckEnv() error {
	for _, name := range Settings {
		if host := NamedHost(os.Getenv(name)); host != "" {
			return fmt.Errorf("%s=%q names %s, and go-toolchain never contacts %s or a host under it; unset %s or point it elsewhere", name, os.Getenv(name), host, Domain, name)
		}
	}
	return nil
}

// guard refuses every request to a host under Domain, a redirect included,
// before its connection opens.
type guard struct {
	next http.RoundTripper
}

// RoundTrip refuses the request or hands it on.
func (g guard) RoundTrip(req *http.Request) (*http.Response, error) {
	if IsHost(req.URL.Hostname()) {
		if req.Body != nil {
			req.Body.Close()
		}
		return nil, fmt.Errorf("refusing %s: go-toolchain never contacts %s or a host under it", req.URL.Redacted(), Domain)
	}
	return g.next.RoundTrip(req)
}

// Install puts the guard in front of http.DefaultTransport, which every
// client with no transport of its own uses.
func Install() {
	if _, ok := http.DefaultTransport.(guard); ok {
		return
	}
	http.DefaultTransport = guard{next: http.DefaultTransport}
}
