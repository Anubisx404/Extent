package reporter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Anubisx404/Extent/internal/baseline"
	"github.com/Anubisx404/Extent/internal/evidence"
	"github.com/Anubisx404/Extent/internal/observability"
	"github.com/Anubisx404/Extent/internal/recommendations"
)

type Config struct {
	PrometheusURL   string
	LokiURL         string
	TempoURL        string
	IncludeEvidence bool
	BaselinePath    string
	ServiceName     string
	Window          string
}

type Report struct {
	OK                 bool                   `json:"ok"`
	ServiceName        string                 `json:"serviceName"`
	Summary            string                 `json:"summary"`
	SlowestPath        string                 `json:"slowestPath,omitempty"`
	SlowestPathLatency float64                `json:"slowestPathLatency,omitempty"`
	DBLatency          float64                `json:"dbLatency,omitempty"`
	DBShare            float64                `json:"dbShare,omitempty"`
	ContainerCPU       float64                `json:"containerCpu,omitempty"`
	HostCPU            float64                `json:"hostCpu,omitempty"`
	Evidence           evidence.Result        `json:"evidence,omitempty"`
	Recommendations    []recommendations.Item `json:"recommendations,omitempty"`
	Comparison         *baseline.Comparison   `json:"comparison,omitempty"`
	ApplicationData    any                    `json:"applicationData,omitempty"`
	Warnings           []string               `json:"warnings,omitempty"`
	Measurements       map[string]Measurement `json:"measurements,omitempty"`
}

type Measurement struct {
	Status string  `json:"status"`
	Scope  string  `json:"scope"`
	Source string  `json:"source"`
	Unit   string  `json:"unit,omitempty"`
	Value  float64 `json:"value,omitempty"`
}

type sample struct {
	Metric map[string]string
	Value  float64
	Found  bool
}

func Build(config Config) Report {
	if config.PrometheusURL == "" {
		config.PrometheusURL = "http://localhost:9090"
	}
	if config.Window == "" {
		config.Window = "30m"
	}
	duration, durationErr := time.ParseDuration(config.Window)
	if strings.TrimSpace(config.ServiceName) == "" {
		return Report{OK: false, Summary: "A service name is required for target-scoped reporting.", Warnings: []string{"service name is required"}}
	}
	if durationErr != nil || duration <= 0 || duration > 30*24*time.Hour {
		return Report{OK: false, Summary: "The requested reporting window is invalid.", Warnings: []string{"invalid report window: " + config.Window}}
	}
	window := strconv.FormatInt(int64(duration.Seconds()), 10) + "s"
	service := strconv.Quote(config.ServiceName)
	client := &http.Client{Timeout: 5 * time.Second}
	var warnings []string

	slowest, latencyErr := queryFirst(client, config.PrometheusURL, `topk(1, histogram_quantile(0.95, sum(rate(http_server_request_duration_seconds_bucket{service_name=`+service+`}[`+window+`])) by (le, http_route, route, path)))`)
	if latencyErr != nil {
		warnings = append(warnings, "app latency query failed: "+latencyErr.Error())
	}
	db, dbErr := queryFirst(client, config.PrometheusURL, `histogram_quantile(0.95, sum(rate(db_client_operation_duration_seconds_bucket{service_name=`+service+`}[`+window+`])) by (le))`)
	if dbErr != nil {
		warnings = append(warnings, "db latency query failed: "+dbErr.Error())
	}
	containerCPU, containerErr := queryFirst(client, config.PrometheusURL, `sum(rate(container_cpu_usage_seconds_total{name!=""}[`+window+`]))`)
	if containerErr != nil {
		warnings = append(warnings, "container cpu query failed: "+containerErr.Error())
	}
	hostCPU, hostErr := queryFirst(client, config.PrometheusURL, `1 - avg(rate(node_cpu_seconds_total{mode="idle"}[`+window+`]))`)
	if hostErr != nil {
		warnings = append(warnings, "host cpu query failed: "+hostErr.Error())
	}

	path := firstNonEmpty(slowest.Metric["route"], slowest.Metric["path"], slowest.Metric["http_route"])
	dbShare := 0.0
	summary := summarize(path, slowest.Value, dbShare, containerCPU.Value, hostCPU.Value)
	report := Report{
		OK:                 len(warnings) == 0,
		ServiceName:        config.ServiceName,
		Summary:            summary,
		SlowestPath:        path,
		SlowestPathLatency: slowest.Value,
		DBLatency:          db.Value,
		DBShare:            dbShare,
		ContainerCPU:       containerCPU.Value,
		HostCPU:            hostCPU.Value,
		Warnings:           warnings,
		Measurements: map[string]Measurement{
			"httpP95":      measurement(slowest, latencyErr, "target", "seconds"),
			"dbP95":        measurement(db, dbErr, "target", "seconds"),
			"containerCPU": measurement(containerCPU, containerErr, "global", "ratio"),
			"hostCPU":      measurement(hostCPU, hostErr, "global", "ratio"),
		},
	}
	if config.IncludeEvidence || config.LokiURL != "" || config.TempoURL != "" {
		report.Evidence = evidence.Collect(evidence.Config{PrometheusURL: config.PrometheusURL, LokiURL: config.LokiURL, TempoURL: config.TempoURL, Window: config.Window, ServiceName: config.ServiceName})
		report.Warnings = append(report.Warnings, report.Evidence.Warnings...)
		report.OK = len(report.Warnings) == 0
	}
	report.Recommendations = recommendations.Synthesize(recommendations.Input{
		SlowestPath:        report.SlowestPath,
		DBShare:            report.DBShare,
		RepeatedDBPatterns: repeatedDBPatternCount(report),
		LogsMissingTraceID: missingTraceLogs(report.Evidence),
		ContainerCPU:       report.ContainerCPU,
		HostCPU:            report.HostCPU,
	}).Items
	if config.BaselinePath != "" {
		if before, err := baseline.Load(config.BaselinePath); err == nil {
			after := SnapshotFromReport(report)
			comparison := baseline.Compare(before, after)
			report.Comparison = &comparison
		} else {
			report.Warnings = append(report.Warnings, "baseline compare failed: "+err.Error())
			report.OK = false
		}
	}
	return report
}

