package cmd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/wow-look-at-my/go-containers/set"
	"golang.org/x/mod/modfile"

	"github.com/wow-look-at-my/go-toolchain/src/logger"
)

// wellKnownHosts are code-hosting domains that resolve directly without
// vanity URL meta-tag resolution.
var wellKnownHosts = set.Of(
	"github.com",
	"gitlab.com",
	"bitbucket.org",
	"golang.org",
	"google.golang.org",
	"gopkg.in",
)

// directMirrorHosts are hosts safe to rewrite a vanity module onto. They serve
// plain git repos with no further indirection; other hosts are left alone since
// rewriting would only swap one indirection for another.
var directMirrorHosts = set.Of(
	"github.com",
	"gitlab.com",
	"bitbucket.org",
)

type vanityModule struct {
	Path    string // e.g. "gotest.tools/gotestsum"
	Version string // e.g. "v1.13.0"
	Host    string // e.g. "gotest.tools"
}

// vanityReplace records a replace directive injected for a vanity module.
type vanityReplace struct {
	OldPath    string
	OldVersion string
	NewPath    string
	NewVersion string
}

// vanityState holds the injected replaces plus a go.sum snapshot from before
// injection, so removeVanityReplaces can undo go mod tidy's rewrite of go.sum
// to the replacement path.
type vanityState struct {
	Replaces  []vanityReplace
	OrigGoSum []byte
}

// parseVanityModulesFromSum reads go.sum and returns modules whose hosts are
// vanity URL domains (not well-known code hosts).
func parseVanityModulesFromSum() ([]vanityModule, error) {
	f, err := os.Open("go.sum")
	if err != nil {
		return nil, err
	}
	defer f.Close()

	seen := set.New[string]()
	var modules []vanityModule

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 3 {
			continue
		}
		modPath := fields[0]
		version := fields[1]

		// Normalize: strip /go.mod suffix from version field
		version = strings.TrimSuffix(version, "/go.mod")

		if seen.Contains(modPath) {
			continue
		}

		host := strings.SplitN(modPath, "/", 2)[0]
		if wellKnownHosts.Contains(host) {
			continue
		}

		seen.Add(modPath)
		modules = append(modules, vanityModule{
			Path:    modPath,
			Version: version,
			Host:    host,
		})
	}

	return modules, scanner.Err()
}

// vanityHostChecker abstracts host reachability for testing.
var vanityHostChecker func(host string) bool

