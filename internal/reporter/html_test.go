package reporter

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Anubisx404/Extent/internal/baseline"
	"github.com/Anubisx404/Extent/internal/evidence"
	"github.com/Anubisx404/Extent/internal/recommendations"
	"github.com/Anubisx404/Extent/internal/smoke"
)

// specimenReport is fixture data for the HTML report. The FigCraft design
// (docs/design/extent-report-dashboard.figcraft) shows the same values, so the
// rendered page can be compared with the design. It is not from a live run.
func specimenReport() Report {
	retrieved := time.Date(2026, 10, 17, 18, 30, 0, 0, time.UTC)
	trace := "4bf92f3577b34da6a3ce929d0e0e4736"
	prov := func(source, status, query string) evidence.Provenance {
		return evidence.Provenance{Source: source, Scope: "target", ServiceName: "checkout", Window: "30m", Query: query, RetrievedAt: retrieved, Status: status}
	}
	report := Report{
		OK:                 false,
		ServiceName:        "checkout",
		SlowestPath:        "/api/orders/{id}",
		SlowestPathLatency: 412.6,
		DBLatency:          0.287,
		ContainerCPU:       0.34,
		HostCPU:            0.61,
		Summary:            summarize("/api/orders/{id}", 412.6, 0, 0.34, 0.61),
		Warnings:           []string{`loki evidence query failed: Get "http://localhost:3100/loki/api/v1/query_range": dial tcp 127.0.0.1:3100: connect: connection refused`},
		Measurements: map[string]Measurement{
			"httpP95":      {Status: "measured", Scope: "target", Source: "prometheus", Unit: "milliseconds", Value: 412.6},
			"dbP95":        {Status: "measured", Scope: "target", Source: "prometheus", Unit: "seconds", Value: 0.287},
			"containerCPU": {Status: "measured", Scope: "global", Source: "prometheus", Unit: "ratio", Value: 0.34},
			"hostCPU":      {Status: "measured", Scope: "global", Source: "prometheus", Unit: "ratio", Value: 0.61},
		},
		Evidence: evidence.Result{
			Traces: []evidence.TraceExample{
				{TraceID: trace, RootServiceName: "checkout", RootTraceName: "GET /api/orders/{id}", DurationMS: 412.6},
				{TraceID: "a1c3e5f7091b2d4f6a8c0e2f4b6d8f01", RootServiceName: "checkout", RootTraceName: "GET /api/orders", DurationMS: 128.0},
				{TraceID: "0f9e8d7c6b5a49382716051423324150", RootServiceName: "checkout", RootTraceName: "GET /health", DurationMS: 3.0},
			},
			Spans: []evidence.SpanEvidence{
				{TraceID: trace, SpanID: "00f067aa0ba902b7", Name: "GET /api/orders/{id}", Kind: "server", DurationMS: 412.6, Attributes: map[string]string{"http.request.method": "GET", "http.route": "/api/orders/{id}", "http.response.status_code": "200"}},
				{TraceID: trace, SpanID: "1b2c3d4e5f607182", Name: "SELECT order_items", Kind: "client", DurationMS: 186.4, Attributes: map[string]string{"db.system": "postgresql"}},
				{TraceID: trace, SpanID: "2c3d4e5f60718293", Name: "POST payments.internal", Kind: "client", DurationMS: 94.2, Attributes: map[string]string{"server.address": "payments.internal"}},
				{TraceID: trace, SpanID: "3d4e5f60718293a4", Name: "SELECT orders", Kind: "client", DurationMS: 41.0, Attributes: map[string]string{"db.system": "postgresql"}},
				{TraceID: trace, SpanID: "4e5f60718293a4b5", Name: "render order json", Kind: "internal", DurationMS: 12.3},
				{TraceID: "a1c3e5f7091b2d4f6a8c0e2f4b6d8f01", SpanID: "5f60718293a4b5c6", Name: "GET /api/orders", Kind: "server", DurationMS: 128.0},
			},
			DBFindings:   []evidence.DBFinding{{TraceID: trace, System: "postgresql", NormalizedStatement: "SELECT * FROM order_items WHERE order_id = ?", RepeatCount: 12, TotalDurationMS: 186.4}},
			ExternalHTTP: []evidence.HTTPFinding{{TraceID: trace, Destination: "payments.internal:8443", DurationMS: 94.2}},
			Metrics: []evidence.MetricSample{
				{Name: "http_server_duration_milliseconds", Metric: map[string]string{"http_route": "/api/orders/{id}", "service_name": "checkout"}, Unit: "ms", Value: 412.6},
				{Name: "http_server_duration_milliseconds", Metric: map[string]string{"http_route": "/api/orders", "service_name": "checkout"}, Unit: "ms", Value: 128.0},
			},
			Saturation: &evidence.SaturationEvidence{DBPoolWaiting: 3, DBPoolActive: 10, DBPoolIdle: 0},
			Provenance: []evidence.Provenance{
				prov("tempo", "measured", "service trace search"),
				prov("loki", "unavailable", "service logs and error/warning anomalies"),
				prov("prometheus", "measured", "service HTTP p95 latency"),
				prov("prometheus", "measured", "service saturation queries"),
			},
		},
	}
	report.Recommendations = recommendations.Synthesize(recommendations.Input{
		SlowestPath:        report.SlowestPath,
		RepeatedDBPatterns: repeatedDBPatternCount(report),
		ContainerCPU:       report.ContainerCPU,
		HostCPU:            report.HostCPU,
		DBPoolWaiting:      report.Evidence.Saturation.DBPoolWaiting,
	}).Items
	return report
}