func measurement(value sample, err error, scope, unit string) Measurement {
	status := "measured"
	if err != nil {
		status = "unavailable"
	} else if !value.Found {
		status = "missing"
	}
	return Measurement{Status: status, Scope: scope, Source: "prometheus", Unit: unit, Value: value.Value}
}

func summarize(path string, latency, dbShare, containerCPU, hostCPU float64) string {
	if path == "" {
		path = "unknown path"
	}
	switch {
	case dbShare >= 0.5:
		return fmt.Sprintf("Slowest path is %s; it appears database dominated (%.0f%% of observed latency), while container CPU is %.0f%% and host CPU is %.0f%%.", path, dbShare*100, containerCPU*100, hostCPU*100)
	case containerCPU >= 0.8:
		return fmt.Sprintf("Slowest path is %s; container CPU pressure is high at %.0f%%, so investigate application CPU work before database tuning.", path, containerCPU*100)
	case hostCPU >= 0.8:
		return fmt.Sprintf("Slowest path is %s; host CPU pressure is high at %.0f%%, so the bottleneck may be infrastructure saturation.", path, hostCPU*100)
	case latency > 0:
		return fmt.Sprintf("Slowest path is %s; no dominant DB or CPU bottleneck is obvious from the current Prometheus signals.", path)
	default:
		return "No application latency samples were found yet. Generate traffic and rerun the report."
	}
}

