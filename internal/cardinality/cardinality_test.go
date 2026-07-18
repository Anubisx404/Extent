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

func TestAnalyzeUsesStructureAndSharedBoundaries(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "ordinary.json"), `{"trace_id":"metadata-value","user_id":"ordinary-field"}`)
	mustWrite(t, filepath.Join(root, "loki.yaml"), "labels:\n  trace_id: extracted\n  environment: prod\n")
	mustWrite(t, filepath.Join(root, "docs", "ignored.json"), `{"expr":"{request_id=~\".+\"}"}`)
	mustWrite(t, filepath.Join(root, "broken.yaml"), "labels: [\n")

	report := Analyze(root)
	if len(report.Findings) != 1 || report.Findings[0].Label != "trace_id" || report.Findings[0].File != "loki.yaml" {
		t.Fatalf("unexpected structural findings: %#v", report.Findings)
	}
	if len(report.Unparsed) != 1 || report.Unparsed[0] != "broken.yaml" {
		t.Fatalf("unparsed files not disclosed: %#v", report.Unparsed)
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
