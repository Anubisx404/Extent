package templates

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"extent/internal/planner"
)

func TestWriteLGTMWritesV1FileSet(t *testing.T) {
	root := t.TempDir()
	plan := planner.Plan{Root: root, Detected: []string{"runtime:node"}}

	written, err := WriteLGTM(root, plan, WriteOptions{})
	if err != nil {
		t.Fatal(err)
	}

	want := []string{
		"docker-compose.observability.yml",
		"otel-collector.yml",
		"tempo.yml",
		"loki.yml",
		"prometheus.yml",
		"prometheus-alerts.yml",
		"extent.yaml",
		".env.observability",
		"grafana/provisioning/datasources/datasources.yml",
		"grafana/provisioning/dashboards/dashboards.yml",
		"grafana/dashboards/service-overview.json",
		".extent/report.md",
		".extent/plan.json",
	}
	for _, rel := range want {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err != nil {
			t.Fatalf("expected %s to exist: %v", rel, err)
		}
	}
	if len(written) != len(want) {
		t.Fatalf("expected %d written files, got %d: %#v", len(want), len(written), written)
	}
}

func TestWriteLGTMIncludesInfrastructureExportersAndDashboards(t *testing.T) {
	root := t.TempDir()
	_, err := WriteLGTM(root, planner.Plan{Root: root}, WriteOptions{})
	if err != nil {
		t.Fatal(err)
	}

	assertFileContains(t, filepath.Join(root, "docker-compose.observability.yml"), "cadvisor:")
	assertFileContains(t, filepath.Join(root, "docker-compose.observability.yml"), "node-exporter:")
	assertFileContains(t, filepath.Join(root, "prometheus.yml"), "cadvisor:8080")
	assertFileContains(t, filepath.Join(root, "prometheus.yml"), "node-exporter:9100")
	assertFileContains(t, filepath.Join(root, "grafana/dashboards/service-overview.json"), "Container CPU")
	assertFileContains(t, filepath.Join(root, "grafana/dashboards/service-overview.json"), "Host CPU")
}

func TestWriteLGTMRefusesOverwrite(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "otel-collector.yml")
	if err := os.WriteFile(path, []byte("existing"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := WriteLGTM(root, planner.Plan{Root: root}, WriteOptions{})
	if err == nil {
		t.Fatal("expected overwrite refusal")
	}
	if _, statErr := os.Stat(filepath.Join(root, ".extent", "plan.json")); !os.IsNotExist(statErr) {
		t.Fatalf("expected preflight to avoid partial writes, got stat error %v", statErr)
	}
}

func assertFileContains(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), want) {
		t.Fatalf("expected %s to contain %q, got:\n%s", path, want, string(data))
	}
}