// isVanityHostReachable checks if a vanity URL host responds to HTTPS.
func isVanityHostReachable(host string) bool {
	if vanityHostChecker != nil {
		return vanityHostChecker(host)
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Head("https://" + host)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return true
}

// resolveVanityVCSURL discovers the VCS repository URL and import prefix for
// a vanity module. The import prefix identifies the root of the vanity
// namespace that maps to the repository; any path beyond the prefix is a
// sub-module path within the repo.
//
// It first queries the Go module proxy (go mod download -json) to get the
// Origin URL and Subdir, then falls back to the go-import meta tag on the
// vanity host.
func resolveVanityVCSURL(modulePath, version string) (string, string, error) {
	// Strategy 1: use go mod download -json via proxy to get Origin.URL
	cmd := exec.Command("go", "mod", "download", "-json", modulePath+"@"+version)
	// Resolve outside the module dir, so this download does not pull in the main
	// module's full requirement graph and risk failing on other vanity modules.
	cmd.Dir = os.TempDir()
	cmd.Env = append(os.Environ(), "GOPROXY=https://proxy.golang.org,direct", "GOWORK=off", "GOFLAGS=")
	output, err := cmd.Output()
	if err == nil {
		var info struct {
			Origin *struct {
				URL    string `json:"URL"`
				Subdir string `json:"Subdir"`
			} `json:"Origin"`
		}
		if json.Unmarshal(output, &info) == nil && info.Origin != nil && info.Origin.URL != "" {
			importPrefix := modulePath
			if info.Origin.Subdir != "" {
				importPrefix = strings.TrimSuffix(modulePath, "/"+info.Origin.Subdir)
			}
			return info.Origin.URL, importPrefix, nil
		}
	}

	// Strategy 2: try go-import meta tag (host may be intermittently available)
	return resolveGoImportMeta(modulePath)
}

// resolveGoImportMeta fetches the go-import meta tag from a vanity host.
func resolveGoImportMeta(modulePath string) (vcsURL, importPrefix string, err error) {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get("https://" + modulePath + "?go-get=1")
	if err != nil {
		return "", "", fmt.Errorf("fetch go-import for %s: %w", modulePath, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return "", "", err
	}

	return parseGoImportMeta(string(body), modulePath)
}

// parseGoImportMeta extracts the VCS repo URL and import prefix from HTML
// containing a go-import meta tag.  The expected format is:
//
//	<meta name="go-import" content="prefix vcs repo-url">
func parseGoImportMeta(html, modulePath string) (repoURL, importPrefix string, err error) {
	for _, line := range strings.Split(html, "\n") {
		if !strings.Contains(line, "go-import") {
			continue
		}
		idx := strings.Index(line, `content="`)
		if idx < 0 {
			continue
		}
		rest := line[idx+len(`content="`):]
		end := strings.Index(rest, `"`)
		if end < 0 {
			continue
		}
		parts := strings.Fields(rest[:end])
		if len(parts) >= 3 && (modulePath == parts[0] || strings.HasPrefix(modulePath, parts[0]+"/")) {
			return parts[2], parts[0], nil
		}
	}
	return "", "", fmt.Errorf("no go-import meta found for %s", modulePath)
}

// vcsURLToModulePath strips the scheme and .git suffix from a VCS URL to get
// a bare module path.
func vcsURLToModulePath(vcsURL string) string {
	p := strings.TrimPrefix(vcsURL, "https://")
	p = strings.TrimPrefix(p, "http://")
	return strings.TrimSuffix(p, ".git")
}

// vanityVCSResolver abstracts VCS URL resolution for testing.
var vanityVCSResolver func(modulePath, version string) (string, string, error)

// injectVanityReplaces parses go.sum for vanity-URL modules, checks host
// reachability, and injects replace directives into go.mod for any module
// whose vanity host is unreachable.
//
// Returns the state (replaces + go.sum snapshot) so the caller can remove
// the replaces and restore go.sum after go mod tidy completes.
func injectVanityReplaces() (*vanityState, error) {
	modules, err := parseVanityModulesFromSum()
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if len(modules) == 0 {
		return nil, nil
	}

	// Group by host
	hostModules := make(map[string][]vanityModule)
	for _, m := range modules {
		hostModules[m.Host] = append(hostModules[m.Host], m)
	}

	// Check each vanity host; collect replaces for unreachable ones
	var replaces []vanityReplace
	for host, mods := range hostModules {
		if isVanityHostReachable(host) {
			continue
		}

		if !jsonOutput {
			logger.Info("⇒ Vanity host %s unreachable, resolving GitHub sources", host)
		}

		for _, m := range mods {
			resolve := resolveVanityVCSURL
			if vanityVCSResolver != nil {
				resolve = vanityVCSResolver
			}
			vcsURL, importPrefix, err := resolve(m.Path, m.Version)
			if err != nil {
				if !jsonOutput {
					logger.Warn("    warning: cannot resolve %s: %v", m.Path, err)
				}
				continue
			}
			ghPath := vcsURLToModulePath(vcsURL)
			if ghPath == "" || ghPath == m.Path {
				continue
			}

			// Only rewrite onto a direct code host; elsewhere a replace would just
			// move the indirection, so let the proxy handle it instead.
			if targetHost := strings.SplitN(ghPath, "/", 2)[0]; !directMirrorHosts.Contains(targetHost) {
				if !jsonOutput {
					logger.Info("    skipping %s: resolved host %s is not a direct mirror", m.Path, targetHost)
				}
				continue
			}

			// The path beyond the import prefix names a sub-module directory
			// within the repository (e.g. otel/trace).
			if importPrefix != "" && strings.HasPrefix(m.Path, importPrefix+"/") {
				ghPath += m.Path[len(importPrefix):]
			}

			// If the vanity module has a /vN major version suffix (e.g.
			// go.yaml.in/yaml/v3), the replacement path needs it too —
			// Go modules require the path suffix to match the major version.
			if parts := strings.Split(m.Path, "/"); len(parts) > 0 {
				last := parts[len(parts)-1]
				if len(last) >= 2 && last[0] == 'v' && last[1] >= '2' && last[1] <= '9' {
					ghParts := strings.Split(ghPath, "/")
					ghLast := ghParts[len(ghParts)-1]
					if ghLast != last {
						ghPath += "/" + last
					}
				}
			}

			replaces = append(replaces, vanityReplace{
				OldPath:    m.Path,
				OldVersion: m.Version,
				NewPath:    ghPath,
				NewVersion: m.Version,
			})
		}
	}

	if len(replaces) == 0 {
		return nil, nil
	}

	// Read and modify go.mod
	data, err := os.ReadFile("go.mod")
	if err != nil {
		return nil, err
	}

	f, err := modfile.Parse("go.mod", data, nil)
	if err != nil {
		return nil, err
	}

	var injected []vanityReplace
	for _, r := range replaces {
		if err := f.AddReplace(r.OldPath, r.OldVersion, r.NewPath, r.NewVersion); err != nil {
			if !jsonOutput {
				logger.Warn("    warning: failed to add replace for %s: %v", r.OldPath, err)
			}
			continue
		}
		if !jsonOutput {
			logger.Info("    replace %s %s => %s %s", r.OldPath, r.OldVersion, r.NewPath, r.NewVersion)
		}
		injected = append(injected, r)
	}

	if len(injected) == 0 {
		return nil, nil
	}

	// Snapshot go.sum before tidy rewrites it, so it can be restored later.
	origGoSum, err := os.ReadFile("go.sum")
	if err != nil {
		return nil, err
	}

	newData, err := f.Format()
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile("go.mod", newData, 0644); err != nil {
		return nil, err
	}
	return &vanityState{Replaces: injected, OrigGoSum: origGoSum}, nil
}

// removeVanityReplaces removes previously injected vanity replace directives
// from go.mod and restores go.sum to its pre-injection snapshot. The restore
// undoes the path swap go mod tidy performed while the replace was active
// (e.g. rewriting gonum.org/v1/gonum entries as github.com/gonum/gonum).
func removeVanityReplaces(state *vanityState) error {
	if state == nil || len(state.Replaces) == 0 {
		return nil
	}

	data, err := os.ReadFile("go.mod")
	if err != nil {
		return err
	}

	f, err := modfile.Parse("go.mod", data, nil)
	if err != nil {
		return err
	}

	for _, r := range state.Replaces {
		if err := f.DropReplace(r.OldPath, r.OldVersion); err != nil {
			return fmt.Errorf("remove replace for %s: %w", r.OldPath, err)
		}
	}

	// Collapse the empty replace slots DropReplace leaves behind, so go.mod
	// returns to its original shape.
	f.Cleanup()

	newData, err := f.Format()
	if err != nil {
		return err
	}
	if err := os.WriteFile("go.mod", newData, 0644); err != nil {
		return err
	}

	if state.OrigGoSum != nil {
		if err := os.WriteFile("go.sum", state.OrigGoSum, 0644); err != nil {
			return fmt.Errorf("restore go.sum: %w", err)
		}
	}
	return nil
}

// checkDirtyInCIWithVanityRestored runs the CI dirty-tree check against the tree as
// removeVanityReplaces will leave it: injected replaces dropped from go.mod, go.sum
// restored to its pre-injection snapshot. The active state is written back before
// returning, so mirror replaces still resolve modules for later phases. With no
// active vanity state, this is exactly checkDirtyInCI.
//
// This exists because the injected replaces are transient, removed only when
// RunTestsWithCoverage returns, while the fail-fast dirty check runs mid-function.
// A run where a vanity host was unreachable at probe time would otherwise fail on a
// canonically tidy tree. Checking the restored tree instead matches the later
// checkDirtyInCI call sites, which run after the deferred restore.
//
// Real dirt still fails: only this run's own injected replaces are dropped, so any
// other go.mod change survives and is reported. One narrowing: while a vanity host
// is down, go.sum drift is indistinguishable from tidy's path swap and gets
// restored away; it fails on the very next run once the host is reachable.
func checkDirtyInCIWithVanityRestored(state *vanityState) error {
	if state == nil || len(state.Replaces) == 0 || os.Getenv("CI") == "" {
		return checkDirtyInCI()
	}
	activeGoMod, modErr := os.ReadFile("go.mod")
	activeGoSum, sumErr := os.ReadFile("go.sum")
	if modErr != nil || sumErr != nil {
		// Cannot snapshot state; check the live tree instead of risking data loss.
		return checkDirtyInCI()
	}
	if err := removeVanityReplaces(state); err != nil {
		// Restore failed; fall back to checking the live tree below.
		logger.Warn("⇒ Warning: could not restore vanity replaces for the dirty check: %v", err)
	}
	dirtyErr := checkDirtyInCI()
	// Restore the active mirror state so later phases keep resolving through the
	// replaces. The deferred removeVanityReplaces does the final restore later.
	if err := os.WriteFile("go.mod", activeGoMod, 0644); err != nil && dirtyErr == nil {
		dirtyErr = fmt.Errorf("restore active go.mod after dirty check: %w", err)
	}
	if err := os.WriteFile("go.sum", activeGoSum, 0644); err != nil && dirtyErr == nil {
		dirtyErr = fmt.Errorf("restore active go.sum after dirty check: %w", err)
	}
	return dirtyErr
}
