package stack

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
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

func TestStatusContextPreflightsAndUsesDirectComposeArgv(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, composeFile), []byte("services: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runner := &scriptedRunner{results: map[string]process.Result{}}
	if err := StatusContext(context.Background(), runner, root); err != nil {
		t.Fatal(err)
	}
	calls := strings.Join(runner.calls, "\n")
	for _, expected := range []string{
		"docker --version",
		"docker compose version",
		"docker info --format {{json .ServerVersion}}",
		"docker compose -f docker-compose.observability.yml -p " + projectName(root) + " ps --format json",
	} {
		if !strings.Contains(calls, expected) {
			t.Fatalf("missing %q in:\n%s", expected, calls)
		}
	}
}

func TestInspectParsesArrayAndLineDelimitedComposeJSON(t *testing.T) {
	for name, output := range map[string]string{
		"array": `[{"Name":"extent-api-1","Service":"api","State":"running","Health":"healthy","ExitCode":0}]`,
		"lines": "{\"Name\":\"extent-api-1\",\"Service\":\"api\",\"State\":\"running\"}\n{\"Name\":\"extent-db-1\",\"Service\":\"db\",\"State\":\"exited\",\"ExitCode\":1}\n",
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, composeFile), []byte("services: {}\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			command := "docker compose -f docker-compose.observability.yml -p " + projectName(root) + " ps --format json"
			runner := &scriptedRunner{results: map[string]process.Result{command: {Stdout: output, ExitCode: 0}}}
			report, err := InspectContext(context.Background(), runner, root)
			if err != nil {
				t.Fatal(err)
			}
			if len(report.Components) == 0 || report.Components[0].Service != "api" || report.Project != projectName(root) {
				t.Fatalf("status report = %#v", report)
			}
		})
	}
	if _, err := parseStatus("not-json"); err == nil {
		t.Fatal("malformed Compose status accepted")
	}
}

func TestStackContextRejectsMissingComposeAndDocker(t *testing.T) {
	missingRunner := &scriptedRunner{results: map[string]process.Result{}}
	if err := UpContext(context.Background(), missingRunner, t.TempDir()); err == nil {
		t.Fatal("accepted missing compose file")
	}
	if len(missingRunner.calls) != 0 {
		t.Fatalf("ran Docker before compose preflight: %#v", missingRunner.calls)
	}

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, composeFile), []byte("services: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dockerMissing := &scriptedRunner{results: map[string]process.Result{
		"docker --version": {ExitCode: -1, Err: errors.New("missing")},
	}}
	if err := UpContext(context.Background(), dockerMissing, root); err == nil {
		t.Fatal("accepted unavailable Docker")
	}
	if len(dockerMissing.calls) != 1 {
		t.Fatalf("unexpected calls: %#v", dockerMissing.calls)
	}
}

func TestStackContextPropagatesComposeFailure(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, composeFile), []byte("services: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runner := &scriptedRunner{results: map[string]process.Result{
		"docker compose -f docker-compose.observability.yml -p " + projectName(root) + " up -d --force-recreate --wait --wait-timeout 120": {ExitCode: 1, Err: errors.New("failed")},
	}}
	if err := UpContext(context.Background(), runner, root); err == nil {
		t.Fatal("compose failure was ignored")
	}
}

func TestStackRejectsUnavailableDaemonAndInvalidProjectName(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, composeFile), []byte("services: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	daemonMissing := &scriptedRunner{results: map[string]process.Result{
		"docker info --format {{json .ServerVersion}}": {ExitCode: 1, Err: errors.New("daemon stopped")},
	}}
	if err := UpContext(context.Background(), daemonMissing, root); err == nil || !strings.Contains(err.Error(), "daemon unavailable") {
		t.Fatalf("daemon error = %v", err)
	}
	if err := UpWithOptions(context.Background(), &scriptedRunner{results: map[string]process.Result{}}, root, UpOptions{ProjectName: "INVALID NAME"}); err == nil {
		t.Fatal("invalid Compose project name accepted")
	}
}

func TestUpRejectsHostPortConflictBeforeStartingCompose(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	root := t.TempDir()
	compose := fmt.Sprintf("services:\n  app:\n    image: example.invalid/app:1\n    ports:\n      - \"127.0.0.1:%d:8080\"\n", port)
	if err := os.WriteFile(filepath.Join(root, composeFile), []byte(compose), 0o644); err != nil {
		t.Fatal(err)
	}
	runner := &scriptedRunner{results: map[string]process.Result{}}
	err = UpWithOptions(context.Background(), runner, root, UpOptions{Wait: true})
	if err == nil || !strings.Contains(err.Error(), "port") {
		t.Fatalf("port conflict error = %v", err)
	}
	for _, call := range runner.calls {
		if strings.Contains(call, " up -d") {
			t.Fatalf("Compose started despite port conflict: %s", call)
		}
	}
}

func TestStackContextRejectsSymlinkedComposeFile(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(t.TempDir(), "compose.yml")
	if err := os.WriteFile(target, []byte("services: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, composeFile)); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	runner := &scriptedRunner{results: map[string]process.Result{}}
	if err := UpContext(context.Background(), runner, root); err == nil {
		t.Fatal("accepted symlinked compose file")
	}
	if len(runner.calls) != 0 {
		t.Fatalf("ran Docker for unsafe compose file: %#v", runner.calls)
	}
}