func RenderMarkdown(report Report, window string) string {
	if strings.TrimSpace(window) == "" {
		window = "30m"
	}
	var b strings.Builder
	b.WriteString("# Extent Bottleneck Report\n\n")
	b.WriteString("- Window: last " + window + "\n")
	b.WriteString(fmt.Sprintf("- Health status: %s\n", healthStatus(report)))
	b.WriteString("\n## Executive Summary\n\n")
	b.WriteString(report.Summary + "\n\n")
	b.WriteString("## Evidence\n\n")
	if report.SlowestPath != "" {
		b.WriteString("- Slowest path: `" + report.SlowestPath + "`\n")
	}
	writeMeasurement(&b, "HTTP p95 latency", report.Measurements["httpP95"])
	writeMeasurement(&b, "DB p95 latency", report.Measurements["dbP95"])
	writeMeasurement(&b, "Container CPU pressure", report.Measurements["containerCPU"])
	writeMeasurement(&b, "Host CPU pressure", report.Measurements["hostCPU"])
	if len(report.Evidence.Provenance) > 0 {
		b.WriteString("\n### Query Provenance\n\n")
		for _, source := range report.Evidence.Provenance {
			b.WriteString(fmt.Sprintf("- %s [%s/%s], service=%s, window=%s, query=%s\n", source.Source, source.Scope, source.Status, source.ServiceName, source.Window, source.Query))
		}
	}
	if len(report.Evidence.Traces) > 0 {
		b.WriteString("\n## Trace Examples\n\n")
		for _, trace := range report.Evidence.Traces {
			b.WriteString(fmt.Sprintf("- `%s` %s %.0fms\n", trace.TraceID, trace.RootTraceName, trace.DurationMS))
		}
	}
	if len(report.Evidence.LogAnomalies) > 0 {
		b.WriteString("\n## Log Anomalies\n\n")
		for _, anomaly := range report.Evidence.LogAnomalies {
			b.WriteString(fmt.Sprintf("- %s %s trace_id=%s message=%s\n", anomaly.ServiceName, anomaly.Problem, anomaly.TraceID, anomaly.Message))
		}
	}
	if report.Comparison != nil {
		b.WriteString("\n## Before Vs After\n\n")
		b.WriteString(report.Comparison.Summary + "\n")
	}
	b.WriteString("\n## Suggested Fixes\n\n")
	if len(report.Recommendations) > 0 {
		for _, item := range report.Recommendations {
			b.WriteString("- " + item.Title + ": " + item.Evidence + " " + item.Suggestion + "\n")
		}
	} else {
		for _, suggestion := range suggestions(report) {
			b.WriteString("- " + suggestion + "\n")
		}
	}
	if len(report.Warnings) > 0 {
		b.WriteString("\n## Verification Warnings\n\n")
		for _, warning := range report.Warnings {
			b.WriteString("- " + warning + "\n")
		}
	}
	if report.ApplicationData != nil {
		if data, err := json.MarshalIndent(report.ApplicationData, "", "  "); err == nil {
			b.WriteString("\n## Gathered Application Data\n\n")
			b.WriteString("```json\n")
			b.Write(data)
			b.WriteString("\n```\n")
		}
	}
	return b.String()
}

func writeMeasurement(b *strings.Builder, label string, measurement Measurement) {
	if measurement.Source == "" {
		return
	}
	if measurement.Status != "measured" {
		b.WriteString(fmt.Sprintf("- %s: %s (%s scope)\n", label, measurement.Status, measurement.Scope))
		return
	}
	if measurement.Unit == "ratio" {
		b.WriteString(fmt.Sprintf("- %s: %.0f%% (%s scope)\n", label, measurement.Value*100, measurement.Scope))
		return
	}
	b.WriteString(fmt.Sprintf("- %s: %.3f %s (%s scope)\n", label, measurement.Value, measurement.Unit, measurement.Scope))
}

func RenderHTML(report Report, window string) string {
	markdown := RenderMarkdown(report, window)
	var b strings.Builder
	b.WriteString("<!doctype html>\n<html><head><meta charset=\"utf-8\"><title>Extent Bottleneck Report</title>")
	b.WriteString("<style>body{font-family:Arial,sans-serif;max-width:860px;margin:40px auto;line-height:1.5;color:#172026}code{background:#eef2f5;padding:2px 5px;border-radius:4px}pre{white-space:pre-wrap}.ok{color:#197a45}.warn{color:#a15c00}</style>")
	b.WriteString("</head><body>\n")
	for _, line := range strings.Split(markdown, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "# "):
			b.WriteString("<h1>" + html.EscapeString(strings.TrimPrefix(trimmed, "# ")) + "</h1>\n")
		case strings.HasPrefix(trimmed, "## "):
			b.WriteString("<h2>" + html.EscapeString(strings.TrimPrefix(trimmed, "## ")) + "</h2>\n")
		case strings.HasPrefix(trimmed, "- "):
			b.WriteString("<p>" + html.EscapeString(trimmed) + "</p>\n")
		case trimmed == "":
			continue
		default:
			b.WriteString("<p>" + html.EscapeString(trimmed) + "</p>\n")
		}
	}
	b.WriteString("</body></html>\n")
	return b.String()
}

func healthStatus(report Report) string {
	if report.OK {
		return "green"
	}
	if len(report.Warnings) > 0 {
		return "yellow"
	}
	return "unknown"
}

func suggestions(report Report) []string {
	switch {
	case report.DBShare >= 0.6:
		return []string{
			"Inspect the slowest traces for repeated normalized SQL statements and possible N+1 query patterns.",
			"Batch repeated lookups, add missing indexes, or move expensive DB work out of the request path.",
		}
	case report.ContainerCPU >= 0.8:
		return []string{
			"Profile CPU-heavy code paths in the app container before tuning database queries.",
			"Check hot loops, JSON serialization, crypto/compression work, and runtime garbage collection pressure.",
		}
	case report.HostCPU >= 0.8:
		return []string{
			"Reduce local stack pressure or move the target app to a less saturated host.",
			"Compare container CPU with host CPU to separate app bottlenecks from machine saturation.",
		}
	case report.SlowestPathLatency > 0:
		return []string{
			"Open the slowest Tempo traces and compare HTTP server time, DB spans, outbound HTTP spans, and logs.",
			"Add deeper spans around expensive business functions if the trace has unattributed gaps.",
		}
	default:
		return []string{
			"Generate representative traffic, rerun `extent smoke`, then rerun this report.",
			"Confirm the app exports OTLP to the Collector and that Prometheus is scraping the Collector exporter.",
		}
	}
}

