package planner

import (
	"testing"

	"extent/internal/scanner"
)

func TestBuildIncludesCoreObservabilityChanges(t *testing.T) {
	plan := Build(scanner.Result{
		Root:         "repo",
		Runtimes:     []string{"node"},
		Frameworks:   []string{"express"},
		ComposeFiles: []string{"docker-compose.yml"},
	})

	assertChange(t, plan, "docker-compose.observability.yml")
	assertChange(t, plan, "otel-collector.yml")
	assertChange(t, plan, "tempo.yml")
	assertChange(t, plan, "loki.yml")
	assertChange(t, plan, "prometheus.yml")
	assertChange(t, plan, ".env.observability")
	if len(plan.Warnings) != 0 {
		t.Fatalf("expected no warnings, got %#v", plan.Warnings)
	}
}

func assertChange(t *testing.T, plan Plan, path string) {
	t.Helper()
	for _, change := range plan.Changes {
		if change.Path == path {
			return
		}
	}
	t.Fatalf("expected change for %q in %#v", path, plan.Changes)
}