// specimenFullReport adds the soak run and baseline comparison so every tab
// of the dashboard renders (docs/design/dashboard-spec.md, "Specimen additions").
func specimenFullReport() Report {
	report := specimenReport()
	report.SoakResult = &smoke.Report{
		TotalRequests:      9600,
		DurationElapsed:    2 * time.Minute,
		RPS:                80.0,
		Concurrency:        8,
		StatusDistribution: map[int]int{200: 9420, 429: 120, 500: 60},
	}
	m := func(v float64, unit string) baseline.Measurement {
		return baseline.Measurement{Status: "measured", Value: v, Unit: unit}
	}
	report.Comparison = &baseline.Comparison{
		Summary:     "HTTP p95 rose 96.1 ms against the baseline.",
		LoadSummary: "Throughput fell from 92.4 to 80.0 rps at the same concurrency.",
		Deltas: map[string]baseline.Delta{
			"httpP95":      {Before: m(316.5, "milliseconds"), After: m(412.6, "milliseconds"), Comparable: true, Change: 96.1},
			"dbP95":        {Before: m(0.201, "seconds"), After: m(0.287, "seconds"), Comparable: true, Change: 0.086},
			"containerCPU": {Before: m(0.29, "ratio"), After: m(0.34, "ratio"), Comparable: true, Change: 0.05},
			"hostCPU":      {Before: m(0.58, "ratio"), After: m(0.61, "ratio"), Comparable: true, Change: 0.03},
		},
	}
	return report
}

func TestRenderHTMLIsSelfContained(t *testing.T) {
	out := RenderHTML(specimenReport(), "30m")
	if strings.Contains(strings.ToLower(out), "<script") {
		t.Fatal("report must not contain scripts")
	}
	for _, external := range []string{"http://fonts.", "https://fonts.", "<link rel=\"stylesheet\"", "src=\"http"} {
		if strings.Contains(out, external) {
			t.Fatalf("report must not load external resources, found %q", external)
		}
	}
	if !strings.Contains(out, "<img src=\"data:image/png;base64,") {
		t.Fatal("expected the embedded app icon")
	}
	if len(appIcon) == 0 || string(appIcon[1:4]) != "PNG" {
		t.Fatal("embedded app icon is not a PNG")
	}
}

func TestRenderHTMLSpecimenSections(t *testing.T) {
	out := RenderHTML(specimenReport(), "30m")
	for _, expected := range []string{
		"<div class=\"health health-yellow\">",
		"<p class=\"path\"><code>/api/orders/{id}</code></p>",
		// Fig. 1: HTTP p95 marker and DB p95 (0.287 s) converted to ms.
		"<tspan class=\"num\" font-size=\"28\">412.6 ms</tspan> HTTP p95",
		"DB p95 287.0 ms",
		"<p>1 warning · exit 5</p>",
		"<div><dt>Exit code</dt><dd>5 (warnings)</dd></div>",
		"<div><dt>Evidence retrieved</dt><dd>2026-10-17 18:30Z</dd></div>",
		// Fig. 2: Loki failed, so its node is crossed out and its line says so.
		"4/4 measurements",
		"3 traces · 6 spans",
		">unavailable</text>",
		"<strong>Possible N+1 query pattern</strong>",
		"<strong>Database connection pool exhaustion</strong>",
		// Fig. 3: shares of the slowest trace's longest span.
		"trace 4bf92f3577b3… · 5 of 6 returned spans · not a timeline",
		"<tspan class=\"num\" font-size=\"15\">186.4</tspan> ms · 45%",
		// Fig. 4: 34 filled waffle cells for container CPU.
		"<b style=\"font:600 var(--t-lg)/1 var(--display)\">34%</b>",
		// Fig. 5: pool slots.
		"aria-label=\"DB pool: 10 active, 0 idle, 3 waiting\"",
		// Fig. 6: tallies keep unavailable sources distinct from zero.
		"class=\"dim\">unavailable</text><text x=\"360\"",
		"Loki could not be queried",
		"<td data-label=\"Retrieved\" class=\"mono nowrap\">2026-10-17T18:30:00Z</td>",
		"--service checkout\n  --last 30m\n  --tempo http://localhost:3200\n  --loki http://localhost:3100\n  --format html",
		".dash:has(#methods:target) .rail a[href=\"#methods\"]",
	} {
		if !strings.Contains(out, expected) {
			t.Fatalf("expected %q in specimen HTML", expected)
		}
	}
	if got := strings.Count(out[strings.Index(out, "aria-label=\"Container CPU\""):], "fill=\"currentColor\"/>"); got < 34 {
		t.Fatalf("expected at least 34 filled waffle cells, got %d", got)
	}
	// Fig. 3 is scoped to the slowest trace: a span from another trace must
	// not appear in it.
	fig := out[strings.Index(out, "<h2>Span anatomy</h2>"):strings.Index(out, "<h2>Trace durations</h2>")]
	if strings.Contains(fig, ">GET /api/orders<") {
		t.Fatal("span anatomy included a span from a different trace")
	}
}

