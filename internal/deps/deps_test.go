package deps

import (
	"context"
	"strings"
	"testing"

	"github.com/Anubisx404/Extent/internal/process"
)

func TestInstallCommandForPackageManagers(t *testing.T) {
	tests := []struct {
		name     string
		managers []string
		want     []string
	}{
		{name: "npm", managers: []string{"npm"}, want: []string{"npm", "install"}},
		{name: "pnpm", managers: []string{"pnpm"}, want: []string{"pnpm", "install"}},
		{name: "yarn", managers: []string{"yarn"}, want: []string{"yarn", "install"}},
		{name: "pip", managers: []string{"pip"}, want: []string{"python", "-m", "pip", "install", "-r", "requirements.txt"}},
		{name: "go", managers: []string{"go-modules"}, want: []string{"go", "get", "go.opentelemetry.io/otel", "go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp", "go.opentelemetry.io/otel/sdk"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := InstallCommand(tt.managers)
			if !ok {
				t.Fatal("expected install command")
			}
			if len(got) != len(tt.want) {
				t.Fatalf("expected %#v, got %#v", tt.want, got)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("expected %#v, got %#v", tt.want, got)
				}
			}
		})
	}
}

func TestInstallCommandForGoAddsOpenTelemetryPackages(t *testing.T) {
	command, ok := InstallCommand([]string{"go-modules"})
	if !ok {
		t.Fatal("expected go install command")
	}
	joined := strings.Join(command, " ")
	if !strings.Contains(joined, "go get") || !strings.Contains(joined, "go.opentelemetry.io/otel") {
		t.Fatalf("expected go get OpenTelemetry command, got %q", joined)
	}
}

type fakeRunner struct {
	result process.Result
	name   string
	args   []string
}

func (runner *fakeRunner) Run(_ context.Context, name string, args ...string) process.Result {
	runner.name = name
	runner.args = append([]string(nil), args...)
	return runner.result
}

func TestSelectCommandRejectsAmbiguousManagers(t *testing.T) {
	if _, err := selectCommand([]string{"npm", "pnpm"}); err == nil {
		t.Fatal("accepted ambiguous dependency managers")
	}
	if command, ok := InstallCommand([]string{"npm", "pnpm"}); ok || command != nil {
		t.Fatalf("ambiguous command = %#v, %v", command, ok)
	}
}

func TestInstallContextUsesDirectArgvAndPropagatesFailure(t *testing.T) {
	runner := &fakeRunner{result: process.Result{ExitCode: 0}}
	command, err := InstallContext(context.Background(), runner, t.TempDir(), []string{"npm"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(command, " ") != "npm install" || runner.name != "npm" || strings.Join(runner.args, " ") != "install" {
		t.Fatalf("command=%#v runner=%s %#v", command, runner.name, runner.args)
	}

	runner.result = process.Result{ExitCode: 9, Err: context.DeadlineExceeded, TimedOut: true}
	if _, err := InstallContext(context.Background(), runner, t.TempDir(), []string{"npm"}); err == nil {
		t.Fatal("dependency process failure was ignored")
	}
}