func repeatedDBPatternEstimate(report Report) int {
	if report.DBShare >= 0.6 && report.DBLatency > 0 {
		return int(report.DBShare * 50)
	}
	return 0
}

func repeatedDBPatternCount(report Report) int {
	total := 0
	for _, finding := range report.Evidence.DBFindings {
		if finding.RepeatCount > 1 {
			total += finding.RepeatCount
		}
	}
	return total
}

func missingTraceLogs(result evidence.Result) int {
	count := 0
	for _, anomaly := range result.LogAnomalies {
		if anomaly.TraceID == "" {
			count++
		}
	}
	return count
}

func SnapshotFromReport(report Report) baseline.Snapshot {
	measurements := map[string]baseline.Measurement{}
	tempoStatus := evidenceStatus(report.Evidence, "tempo")
	if tempoStatus != "unavailable" && tempoStatus != "unknown" {
		measurements["traceCount"] = baseline.Measurement{Status: "measured", Value: float64(len(report.Evidence.Traces)), Unit: "count"}
	}
	lokiStatus := evidenceStatus(report.Evidence, "loki")
	if lokiStatus != "unavailable" && lokiStatus != "unknown" {
		measurements["logCount"] = baseline.Measurement{Status: "measured", Value: float64(len(report.Evidence.LogAnomalies)), Unit: "count"}
		if len(report.Evidence.LogAnomalies) == 0 {
			measurements["logCorrelation"] = baseline.Measurement{Status: "unknown", Unit: "ratio"}
		} else {
			correlated := 0
			for _, anomaly := range report.Evidence.LogAnomalies {
				if anomaly.TraceID != "" {
					correlated++
				}
			}
			measurements["logCorrelation"] = baseline.Measurement{Status: "measured", Value: float64(correlated) / float64(len(report.Evidence.LogAnomalies)), Unit: "ratio"}
		}
	}
	dbSpans := 0
	for _, span := range report.Evidence.Spans {
		if span.Attributes["db.system"] != "" || span.Attributes["db.system.name"] != "" {
			dbSpans++
		}
	}
	if tempoStatus != "unavailable" && tempoStatus != "unknown" {
		measurements["dbSpanCount"] = baseline.Measurement{Status: "measured", Value: float64(dbSpans), Unit: "count"}
		measurements["slowDBOperations"] = baseline.Measurement{Status: "measured", Value: float64(len(report.Evidence.DBFindings)), Unit: "count"}
		measurements["nPlusOneFindings"] = baseline.Measurement{Status: "measured", Value: float64(repeatedDBPatternCount(report)), Unit: "count"}
	}
	return baseline.Snapshot{
		ServiceName:  report.ServiceName,
		Measurements: measurements,
	}
}

func evidenceStatus(result evidence.Result, source string) string {
	for _, item := range result.Provenance {
		if item.Source == source {
			return item.Status
		}
	}
	return "unknown"
}

func queryFirst(client *http.Client, baseURL, expr string) (sample, error) {
	endpoint, err := observability.ValidateURL(baseURL)
	if err != nil {
		return sample{}, err
	}
	values := make(url.Values)
	values.Set("query", expr)
	bounded := &observability.Client{HTTP: client, BodyLimit: observability.DefaultBodyLimit}
	resp, err := bounded.Do(context.Background(), http.MethodGet, endpoint, "/api/v1/query", values, nil)
	if err != nil {
		return sample{}, err
	}
	var payload struct {
		Status string `json:"status"`
		Data   struct {
			Result []struct {
				Metric map[string]string `json:"metric"`
				Value  []any             `json:"value"`
			} `json:"result"`
		} `json:"data"`
	}
	if err := json.NewDecoder(bytes.NewReader(resp.Body)).Decode(&payload); err != nil {
		return sample{}, err
	}
	if payload.Status != "success" || len(payload.Data.Result) == 0 || len(payload.Data.Result[0].Value) < 2 {
		return sample{}, nil
	}
	raw, ok := payload.Data.Result[0].Value[1].(string)
	if !ok {
		return sample{}, nil
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return sample{}, err
	}
	return sample{Metric: payload.Data.Result[0].Metric, Value: value, Found: true}, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
