package bench

import (
	"encoding/json"
	"fmt"
	"strings"
)

// NotesRef is the git notes reference for benchmark data
const NotesRef = "refs/notes/benchmarks"

// FullCommandRunner extends CommandRunner with Run method
type FullCommandRunner interface {
	CommandRunner
	Run(name string, args ...string) error
}

// StoreNotes stores benchmark results in git notes for HEAD
func StoreNotes(runner FullCommandRunner, report *BenchmarkReport) error {
	data, err := json.Marshal(report)
	if err != nil {
		return fmt.Errorf("failed to marshal report: %w", err)
	}

	// Use git notes add -f to overwrite any existing note
	err = runner.Run("git", "notes", "--ref=benchmarks", "add", "-f", "-m", string(data), "HEAD")
	if err != nil {
		return fmt.Errorf("failed to store git notes: %w", err)
	}

	return nil
}

// FetchPrevious retrieves the most recent stored benchmark results
// Returns the report, commit SHA, and any error
func FetchPrevious(runner CommandRunner) (*BenchmarkReport, string, error) {
	// Get commits that have benchmark notes, most recent first
	output, err := runner.RunWithOutput("git", "log", "--format=%H", "--notes=benchmarks", "--grep=", "-1")
	if err != nil {
		return nil, "", nil // no previous results
	}

	sha := strings.TrimSpace(string(output))
	if sha == "" {
		return nil, "", nil // no previous results
	}

	// Check if this commit actually has notes (log output includes commits without notes)
	noteOutput, err := runner.RunWithOutput("git", "notes", "--ref=benchmarks", "show", sha)
	if err != nil {
		return nil, "", nil // no notes for this commit
	}

	report, err := parseNotesJSON(noteOutput)
	if err != nil {
		return nil, sha, fmt.Errorf("failed to parse benchmark notes: %w", err)
	}

	return report, sha, nil
}

// FetchForCommit retrieves stored benchmark results for a specific commit
func FetchForCommit(runner CommandRunner, sha string) (*BenchmarkReport, error) {
	output, err := runner.RunWithOutput("git", "notes", "--ref=benchmarks", "show", sha)
	if err != nil {
		return nil, fmt.Errorf("no benchmark data for commit %s", sha)
	}

	return parseNotesJSON(output)
}

// GetHeadSHA returns the current HEAD commit SHA
func GetHeadSHA(runner CommandRunner) (string, error) {
	output, err := runner.RunWithOutput("git", "rev-parse", "--short", "HEAD")
	if err != nil {
		return "", fmt.Errorf("failed to get HEAD SHA: %w", err)
	}
	return strings.TrimSpace(string(output)), nil
}

func parseNotesJSON(data []byte) (*BenchmarkReport, error) {
	var report BenchmarkReport
	if err := json.Unmarshal(data, &report); err != nil {
		return nil, err
	}
	return &report, nil
}