func TestRenderHTMLIsMonochrome(t *testing.T) {
	out := RenderHTML(specimenReport(), "30m")
	css := out[strings.Index(out, "<style>"):strings.Index(out, "</style>")]
	for _, m := range regexp.MustCompile(`oklch\(([0-9.]+) ([0-9.]+) `).FindAllStringSubmatch(css, -1) {
		if c, _ := strconv.ParseFloat(m[2], 64); c > 0.01 {
			t.Fatalf("colour %s has chroma %.3f; the report must stay monochrome", m[0], c)
		}
	}
	if regexp.MustCompile(`#[0-9A-Fa-f]{6}\b`).MatchString(css) {
		t.Fatal("hex colours bypass the monochrome tokens")
	}
}

func TestRenderHTMLEvidenceNotCollected(t *testing.T) {
	report := Report{
		OK:          true,
		ServiceName: "checkout",
		Summary:     "Slowest path is /x; no dominant DB or CPU bottleneck is obvious from the current Prometheus signals.",
		SlowestPath: "/x",
		Measurements: map[string]Measurement{
			"httpP95": {Status: "measured", Scope: "target", Source: "prometheus", Unit: "milliseconds", Value: 12},
		},
	}
	out := RenderHTML(report, "15m")
	for _, expected := range []string{
		"<div class=\"health health-green\">",
		"<div><dt>Exit code</dt><dd>0</dd></div>",
		"Tempo was not queried. Re-run with --tempo http://localhost:3200.",
		"Loki was not queried. Re-run with --loki http://localhost:3100.",
		"Saturation is collected with evidence.",
		"Tempo and Loki were not queried",
		"<span class=\"meta label\">Tempo · not collected</span>",
		"class=\"dim\">not collected</text>",
	} {
		if !strings.Contains(out, expected) {
			t.Fatalf("expected %q in not-collected HTML", expected)
		}
	}
	if strings.Contains(out, "%TARGET_RULES%") {
		t.Fatal("unexpanded CSS placeholder")
	}
	if strings.Contains(out, "class=\"alert\"") {
		t.Fatal("warning panel rendered without warnings")
	}
}

func TestRenderHTMLMissingServiceState(t *testing.T) {
	report := Build(Config{})
	out := RenderHTML(report, "30m")
	for _, expected := range []string{
		"A service name is required for target-scoped reporting.",
		"<div class=\"health health-yellow\">",
		"No measurements were taken.",
		"No route reported",
		"HTTP p95 not collected · no sample to place",
		"--service &lt;service.name&gt;",
	} {
		if !strings.Contains(out, expected) {
			t.Fatalf("expected %q in missing-service HTML", expected)
		}
	}
}

