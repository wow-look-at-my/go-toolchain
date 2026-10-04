package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// runLockStoreEnv names the buildhost server that holds the run locks, as the fork's go command reads it.
const runLockStoreEnv = "GOSMOPOLITAN_RUN_LOCK_STORE"

// defaultRunLockStore is the store the fork's go command uses when runLockStoreEnv is unset.
const defaultRunLockStore = "https://pazer.build"

// runLockBody is the JSON the store reads and answers with.
type runLockBody struct {
	Repository string `json:"repository,omitempty"`
	RunID      string `json:"run_id,omitempty"`
	RunAttempt string `json:"run_attempt,omitempty"`
	Name       string `json:"name,omitempty"`
	Value      string `json:"value,omitempty"`
	Found      bool   `json:"found,omitempty"`
}

// runLockClient is a test seam.
var runLockClient = &http.Client{Timeout: time.Minute}

// lockedRunValue answers what the run lock holds for name in this run attempt.
// When it holds nothing, it claims head and answers what the claim leaves,
// which a racing job can have set first.
func lockedRunValue(name, head string) (string, error) {
	store := strings.TrimSuffix(os.Getenv(runLockStoreEnv), "/")
	if store == "" {
		store = defaultRunLockStore
	}
	fail := func(err error) error { return fmt.Errorf("%s: run lock store %s: %w", name, store, err) }
	token, err := runLockToken(store)
	if err != nil {
		return "", fail(err)
	}
	body := runLockBody{
		Repository: os.Getenv("GITHUB_REPOSITORY"),
		RunID:      os.Getenv("GITHUB_RUN_ID"),
		RunAttempt: os.Getenv("GITHUB_RUN_ATTEMPT"),
		Name:       name,
	}
	query := url.Values{"repository": {body.Repository}, "run_id": {body.RunID}, "run_attempt": {body.RunAttempt}, "name": {name}}
	var answer runLockBody
	if err := runLockDo(http.MethodGet, store+"/api/v1/run-locks?"+query.Encode(), token, nil, &answer); err != nil {
		return "", fail(err)
	}
	if !answer.Found {
		if head == "" {
			return "", fail(fmt.Errorf("no head to lock"))
		}
		body.Value = head
		payload, err := json.Marshal(body)
		if err != nil {
			return "", fail(err)
		}
		if err := runLockDo(http.MethodPost, store+"/api/v1/run-locks", token, payload, &answer); err != nil {
			return "", fail(err)
		}
	}
	if answer.Value == "" {
		return "", fail(fmt.Errorf("the store holds an empty value"))
	}
	return answer.Value, nil
}

// runLockToken asks GitHub Actions for an OIDC token whose audience is the store.
func runLockToken(store string) (string, error) {
	tokenURL := os.Getenv("ACTIONS_ID_TOKEN_REQUEST_URL")
	if tokenURL == "" || os.Getenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN") == "" {
		return "", fmt.Errorf("the job has no GitHub Actions OIDC token; grant the workflow id-token: write")
	}
	sep := "?"
	if strings.Contains(tokenURL, "?") {
		sep = "&"
	}
	var answer struct {
		Value string `json:"value"`
	}
	if err := runLockDo(http.MethodGet, tokenURL+sep+"audience="+url.QueryEscape(store), os.Getenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN"), nil, &answer); err != nil {
		return "", fmt.Errorf("requesting an OIDC token: %w", err)
	}
	if answer.Value == "" {
		return "", fmt.Errorf("requesting an OIDC token: the answer holds no token")
	}
	return answer.Value, nil
}

// runLockDo sends one request and decodes a JSON answer.
func runLockDo(method, target, token string, body []byte, answer any) error {
	req, err := http.NewRequest(method, target, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := runLockClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(data)))
	}
	return json.Unmarshal(data, answer)
}
