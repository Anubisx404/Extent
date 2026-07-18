package codemods

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Anubisx404/Extent/internal/process"
)

type scriptedCommandRunner struct {
	results map[string]process.Result
	calls   [][]string
}

func (runner *scriptedCommandRunner) Run(_ context.Context, name string, args ...string) process.Result {
	call := append([]string{name}, args...)
	runner.calls = append(runner.calls, call)
	if result, ok := runner.results[name]; ok {
		return result
	}
	return process.Result{ExitCode: 0}
}

func TestRunContextExecutesPlannedCommandsAsDirectArgv(t *testing.T) {
	root := languageFixture(t)
	runner := &scriptedCommandRunner{results: map[string]process.Result{}}
	result, err := RunContext(context.Background(), root, runner)
	if err != nil {
		t.Fatal(err)
	}
	wantRuntimes := []string{"node", "python", "dotnet", "java"}
	gotRuntimes := make([]string, 0, len(result.Commands))
	for _, command := range result.Commands {
		gotRuntimes = append(gotRuntimes, command.Runtime)
	}
	if !reflect.DeepEqual(gotRuntimes, wantRuntimes) {
		t.Fatalf("runtime order = %#v", gotRuntimes)
	}
	if len(runner.calls) != len(wantRuntimes) {
		t.Fatalf("calls = %#v", runner.calls)
	}
	if !result.Experimental || result.Warning == "" {
		t.Fatalf("missing experimental safety warning: %#v", result)
	}
	for i, runtime := range wantRuntimes {
		if runner.calls[i][0] != runtime {
			t.Fatalf("call %d = %#v", i, runner.calls[i])
		}
	}
}

func TestRunContextStopsAndReturnsStructuredFailure(t *testing.T) {
	root := languageFixture(t)
	runner := &scriptedCommandRunner{results: map[string]process.Result{
		"python": {ExitCode: 9, Err: errors.New("failed"), Stderr: "private detail"},
	}}
	result, err := RunContext(context.Background(), root, runner)
	if err == nil {
		t.Fatal("runner failure was ignored")
	}
	var failure *Failure
	if !errors.As(err, &failure) {
		t.Fatalf("error type = %T", err)
	}
	if failure.Runtime != "python" || failure.Result.ExitCode != 9 || len(runner.calls) != 2 {
		t.Fatalf("failure=%#v calls=%#v", failure, runner.calls)
	}
	if len(result.Commands) != 4 {
		t.Fatalf("planned commands lost on failure: %#v", result.Commands)
	}
}

func TestRunContextReportsTimeout(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "package.json"))
	runner := &scriptedCommandRunner{results: map[string]process.Result{
		"node": {ExitCode: -1, Err: context.DeadlineExceeded, TimedOut: true},
	}}
	_, err := RunContext(context.Background(), root, runner)
	var failure *Failure
	if !errors.As(err, &failure) || !failure.Result.TimedOut {
		t.Fatalf("timeout error = %#v", err)
	}
}

func languageFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, path := range []string{"package.json", "requirements.txt", "service.csproj", "pom.xml"} {
		mustWrite(t, filepath.Join(root, path))
	}
	return root
}

func mustWrite(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("fixture\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}
