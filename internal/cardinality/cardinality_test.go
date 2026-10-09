package cardinality

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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

func TestAnalyzeWithPrometheusFlagsHighCardinalityAndIdentityLabels(t *testing.T) {
	root := t.TempDir()
	many := make([]string, 150)
	for i := range many {
		many[i] = fmt.Sprintf(`"/item/%d"`, i)
	}
	var matched []string
	prom := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if got := r.URL.Query()["match[]"]; len(got) != 1 || got[0] != "http_server_duration_milliseconds_count" {
			t.Errorf("match[] = %v", got)
		}
		matched = append(matched, r.URL.Path)
		switch {
		case r.URL.Path == "/api/v1/labels":
			w.Write([]byte(`{"status":"success","data":["__name__","service_name","env","route","process_pid"]}`))
		case r.URL.Path == "/api/v1/label/env/values":
			w.Write([]byte(`{"status":"success","data":["prod","staging"]}`))
		case r.URL.Path == "/api/v1/label/route/values":
			w.Write([]byte(`{"status":"success","data":[` + strings.Join(many, ",") + `]}`))
		case r.URL.Path == "/api/v1/label/process_pid/values":
			w.Write([]byte(`{"status":"success","data":["101","102"]}`))
		case r.URL.Path == "/api/v1/label/service_name/values":
			w.Write([]byte(`{"status":"success","data":["checkout"]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer prom.Close()

	report := AnalyzeWithOptions(root, Options{PrometheusURL: prom.URL})
	got := map[string]bool{}
	for _, finding := range report.Findings {
		got[finding.Label] = true
	}
	if !got["route"] || !got["process_pid"] {
		t.Fatalf("expected route and process_pid findings, got %#v", report.Findings)
	}
	if got["env"] || got["service_name"] {
		t.Fatalf("bounded labels flagged: %#v", report.Findings)
	}
	if report.Score != 100-15*len(report.Findings) {
		t.Fatalf("score = %d for %d findings", report.Score, len(report.Findings))
	}
	if len(report.Warnings) != 0 {
		t.Fatalf("unexpected warnings: %#v", report.Warnings)
	}
}

func TestAnalyzeWithoutPrometheusIsStaticOnly(t *testing.T) {
	root := t.TempDir()
	report := AnalyzeWithOptions(root, Options{})
	if report.Score != 100 || len(report.Findings) != 0 || len(report.Warnings) != 0 {
		t.Fatalf("static-only analysis changed: %#v", report)
	}
}

func TestAnalyzeWithUnreachablePrometheusWarnsWithoutFailing(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "dash.json"), `{"expr":"{trace_id=~\".+\"}"}`)
	report := AnalyzeWithOptions(root, Options{PrometheusURL: "ftp://example.test"})
	if len(report.Warnings) != 1 {
		t.Fatalf("expected one warning, got %#v", report.Warnings)
	}
	if len(report.Findings) != 1 || report.Findings[0].Label != "trace_id" {
		t.Fatalf("static findings lost: %#v", report.Findings)
	}
}