func TestRenderHTMLTabs(t *testing.T) {
	out := RenderHTML(specimenFullReport(), "30m")
	order := []string{"overview", "measurements", "traces", "findings", "logs", "soak", "comparison", "methods", "architecture"}
	last := -1
	for i, id := range order {
		section := `<section id="` + id + `" class="page"`
		at := strings.Index(out, section)
		if at < 0 {
			t.Fatalf("tab %q has no page section", id)
		}
		if at < last {
			t.Fatalf("tab %q is out of order", id)
		}
		last = at
		nav := fmt.Sprintf(`<li><a href="#%s"><span class="i">%02d</span>`, id, i+1)
		if !strings.Contains(out, nav) {
			t.Fatalf("nav entry %q missing", nav)
		}
	}
	for _, expected := range []string{
		"main:not(:has(.page:target)) #overview{display:block}",
		// Fig. 7 trace durations, Fig. 8 DB repeats, Fig. 11 dumbbell.
		`aria-label="Trace root durations"`,
		`<tspan class="num" font-size="17">12×</tspan> · 186.4 ms`,
		`316.5 ms → <tspan class="num" font-size="15">412.6 ms</tspan> <tspan class="dim">(+96.1 ms, +30%)</tspan>`,
		`<td data-label="Change" class="num">+5 pts</td>`,
		`<td data-label="Change" class="num">+0.086 s</td>`,
		// Soak.
		"<div><dt>Throughput</dt><dd>80.0 rps</dd></div>",
		`<td data-label="Status code">429</td><td data-label="Count" class="num">120</td><td data-label="Share" class="num">1.2%</td>`,
		// Methods usage.
		"<code>--include-data</code>",
		"Unset defaults to 50 with --soak",
		// Architecture: this report is drawn solid in the workflow and components.
		`<li class="here"><code>extent report / baseline</code>`,
		`<li class="here">internal/reporter<span>bottleneck report synthesis</span></li>`,
		"Grafana", ":4317 / :4318",
		"Every mutation is planned first and recorded",
		// Logs tab keeps Loki's failure distinct from zero.
		"Loki could not be queried. Start the stack (extent stack up) and re-run.",
	} {
		if !strings.Contains(out, expected) {
			t.Fatalf("expected %q in full specimen HTML", expected)
		}
	}
	if strings.Contains(out, "@media (max-width") {
		t.Fatal("the dashboard is desktop-only: no narrow-viewport layout rules")
	}
}

func TestRenderHTMLConditionalTabs(t *testing.T) {
	out := RenderHTML(specimenReport(), "30m")
	for _, id := range []string{"soak", "comparison"} {
		if strings.Contains(out, `id="`+id+`"`) || strings.Contains(out, `href="#`+id+`"`) {
			t.Fatalf("tab %q rendered without its data", id)
		}
	}
	if !strings.Contains(out, `<li><a href="#methods"><span class="i">06</span>`) {
		t.Fatal("tabs are not renumbered when optional tabs are absent")
	}
}

func TestLogLevelsCountsByLevel(t *testing.T) {
	got := logLevels([]evidence.LogEvent{{Level: "ERROR"}, {Level: "warning"}, {Level: ""}, {Level: "trace"}}, 2)
	for _, row := range []string{">error</text>", ">warn</text>", ">info</text>", ">other</text>", ">anomalies</text>"} {
		if !strings.Contains(got, row) {
			t.Fatalf("missing row %q", row)
		}
	}
	if strings.Count(got, `width="5" height="16"`) != 6 {
		t.Fatalf("expected 6 marks (4 events + 2 anomalies): %s", got)
	}
}

func TestWarningHeadline(t *testing.T) {
	long := `app latency query failed: GET http://localhost:9090/api/v1/query?query=topk%281%2C+histogram_quantile%280.95%29%29: Get "http://localhost:9090/api/v1/query?query=topk%281%29": dial tcp [::1]:9090: connectex: No connection could be made because the target machine actively refused it.`
	got := warningHeadline(long)
	want := "app latency query failed — localhost:9090: No connection could be made because the target machine actively refused it."
	if got != want {
		t.Fatalf("headline = %q, want %q", got, want)
	}
	if short := "A service name is required for target-scoped reporting."; warningHeadline(short) != short {
		t.Fatal("short warnings must be shown unchanged")
	}
}

func TestPoolSlotsScalesLargePools(t *testing.T) {
	got := poolSlots(200, 100, 0)
	if !strings.Contains(got, "one slot ≈ 10.0 connections") {
		t.Fatalf("large pool not scaled: %s", got)
	}
}

func TestRerunCommandQuotesUnsafeServiceNames(t *testing.T) {
	got := rerunCommand(Report{ServiceName: "it's me"}, "30m")
	if !strings.Contains(got, `--service 'it'\''s me'`) {
		t.Fatalf("unsafe service name not quoted: %q", got)
	}
}

// TestWriteHTMLSpecimen writes the specimen report for visual review when
// EXTENT_HTML_SPECIMEN names an output path. It is skipped otherwise.
func TestWriteHTMLSpecimen(t *testing.T) {
	path := os.Getenv("EXTENT_HTML_SPECIMEN")
	if path == "" {
		t.Skip("set EXTENT_HTML_SPECIMEN=<file.html> to write the specimen report")
	}
	if err := os.WriteFile(path, []byte(RenderHTML(specimenFullReport(), "30m")), 0o644); err != nil {
		t.Fatal(err)
	}
	// First-use state: the real Build against a Prometheus with no samples yet.
	prom := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":"success","data":{"result":[]}}`))
	}))
	defer prom.Close()
	report := Build(Config{PrometheusURL: prom.URL, ServiceName: "checkout", Window: "30m"})
	empty := strings.TrimSuffix(path, ".html") + "-no-samples.html"
	if err := os.WriteFile(empty, []byte(RenderHTML(report, "30m")), 0o644); err != nil {
		t.Fatal(err)
	}
}
