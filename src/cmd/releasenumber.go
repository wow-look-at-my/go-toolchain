package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"

	"github.com/wow-look-at-my/go-containers/set"
	"github.com/wow-look-at-my/go-toolchain/src/logger"
)

// ReleaseEnv names the buildhost release number for a build that already has one.
const ReleaseEnv = "GO_TOOLCHAIN_RELEASE_VERSION"

// releaseVarNames are the main-package variables a build fills with the buildhost release number.
var releaseVarNames = []string{"releaseNumber", "ReleaseNumber"}

// releaseRunLock is the run lock that makes every job of one run attempt stamp the same number.
const releaseRunLock = "buildhost-release"

var (
	releaseOnce  sync.Once
	releaseValue string
	releaseErr   error
)

// resolveRelease answers the buildhost release number this run builds, once per process.
// Outside CI with no ReleaseEnv there is no release, and it answers "".
func resolveRelease() (string, error) {
	releaseOnce.Do(func() {
		releaseValue, releaseErr = lookupRelease()
		if releaseErr == nil && releaseValue != "" {
			writeReleaseOutput(releaseValue)
		}
	})
	return releaseValue, releaseErr
}

func lookupRelease() (string, error) {
	if v := strings.TrimSpace(os.Getenv(ReleaseEnv)); v != "" {
		return usableRelease(v)
	}
	if os.Getenv("GITHUB_ACTIONS") != "true" {
		return "", nil
	}
	return createRunRelease()
}

// usableRelease rejects what -ldflags cannot carry, the same rule as a revision.
func usableRelease(v string) (string, error) {
	if usableRevision(v) != v {
		return "", fmt.Errorf("release number %q holds whitespace or a quote, which -ldflags cannot carry", v)
	}
	return v, nil
}

// createRunRelease creates the buildhost release this run publishes, or answers
// the one another job of the same run attempt already created.
func createRunRelease() (string, error) {
	store := strings.TrimSuffix(os.Getenv(runLockStoreEnv), "/")
	if store == "" {
		store = defaultRunLockStore
	}
	fail := func(err error) (string, error) { return "", fmt.Errorf("buildhost release: %s: %w", store, err) }
	token, err := runLockToken(store)
	if err != nil {
		return fail(err)
	}
	lock := runLockBody{
		Repository: os.Getenv("GITHUB_REPOSITORY"),
		RunID:      os.Getenv("GITHUB_RUN_ID"),
		RunAttempt: os.Getenv("GITHUB_RUN_ATTEMPT"),
		Name:       releaseRunLock,
	}
	query := url.Values{"repository": {lock.Repository}, "run_id": {lock.RunID}, "run_attempt": {lock.RunAttempt}, "name": {releaseRunLock}}
	var held runLockBody
	if err := runLockDo(http.MethodGet, store+"/api/v1/run-locks?"+query.Encode(), token, nil, &held); err != nil {
		return fail(err)
	}
	if held.Found && held.Value != "" {
		return usableRelease(held.Value)
	}
	project := lock.Repository[strings.LastIndex(lock.Repository, "/")+1:]
	if project == "" {
		return fail(fmt.Errorf("GITHUB_REPOSITORY is unset, so the build cannot name its project"))
	}
	body, err := json.Marshal(map[string]string{
		"git_branch": os.Getenv("GITHUB_REF_NAME"),
		"git_commit": os.Getenv("GITHUB_SHA"),
	})
	if err != nil {
		return fail(err)
	}
	var created struct {
		Version string `json:"version"`
	}
	if err := releaseDo(store+"/api/v1/projects/"+url.PathEscape(project)+"/releases", token, body, &created); err != nil {
		return fail(fmt.Errorf("creating the release of %s: %w", project, err))
	}
	if created.Version == "" {
		return fail(fmt.Errorf("creating the release of %s: the answer names no version", project))
	}
	lock.Value = created.Version
	claim, err := json.Marshal(lock)
	if err != nil {
		return fail(err)
	}
	var won runLockBody
	if err := runLockDo(http.MethodPost, store+"/api/v1/run-locks", token, claim, &won); err != nil {
		return fail(err)
	}
	if won.Value == "" {
		return fail(fmt.Errorf("the run lock holds an empty release number"))
	}
	return usableRelease(won.Value)
}

// releaseDo posts a create-release request.
func releaseDo(target, token string, body []byte, answer any) error {
	req, err := http.NewRequest(http.MethodPost, target, strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := runLockClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var raw json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return fmt.Errorf("%s: %w", resp.Status, err)
	}
	if resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(raw)))
	}
	return json.Unmarshal(raw, answer)
}

// writeReleaseOutput hands the number to the action's publish, which releases under the same number.
func writeReleaseOutput(v string) {
	path := os.Getenv("GITHUB_OUTPUT")
	if path == "" {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		logger.Warn("⇒ Warning: cannot record release %s in GITHUB_OUTPUT: %v", v, err)
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "release_version=%s\n", v)
}

// releaseStamp writes the -X flags for the release variables declared holds.
// A package that declares one gets the number or the build fails, except
// outside CI, where no release exists and the variable keeps its placeholder.
func releaseStamp(importPath string, declared set.Set[string], resolve func() (string, error)) (string, error) {
	var names []string
	for _, name := range releaseVarNames {
		if declared.Contains(name) {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return "", nil
	}
	rel, err := resolve()
	if err != nil {
		return "", fmt.Errorf("%s declares %s: %w", importPath, strings.Join(names, ", "), err)
	}
	if rel == "" {
		logger.Warn("⇒ Warning: %s declares %s, and a build outside CI has no buildhost release, so the binary carries no release number; set %s to give it one",
			importPath, strings.Join(names, ", "), ReleaseEnv)
		return "", nil
	}
	flags := make([]string, len(names))
	for i, name := range names {
		flags[i] = "-X " + importPath + "." + name + "=" + rel
	}
	return strings.Join(flags, " "), nil
}
