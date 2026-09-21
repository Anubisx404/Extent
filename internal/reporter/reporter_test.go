package reporter

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Anubisx404/Extent/internal/baseline"
	"github.com/Anubisx404/Extent/internal/evidence"
	"github.com/Anubisx404/Extent/internal/smoke"
)

func TestBuildReportUsesServiceScopedMeasurementsWithoutInventingDBShare(t *testing.T) {
	prom := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query().Get("query")
		if strings.Contains(query, "http_server_duration_milliseconds") || strings.Contains(query, "db_client_operation_duration") {
			if !strings.Contains(query, `service_name="checkout"`) {
				t.Fatalf("unscoped service query: %s", query)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(query, "http_server_duration_milliseconds"):
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

	report := Build(Config{PrometheusURL: prom.URL, ServiceName: "checkout", Window: "30m"})
	if !report.OK {
		t.Fatalf("expected report OK, got %#v", report)
	}
	if report.SlowestPath != "/checkout" {
		t.Fatalf("expected /checkout, got %q", report.SlowestPath)
	}
	if strings.Contains(report.Summary, "database dominated") || report.DBShare != 0 {
		t.Fatalf("dimensionally incompatible DB ratio was inferred: %#v", report)
	}
	if report.Measurements["httpP95"].Status != "measured" || report.Measurements["httpP95"].Scope != "target" {
		t.Fatalf("measurement metadata = %#v", report.Measurements)
	}
}

func TestRenderMarkdownIncludesDetailedEvidenceSections(t *testing.T) {
	report := Report{
		OK:          true,
		ServiceName: "checkout",
		Summary:     "summary",
		SlowestPath: "/checkout",
		Measurements: map[string]Measurement{
			"httpP95": {Status: "measured", Scope: "target", Source: "prometheus", Unit: "milliseconds", Value: 8},
			"dbP95":   {Status: "measured", Scope: "target", Source: "prometheus", Unit: "seconds", Value: 0.12},
		},
		Evidence: evidence.Result{
			Provenance: []evidence.Provenance{
				{Source: "tempo", Scope: "target", Status: "measured", ServiceName: "checkout", Window: "30m", Query: "service trace search"},
			},
			Traces:        []evidence.TraceExample{{TraceID: "trace-1", RootServiceName: "checkout", RootTraceName: "GET /checkout", DurationMS: 1800}},
			Spans:         []evidence.SpanEvidence{{TraceID: "trace-1", SpanID: "span-1", Name: "request handler - /checkout", Kind: "span", DurationMS: 3, Attributes: map[string]string{"http.route": "/checkout", "http.status_code": "200"}}},
			Metrics:       []evidence.MetricSample{{Name: "http_p95_latency", Metric: map[string]string{"route": "/checkout"}, Value: 8}},
			DBFindings:    []evidence.DBFinding{{TraceID: "trace-1", System: "postgresql", NormalizedStatement: "select * from products where id = ?", RepeatCount: 2, TotalDurationMS: 120}},
			ExternalHTTP:  []evidence.HTTPFinding{{TraceID: "trace-1", Destination: "payments.example", DurationMS: 200}},
			QueueFindings: []evidence.QueueFinding{{TraceID: "trace-1", System: "rabbitmq", Name: "publish checkout", DurationMS: 50}},
			Logs:          []evidence.LogEvent{{ServiceName: "checkout", Level: "info", TraceID: "trace-1", RequestID: "req-1", Message: "checkout request"}},
			LogAnomalies:  []evidence.LogAnomaly{{ServiceName: "checkout", Level: "error", TraceID: "trace-1", Message: "payment failed", Problem: "error_or_warning_log"}},
		},
	}

	out := RenderMarkdown(report, "30m")
	for _, expected := range []string{
		"| Metric | Status | Value / Unit | Scope | Source |",
		"| HTTP p95 latency | measured | 8.000 milliseconds | target | prometheus |",
		"| DB p95 latency | measured | 0.120 seconds | target | prometheus |",
		"### Query Provenance",
		"| Source | Scope | Status | Service | Window | Query |",
		"| tempo | target | measured | checkout | 30m | `service trace search` |",
		"## Evidence Summary",
		"- Traces: 1",
		"- Spans: 1",
		"## Trace Examples",
		"| Trace ID | Service | Root Operation | Duration |",
		"| `trace-1` | checkout | `GET /checkout` | 1800ms |",
		"<summary>Trace Span Details (1)</summary>",
		"request handler - /checkout",
		"## Metric Evidence",
		"http_p95_latency",
		"## Database Findings",
		"| System | Normalized Statement | Repeat Count | Total Duration | Trace ID |",
		"select * from products where id = ?",
		"## External HTTP Findings",
		"| Destination | Duration | Trace ID |",
		"payments.example",
		"## Queue Findings",
		"| System | Name | Duration | Trace ID |",
		"rabbitmq",
		"<summary>Log Events (1)</summary>",
		"checkout request",
		"req-1",
		"### Error/Warning Anomalies",
		"| Service | Problem | Level | Trace ID | Message |",
		"payment failed",
	} {
		if !strings.Contains(out, expected) {
			t.Fatalf("expected %q in report, got:\n%s", expected, out)
		}
	}
}

func TestRenderMarkdownMarksMissingHTTPMetricsAsWarning(t *testing.T) {
	report := Report{
		OK:      true,
		Summary: "summary",
		Measurements: map[string]Measurement{
			"httpP95": {Status: "missing", Scope: "target", Source: "prometheus", Unit: "milliseconds"},
		},
	}

	out := RenderMarkdown(report, "30m")

	if !strings.Contains(out, "- Health status: yellow") {
		t.Fatalf("expected missing HTTP evidence to downgrade health, got:\n%s", out)
	}
	if !strings.Contains(out, "| HTTP p95 latency | missing | - | target | prometheus |") {
		t.Fatalf("expected missing measurement table row, got:\n%s", out)
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

	if !strings.Contains(out, "<summary>Gathered Application Data</summary>") {
		t.Fatalf("expected gathered application data appendix, got:\n%s", out)
	}
	if !strings.Contains(out, `"runtime": "python"`) {
		t.Fatalf("expected raw JSON data, got:\n%s", out)
	}
}

func TestRenderMarkdownSanitizesUnsafeAndMultilineContent(t *testing.T) {
	report := Report{
		OK:          true,
		ServiceName: "service|with\nnewlines",
		Summary:     "summary",
		Measurements: map[string]Measurement{
			"httpP95": {Status: "measured", Scope: "target", Source: "prometheus", Unit: "milliseconds", Value: 10},
		},
		Evidence: evidence.Result{
			DBFindings: []evidence.DBFinding{
				{
					TraceID:             "trace|1\r\n2",
					System:              "sql|injection",
					NormalizedStatement: "select * from `users`\nwhere a = 1 | b = 2",
					RepeatCount:         3,
					TotalDurationMS:     300,
				},
			},
			ExternalHTTP: []evidence.HTTPFinding{
				{
					TraceID:     "trace|http",
					Destination: "http://api.internal/path|with\npipe",
					DurationMS:  150,
				},
			},
			Spans: []evidence.SpanEvidence{
				{
					TraceID:    "trace|span",
					SpanID:     "span|1",
					Name:       "handler\nwith\rnewlines`code`|pipe",
					Kind:       "span",
					DurationMS: 25,
					Attributes: map[string]string{
						"http.route": "/api|test\nbreak",
					},
				},
			},
			Logs: []evidence.LogEvent{
				{
					ServiceName: "app",
					Level:       "error",
					TraceID:     "trace|log",
					SpanID:      "span|log",
					RequestID:   "req|123\nnewline",
					Message:     "multi\nline\r\nmessage | with | pipes `and code`",
				},
			},
		},
	}

	out := RenderMarkdown(report, "15m")

	for _, bad := range []string{
		"\r",
		"select * from `users`",
		"where a = 1 | b = 2",
		"multi\nline",
		"handler\nwith",
	} {
		if strings.Contains(out, bad) {
			t.Fatalf("found unescaped/unsafe substring %q in markdown:\n%s", bad, out)
		}
	}

	for _, expected := range []string{
		"| sql\\|injection | `select * from 'users' where a = 1 \\| b = 2` | 3 | 300.0ms | `trace\\|1 2` |",
		"| `http://api.internal/path\\|with pipe` | 150.0ms | `trace\\|http` |",
		"| `handler with newlines'code'\\|pipe` | span | 25.0ms | `trace\\|span` | `span\\|1` | (route=/api\\|test break) |",
		"| error | app | `trace\\|log` | `span\\|log` | `req\\|123 newline` | multi line message \\| with \\| pipes 'and code' |",
	} {
		if !strings.Contains(out, expected) {
			t.Fatalf("expected sanitized row %q in markdown, got:\n%s", expected, out)
		}
	}
}

func TestRenderHTMLStructureAndEscaping(t *testing.T) {
	report := Report{
		OK:          true,
		ServiceName: "web<script>alert(1)</script>",
		Summary:     "A test <summary> message & more",
		SlowestPath: "/search?q=<test>",
		Measurements: map[string]Measurement{
			"httpP95": {Status: "measured", Scope: "target", Source: "prometheus", Unit: "milliseconds", Value: 42.5},
		},
		Evidence: evidence.Result{
			Provenance: []evidence.Provenance{
				{Source: "prometheus", Scope: "target", Status: "measured", ServiceName: "web<script>", Window: "30m", Query: "topk(1, ...)"},
			},
			Traces: []evidence.TraceExample{
				{TraceID: "tr-1", RootServiceName: "web", RootTraceName: "GET /search", DurationMS: 200},
			},
			Spans: []evidence.SpanEvidence{
				{TraceID: "tr-1", SpanID: "sp-1", Name: "search<handler>", Kind: "span", DurationMS: 40},
			},
			Logs: []evidence.LogEvent{
				{ServiceName: "web", Level: "warn", TraceID: "tr-1", SpanID: "sp-1", Message: "found <nil> pointer"},
			},
		},
	}

	htmlOut := RenderHTML(report, "30m")

	if strings.Contains(htmlOut, "<script>alert(1)</script>") {
		t.Fatalf("expected XSS payload to be escaped in HTML, got:\n%s", htmlOut)
	}
	for _, expected := range []string{
		"<!doctype html>",
		"<title>Extent Bottleneck Report</title>",
		"web&lt;script&gt;alert(1)&lt;/script&gt;",
		"A test &lt;summary&gt; message &amp; more",
		"/search?q=&lt;test&gt;",
		"<table>",
		"<thead>",
		"<tbody>",
		"<tr><td>HTTP p95 latency</td><td>measured</td><td>42.500 milliseconds</td><td>target</td><td>prometheus</td></tr>",
		"<details>",
		"<summary>Trace Span Details (1)</summary>",
		"search&lt;handler&gt;",
		"<summary>Log Events (1)</summary>",
		"found &lt;nil&gt; pointer",
	} {
		if !strings.Contains(htmlOut, expected) {
			t.Fatalf("expected %q in rendered HTML, got:\n%s", expected, htmlOut)
		}
	}
}

func prometheusScalar(value string) string {
	return `{"status":"success","data":{"result":[{"value":[1,"` + value + `"]}]}}`
}

func prometheusVector(label, labelValue, value string) string {
	return `{"status":"success","data":{"result":[{"metric":{"` + label + `":"` + labelValue + `"},"value":[1,"` + value + `"]}]}}`
}

func TestBuildExecutesSoakTestAndPopulatesSoakMetrics(t *testing.T) {
	appServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))
	defer appServer.Close()

	promServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query().Get("query")
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(query, "http_server_duration_milliseconds"):
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
	defer promServer.Close()

	report := Build(Config{
		PrometheusURL: promServer.URL,
		ServiceName:   "checkout",
		SoakDuration:  200 * time.Millisecond,
		TargetURL:     appServer.URL,
		Concurrency:   2,
	})

	if report.SoakResult == nil {
		t.Fatal("expected report.SoakResult != nil")
	}
	if report.SoakResult.TotalRequests <= 0 {
		t.Fatalf("expected report.SoakResult.TotalRequests > 0, got %d", report.SoakResult.TotalRequests)
	}
	if report.Measurements["soakThroughput"].Value <= 0 {
		t.Fatalf("expected report.Measurements[\"soakThroughput\"].Value > 0, got %f", report.Measurements["soakThroughput"].Value)
	}
	markdown := RenderMarkdown(report, "10m")
	if !strings.Contains(markdown, "Sustained Soak Performance") {
		t.Fatalf("expected markdown to contain 'Sustained Soak Performance', got:\n%s", markdown)
	}
	if !strings.Contains(strings.ToLower(markdown), "requests") || !strings.Contains(strings.ToLower(markdown), "rps") {
		t.Fatalf("expected markdown to mention request count and throughput, got:\n%s", markdown)
	}
}

func TestSnapshotFromReportPopulatesLoadProfile(t *testing.T) {
	report := Report{
		ServiceName:        "checkout",
		SlowestPathLatency: 125.5,
		SoakResult: &smoke.Report{
			TotalRequests:   100,
			DurationElapsed: 5 * time.Second,
			RPS:             20.0,
			Concurrency:     4,
			StatusDistribution: map[int]int{
				200: 95,
				500: 5,
			},
		},
	}
	snapshot := SnapshotFromReport(report)
	if snapshot.LoadProfile == nil {
		t.Fatal("expected snapshot.LoadProfile != nil")
	}
	if snapshot.LoadProfile.Concurrency != 4 {
		t.Fatalf("expected concurrency 4, got %d", snapshot.LoadProfile.Concurrency)
	}
	if snapshot.LoadProfile.TotalRequests != 100 {
		t.Fatalf("expected total requests 100, got %d", snapshot.LoadProfile.TotalRequests)
	}
	if snapshot.LoadProfile.ThroughputRPS != 20.0 {
		t.Fatalf("expected throughput 20.0, got %f", snapshot.LoadProfile.ThroughputRPS)
	}
	if snapshot.LoadProfile.ErrorRate != 0.05 {
		t.Fatalf("expected error rate 0.05, got %f", snapshot.LoadProfile.ErrorRate)
	}
	if snapshot.LoadProfile.P99LatencyMS != 125.5 {
		t.Fatalf("expected p99 latency 125.5, got %f", snapshot.LoadProfile.P99LatencyMS)
	}
	if snapshot.LoadProfile.DurationSeconds != 5.0 {
		t.Fatalf("expected duration 5.0, got %f", snapshot.LoadProfile.DurationSeconds)
	}
}

func TestRenderMarkdownAndHTMLWithSoakResult(t *testing.T) {
	report := Report{
		OK:          true,
		ServiceName: "checkout",
		Summary:     "summary",
		SoakResult: &smoke.Report{
			TotalRequests:   50,
			DurationElapsed: 2 * time.Second,
			RPS:             25.0,
			Concurrency:     2,
			StatusDistribution: map[int]int{
				200: 50,
			},
		},
		Comparison: &baseline.Comparison{
			LoadSummary: "Load profile (stable): throughput 20.0 -> 25.0 RPS",
		},
	}
	md := RenderMarkdown(report, "10m")
	if !strings.Contains(md, "## Sustained Soak Performance") {
		t.Fatalf("missing Sustained Soak Performance in md:\n%s", md)
	}
	if !strings.Contains(md, "Load profile (stable)") {
		t.Fatalf("missing LoadSummary in md:\n%s", md)
	}
	htmlOut := RenderHTML(report, "10m")
	if !strings.Contains(htmlOut, "<h2>Sustained Soak Performance</h2>") {
		t.Fatalf("missing Sustained Soak Performance in html:\n%s", htmlOut)
	}
	if !strings.Contains(htmlOut, "Load profile (stable)") {
		t.Fatalf("missing LoadSummary in html:\n%s", htmlOut)
	}
}
