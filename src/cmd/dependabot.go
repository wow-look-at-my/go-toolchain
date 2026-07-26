package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/mod/modfile"

	"github.com/wow-look-at-my/go-toolchain/src/logger"
)

type depSnapshot struct {
	Version   int                    `json:"version"`
	SHA       string                 `json:"sha"`
	Ref       string                 `json:"ref"`
	Job       depJob                 `json:"job"`
	Detector  depDetector            `json:"detector"`
	Scanned   string                 `json:"scanned"`
	Manifests map[string]depManifest `json:"manifests"`
}

type depJob struct {
	ID         string `json:"id"`
	Correlator string `json:"correlator"`
}

type depDetector struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	URL     string `json:"url"`
}

type depManifest struct {
	Name     string                 `json:"name"`
	File     depManifestFile        `json:"file"`
	Resolved map[string]depResolved `json:"resolved"`
}

type depManifestFile struct {
	SourceLocation string `json:"source_location"`
}

type depResolved struct {
	PackageURL   string `json:"package_url"`
	Relationship string `json:"relationship"`
	Scope        string `json:"scope"`
}

func buildDepSnapshot() (*depSnapshot, error) {
	sha := os.Getenv("GITHUB_SHA")
	if sha == "" {
		return nil, fmt.Errorf("GITHUB_SHA required")
	}

	ref := os.Getenv("GITHUB_REF")
	if ref == "" {
		ref = "refs/heads/main"
	}

	goModPath := findGoMod()
	data, err := os.ReadFile(goModPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read go.mod: %w", err)
	}

	f, err := modfile.Parse("go.mod", data, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to parse go.mod: %w", err)
	}

	resolved := make(map[string]depResolved)
	for _, req := range f.Require {
		rel := "direct"
		if req.Indirect {
			rel = "indirect"
		}
		resolved[req.Mod.Path] = depResolved{
			PackageURL:   fmt.Sprintf("pkg:golang/%s@%s", req.Mod.Path, req.Mod.Version),
			Relationship: rel,
			Scope:        "runtime",
		}
	}

	sourceLocation := "go.mod"
	if workspace := os.Getenv("GITHUB_WORKSPACE"); workspace != "" {
		absGoMod, err := filepath.Abs(goModPath)
		if err == nil {
			if rel, err := filepath.Rel(workspace, absGoMod); err == nil {
				sourceLocation = filepath.ToSlash(rel)
			}
		}
	}

	jobID := os.Getenv("GITHUB_RUN_ID")
	if jobID == "" {
		jobID = fmt.Sprintf("local-%d", time.Now().Unix())
	}

	correlator := "go-toolchain"
	if wd := os.Getenv("GITHUB_WORKSPACE"); wd != "" {
		if cwd, err := os.Getwd(); err == nil {
			if rel, err := filepath.Rel(wd, cwd); err == nil && rel != "." {
				correlator += "-" + filepath.ToSlash(rel)
			}
		}
	}

	return &depSnapshot{
		Version: 0,
		SHA:     sha,
		Ref:     ref,
		Job: depJob{
			ID:         jobID,
			Correlator: correlator,
		},
		Detector: depDetector{
			Name:    "go-toolchain",
			Version: buildVersion,
			URL:     "https://github.com/wow-look-at-my/go-toolchain",
		},
		Scanned: time.Now().UTC().Format(time.RFC3339),
		Manifests: map[string]depManifest{
			sourceLocation: {
				Name:     sourceLocation,
				File:     depManifestFile{SourceLocation: sourceLocation},
				Resolved: resolved,
			},
		},
	}, nil
}

func postDepSnapshot(snapshot *depSnapshot) error {
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		token = os.Getenv("GH_TOKEN")
	}
	if token == "" {
		return fmt.Errorf("GITHUB_TOKEN or GH_TOKEN required — in GitHub Actions pass GITHUB_TOKEN: ${{ github.token }} in the step's env (the go-toolchain action does this automatically)")
	}

	repo := os.Getenv("GITHUB_REPOSITORY")
	if repo == "" {
		return fmt.Errorf("GITHUB_REPOSITORY required")
	}

	body, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("failed to marshal snapshot: %w", err)
	}

	url := fmt.Sprintf("%s/repos/%s/dependency-graph/snapshots", githubAPIBase, repo)
	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Authorization", "token "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to submit snapshot: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(resp.Body)
		if resp.StatusCode == http.StatusForbidden {
			return fmt.Errorf("dependency submission failed (HTTP %d): %s — the workflow token lacks contents: write; add \"permissions: contents: write\" to the calling workflow job", resp.StatusCode, string(respBody))
		}
		return fmt.Errorf("dependency submission failed (HTTP %d): %s", resp.StatusCode, string(respBody))
	}

	total := 0
	for _, m := range snapshot.Manifests {
		total += len(m.Resolved)
	}
	logger.Info("=> Submitted %d dependencies to GitHub Dependency Graph", total)
	return nil
}

// buildingRepoWorkspace reports whether the module being built is the checked-out
// repository's own -- that is, whether the working directory is inside
// GITHUB_WORKSPACE.
//
// A snapshot describes GITHUB_REPOSITORY at GITHUB_SHA, so it is only meaningful
// for that repository's own module. A pipeline driven inside a synthetic
// throwaway module elsewhere on the runner (this repo's smoke jobs build one
// under RUNNER_TEMP) would otherwise publish the fixture's dependencies as the
// repository's dependency graph -- worse than publishing nothing, since it is
// wrong data in the surface Dependabot and vulnerability alerts read.
//
// This is deliberately a property of what is being built rather than a switch.
// There is no opt-out env var: submission is part of building in CI, and any
// knob that turned it off would eventually be set and left set, leaving a
// repository silently absent from vulnerability scanning while its builds
// stayed green.
func buildingRepoWorkspace() bool {
	workspace := os.Getenv("GITHUB_WORKSPACE")
	if workspace == "" {
		return false
	}
	absWorkspace, err := filepath.Abs(workspace)
	if err != nil {
		return false
	}
	cwd, err := os.Getwd()
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(absWorkspace, cwd)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// maybeSubmitDeps submits a dependency snapshot when running in GitHub Actions
// against the repository's own checkout. A failed snapshot or submission fails
// the build.
func maybeSubmitDeps() error {
	if os.Getenv("CI") == "" || os.Getenv("GITHUB_REPOSITORY") == "" || os.Getenv("GITHUB_SHA") == "" {
		return nil
	}
	if !buildingRepoWorkspace() {
		logger.Debug("=> Dependency submission skipped: not building the repository's own workspace")
		return nil
	}

	snapshot, err := buildDepSnapshot()
	if err != nil {
		return fmt.Errorf("dependency snapshot failed: %w", err)
	}
	return postDepSnapshot(snapshot)
}
