package deps

import (
	"strings"
	"testing"
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
