package bench

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/wow-look-at-my/go-toolchain/src/runner"
)

// NotesRef is the git notes reference for benchmark data
const NotesRef = "refs/notes/benchmarks"

// StoreNotes stores benchmark results in git notes for HEAD
func StoreNotes(r runner.CommandRunner, report *BenchmarkReport) error {
	data, err := json.Marshal(report)
	if err != nil {
		return fmt.Errorf("failed to marshal report: %w", err)
	}

	// Use git notes add -f to overwrite any existing note
	proc, err := runner.Cmd("git", "notes", "--ref=benchmarks", "add", "-f", "-m", string(data), "HEAD").
		WithQuiet().
		Run(r)
	if err != nil {
		return fmt.Errorf("failed to store git notes: %w", err)
	}
	if err := proc.Wait(); err != nil {
		return fmt.Errorf("failed to store git notes: %w", err)
	}

	return nil
}

// FetchPrevious retrieves the most recent stored benchmark results
// Returns the report, commit SHA, and any error
func FetchPrevious(r runner.CommandRunner) (*BenchmarkReport, string, error) {
	// Get commits that have benchmark notes, most recent first
	proc, err := runner.Cmd("git", "log", "--format=%H", "--notes=benchmarks", "--grep=", "-1").
		WithQuiet().
		Run(r)
	if err != nil {
		return nil, "", nil // no previous results
	}
	output, _ := io.ReadAll(proc.Stdout())
	if proc.Wait() != nil {
		return nil, "", nil // no previous results
	}

	sha := strings.TrimSpace(string(output))
	if sha == "" {
		return nil, "", nil // no previous results
	}

	// Check if this commit actually has notes (log output includes commits without notes)
	proc, err = runner.Cmd("git", "notes", "--ref=benchmarks", "show", sha).
		WithQuiet().
		Run(r)
	if err != nil {
		return nil, "", nil // no notes for this commit
	}
	noteOutput, _ := io.ReadAll(proc.Stdout())
	if proc.Wait() != nil {
		return nil, "", nil // no notes for this commit
	}

	report, err := parseNotesJSON(noteOutput)
	if err != nil {
		return nil, sha, fmt.Errorf("failed to parse benchmark notes: %w", err)
	}

	return report, sha, nil
}

// FetchForCommit retrieves stored benchmark results for a specific commit
func FetchForCommit(r runner.CommandRunner, sha string) (*BenchmarkReport, error) {
	proc, err := runner.Cmd("git", "notes", "--ref=benchmarks", "show", sha).
		WithQuiet().
		Run(r)
	if err != nil {
		return nil, fmt.Errorf("no benchmark data for commit %s", sha)
	}
	output, _ := io.ReadAll(proc.Stdout())
	if proc.Wait() != nil {
		return nil, fmt.Errorf("no benchmark data for commit %s", sha)
	}

	return parseNotesJSON(output)
}

// GetHeadSHA returns the current HEAD commit SHA
func GetHeadSHA(r runner.CommandRunner) (string, error) {
	proc, err := runner.Cmd("git", "rev-parse", "--short", "HEAD").
		WithQuiet().
		Run(r)
	if err != nil {
		return "", fmt.Errorf("failed to get HEAD SHA: %w", err)
	}
	output, _ := io.ReadAll(proc.Stdout())
	if proc.Wait() != nil {
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
