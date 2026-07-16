package reporter

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBuildReportIdentifiesDatabaseDominatedPath(t *testing.T) {
	prom := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query().Get("query")
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(query, "http_server_duration"):
			w.Write([]byte(prometheusVector("route", "/checkout", "1.2")))
		case strings.Contains(query, "db_client_operation_duration"):
			w.Write([]byte(prometheusScalar("0.96")))
		case strings.Contains(query, "container_cpu_usage_seconds_total"):
			w.Write([]byte(prometheusScalar("0.25")))
		case strings.Contains(query, "node_cpu_seconds_total"):
			w.Write([]byte(prometheusScalar("0.18")))
		default:
			w.Write([]byte(prometheusScalar("1")))
		}
	}))
	defer prom.Close()

	report := Build(Config{PrometheusURL: prom.URL})
	if !report.OK {
		t.Fatalf("expected report OK, got %#v", report)
	}
	if report.SlowestPath != "/checkout" {
		t.Fatalf("expected /checkout, got %q", report.SlowestPath)
	}
	if !strings.Contains(report.Summary, "database dominated") {
		t.Fatalf("expected database dominated summary, got %q", report.Summary)
	}
}

func TestRenderMarkdownIncludesGatheredApplicationData(t *testing.T) {
	report := Report{
		OK:      true,
		Summary: "summary",
		ApplicationData: map[string]any{
			"scan": map[string]any{"runtime": "python"},
		},
	}

	out := RenderMarkdown(report, "30m")

	if !strings.Contains(out, "## Gathered Application Data") {
		t.Fatalf("expected gathered application data appendix, got:\n%s", out)
	}
	if !strings.Contains(out, `"runtime": "python"`) {
		t.Fatalf("expected raw JSON data, got:\n%s", out)
	}
}

func prometheusScalar(value string) string {
	return `{"status":"success","data":{"result":[{"value":[1,"` + value + `"]}]}}`
}

func prometheusVector(label, labelValue, value string) string {
	return `{"status":"success","data":{"result":[{"metric":{"` + label + `":"` + labelValue + `"},"value":[1,"` + value + `"]}]}}`
}
