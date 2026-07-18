package gitops

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Anubisx404/Extent/internal/process"
)

type scriptedRunner struct {
	results map[string]process.Result
	calls   []string
}

func (runner *scriptedRunner) Run(_ context.Context, name string, args ...string) process.Result {
	call := strings.Join(append([]string{name}, args...), " ")
	runner.calls = append(runner.calls, call)
	if result, ok := runner.results[call]; ok {
		return result
	}
	return process.Result{ExitCode: 0}
}

func TestEnsureBranchContextValidatesAndCreatesBranch(t *testing.T) {
	runner := &scriptedRunner{results: map[string]process.Result{
		"git rev-parse --verify refs/heads/v1": {ExitCode: 1, Err: errors.New("missing")},
	}}
	if err := EnsureBranchContext(context.Background(), runner, t.TempDir(), "v1"); err != nil {
		t.Fatal(err)
	}
	calls := strings.Join(runner.calls, "\n")
	for _, expected := range []string{
		"git rev-parse --is-inside-work-tree",
		"git check-ref-format --branch v1",
		"git status --porcelain",
		"git rev-parse --verify refs/heads/v1",
		"git checkout -b v1",
	} {
		if !strings.Contains(calls, expected) {
			t.Fatalf("missing call %q in:\n%s", expected, calls)
		}
	}
}

func TestEnsureBranchContextRejectsInvalidOrDirtyRepositories(t *testing.T) {
	invalid := &scriptedRunner{results: map[string]process.Result{
		"git check-ref-format --branch bad name": {ExitCode: 1, Err: errors.New("invalid")},
	}}
	if err := EnsureBranchContext(context.Background(), invalid, t.TempDir(), "bad name"); err == nil {
		t.Fatal("accepted invalid branch")
	}
	if strings.Contains(strings.Join(invalid.calls, "\n"), "checkout") {
		t.Fatal("invalid branch reached checkout")
	}

	dirty := &scriptedRunner{results: map[string]process.Result{
		"git status --porcelain": {ExitCode: 0, Stdout: " M main.go\n"},
	}}
	if err := EnsureBranchContext(context.Background(), dirty, t.TempDir(), "v1"); err == nil {
		t.Fatal("accepted dirty worktree")
	}
	if strings.Contains(strings.Join(dirty.calls, "\n"), "checkout") {
		t.Fatal("dirty worktree reached checkout")
	}
}

func TestEnsureBranchContextChecksOutExistingBranch(t *testing.T) {
	runner := &scriptedRunner{results: map[string]process.Result{}}
	if err := EnsureBranchContext(context.Background(), runner, t.TempDir(), "existing"); err != nil {
		t.Fatal(err)
	}
	calls := strings.Join(runner.calls, "\n")
	if !strings.Contains(calls, "git checkout existing") || strings.Contains(calls, "git checkout -b") {
		t.Fatalf("unexpected calls:\n%s", calls)
	}
}

func TestEnsureBranchContextDoesNotTreatLookupFailureAsMissingBranch(t *testing.T) {
	runner := &scriptedRunner{results: map[string]process.Result{
		"git rev-parse --verify refs/heads/v1": {ExitCode: -1, Err: context.DeadlineExceeded, TimedOut: true},
	}}
	if err := EnsureBranchContext(context.Background(), runner, t.TempDir(), "v1"); err == nil {
		t.Fatal("branch lookup failure was ignored")
	}
	if strings.Contains(strings.Join(runner.calls, "\n"), "checkout") {
		t.Fatal("branch lookup failure reached checkout")
	}
}
