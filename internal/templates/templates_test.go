package templates

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Anubisx404/Extent/internal/fileops"
	"github.com/Anubisx404/Extent/internal/planner"
	"github.com/Anubisx404/Extent/internal/state"
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

func TestWriteLGTMDefaultIsPortablePersistentAndLoopbackOnly(t *testing.T) {
	root := t.TempDir()
	_, err := WriteLGTM(root, planner.Plan{Root: root}, WriteOptions{})
	if err != nil {
		t.Fatal(err)
	}

	compose := mustRead(t, filepath.Join(root, "docker-compose.observability.yml"))
	for _, forbidden := range []string{"cadvisor:", "node-exporter:", "privileged: true", `- "/:`} {
		if strings.Contains(compose, forbidden) {
			t.Fatalf("portable default contains %q:\n%s", forbidden, compose)
		}
	}
	for _, required := range []string{"127.0.0.1:4317:4317", "127.0.0.1:3000:3000", "grafana-data:/var/lib/grafana", "./grafana/dashboards:/var/lib/grafana/dashboards:ro", "healthcheck:", `user: "0:0"`} {
		if !strings.Contains(compose, required) {
			t.Fatalf("portable default missing %q:\n%s", required, compose)
		}
	}
	prometheus := mustRead(t, filepath.Join(root, "prometheus.yml"))
	if strings.Contains(prometheus, "cadvisor:8080") || strings.Contains(prometheus, "node-exporter:9100") {
		t.Fatalf("portable Prometheus config contains host targets:\n%s", prometheus)
	}
	for _, required := range []string{`targets: ["otel-collector:8888"]`, `targets: ["otel-collector:9464"]`} {
		if !strings.Contains(prometheus, required) {
			t.Fatalf("Prometheus config missing %q:\n%s", required, prometheus)
		}
	}
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

func TestPlanLGTMValidatesProfilesWithoutMutation(t *testing.T) {
	root := t.TempDir()
	if _, err := PlanLGTM(root, planner.Plan{Root: root}, WriteOptions{Profile: "full"}); err != nil {
		t.Fatalf("full profile rejected: %v", err)
	}
	if _, err := PlanLGTM(root, planner.Plan{Root: root}, WriteOptions{Profile: "invalid"}); err == nil {
		t.Fatal("invalid profile accepted")
	}
	if _, err := os.Stat(filepath.Join(root, ".extent")); !os.IsNotExist(err) {
		t.Fatalf("planning mutated project: %v", err)
	}
}

func TestWriteLGTMIsIdempotentAndUndoRestoresExactBytes(t *testing.T) {
	root := t.TempDir()
	original := []byte("user collector config\n")
	collector := filepath.Join(root, "otel-collector.yml")
	if err := os.WriteFile(collector, original, 0o640); err != nil {
		t.Fatal(err)
	}
	plan := planner.Plan{Root: root, Detected: []string{"runtime:go"}}
	options := WriteOptions{Profile: "full", Overwrite: true}
	if _, err := WriteLGTM(root, plan, options); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteLGTM(root, plan, options); err != nil {
		t.Fatal(err)
	}
	manifest, err := state.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Operations) != 1 {
		t.Fatalf("idempotent apply recorded %d operations", len(manifest.Operations))
	}
	if err := fileops.UndoKind(root, "stack-config"); err != nil {
		t.Fatal(err)
	}
	restored, err := os.ReadFile(collector)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(restored, original) {
		t.Fatalf("restored bytes = %q", restored)
	}
	if _, err := os.Stat(filepath.Join(root, "docker-compose.observability.yml")); !os.IsNotExist(err) {
		t.Fatalf("created file survived undo: %v", err)
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

func mustRead(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
