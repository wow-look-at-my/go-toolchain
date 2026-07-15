package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
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
		return fmt.Errorf("GITHUB_TOKEN or GH_TOKEN required")
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
		return fmt.Errorf("dependency submission failed (HTTP %d): %s", resp.StatusCode, string(respBody))
	}

	total := 0
	for _, m := range snapshot.Manifests {
		total += len(m.Resolved)
	}
	logger.Info("=> Submitted %d dependencies to GitHub Dependency Graph", total)
	return nil
}

// maybeSubmitDeps submits a dependency snapshot when running in GitHub Actions.
// Errors are logged but don't fail the build.
func maybeSubmitDeps() {
	if os.Getenv("CI") == "" || os.Getenv("GITHUB_REPOSITORY") == "" || os.Getenv("GITHUB_SHA") == "" {
		return
	}

	snapshot, err := buildDepSnapshot()
	if err != nil {
		logger.Warn("=> Warning: dependency snapshot failed: %v", err)
		return
	}
	if err := postDepSnapshot(snapshot); err != nil {
		logger.Warn("=> Warning: dependency submission failed: %v", err)
	}
}
