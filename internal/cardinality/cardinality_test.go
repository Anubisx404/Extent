package cardinality

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAnalyzeFlagsHighCardinalityLabels(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "grafana", "dashboards", "service-overview.json"), `{"expr":"{service_name=~\".+\",trace_id=~\".+\",user_id=~\".+\"}"}`)

	report := Analyze(root)

	if report.Score >= 100 {
		t.Fatalf("expected score penalty, got %#v", report)
	}
	if len(report.Findings) < 2 {
		t.Fatalf("expected high-cardinality findings, got %#v", report)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}
