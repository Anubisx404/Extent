package reporter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Anubisx404/Extent/internal/baseline"
	"github.com/Anubisx404/Extent/internal/evidence"
	"github.com/Anubisx404/Extent/internal/observability"
	"github.com/Anubisx404/Extent/internal/recommendations"
	"github.com/Anubisx404/Extent/internal/smoke"
)

type Config struct {
	PrometheusURL   string
	LokiURL         string
	TempoURL        string
	IncludeEvidence bool
	BaselinePath    string
	ServiceName     string
	Window          string
	SoakDuration    time.Duration
	TargetURL       string
	Concurrency     int
	RateLimit       float64
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
	SoakResult         *smoke.Report          `json:"soakResult,omitempty"`
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

	slowest, latencyErr := queryFirst(client, config.PrometheusURL, `topk(1, histogram_quantile(0.95, sum(rate(http_server_duration_milliseconds_bucket{service_name=`+service+`}[`+window+`])) by (le, http_route, route, path)))`)
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

	if latencyErr == nil && !slowest.Found {
		warnings = append(warnings, "service HTTP latency metric is missing; expected http_server_duration_milliseconds_bucket for the target service")
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
			"httpP95":      measurement(slowest, latencyErr, "target", "milliseconds"),
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
	var rateLimit429, dbPoolWaiting, queueLag, memoryGrowth float64
	if report.Evidence.Saturation != nil {
		rateLimit429 = report.Evidence.Saturation.RateLimit429Count
		dbPoolWaiting = report.Evidence.Saturation.DBPoolWaiting
		queueLag = report.Evidence.Saturation.QueueLag
		memoryGrowth = report.Evidence.Saturation.MemoryGrowthBytesSec
	}
	report.Recommendations = recommendations.Synthesize(recommendations.Input{
		SlowestPath:          report.SlowestPath,
		DBShare:              report.DBShare,
		RepeatedDBPatterns:   repeatedDBPatternCount(report),
		LogsMissingTraceID:   missingTraceLogs(report.Evidence),
		ContainerCPU:         report.ContainerCPU,
		HostCPU:              report.HostCPU,
		RateLimit429Count:    rateLimit429,
		DBPoolWaiting:        dbPoolWaiting,
		QueueLag:             queueLag,
		MemoryGrowthBytesSec: memoryGrowth,
	}).Items
	if config.SoakDuration > 0 && config.TargetURL != "" {
		soakRes := smoke.Run(smoke.Config{
			URL:           config.TargetURL,
			PrometheusURL: config.PrometheusURL,
			TempoURL:      config.TempoURL,
			LokiURL:       config.LokiURL,
			ServiceName:   config.ServiceName,
			Duration:      config.SoakDuration,
			Concurrency:   config.Concurrency,
			RateLimit:     config.RateLimit,
			SettleTimeout: 2 * time.Second,
		})
		report.SoakResult = &soakRes
		if !soakRes.OK {
			report.Warnings = append(report.Warnings, "soak test traffic generation encountered failures")
		}
		var errCount int
		for code, count := range soakRes.StatusDistribution {
			if code >= 400 {
				errCount += count
			}
		}
		errorRate := 0.0
		if soakRes.TotalRequests > 0 {
			errorRate = float64(errCount) / float64(soakRes.TotalRequests)
		}
		report.Measurements["soakThroughput"] = Measurement{
			Status: "measured",
			Scope:  "target",
			Source: "smoke",
			Unit:   "rps",
			Value:  soakRes.RPS,
		}
		report.Measurements["soakTotalRequests"] = Measurement{
			Status: "measured",
			Scope:  "target",
			Source: "smoke",
			Unit:   "requests",
			Value:  float64(soakRes.TotalRequests),
		}
		report.Measurements["soakErrorRate"] = Measurement{
			Status: "measured",
			Scope:  "target",
			Source: "smoke",
			Unit:   "ratio",
			Value:  errorRate,
		}
	}
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
	if report.SlowestPath == "" && len(report.Evidence.Traces) > 0 {
		report.Summary = fmt.Sprintf("Telemetry contains %d target-service trace(s) and %d span(s), but the service HTTP latency metric is unavailable; verify the Collector Prometheus exporter and metric family.", len(report.Evidence.Traces), len(report.Evidence.Spans))
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
	if report.ServiceName != "" {
		b.WriteString(fmt.Sprintf("- Target service: `%s`\n", safeInline(report.ServiceName)))
	}
	if report.SlowestPath != "" {
		b.WriteString(fmt.Sprintf("- Slowest path: `%s`\n", safeInline(report.SlowestPath)))
	}
	b.WriteString("\n## Executive Summary\n\n")
	b.WriteString(report.Summary + "\n\n")

	b.WriteString("## Measurements\n\n")
	b.WriteString("| Metric | Status | Value / Unit | Scope | Source |\n")
	b.WriteString("| --- | --- | --- | --- | --- |\n")
	for _, m := range orderedMeasurements(report.Measurements) {
		b.WriteString(fmt.Sprintf("| %s | %s | %s | %s | %s |\n",
			safeTableCell(m.label),
			safeTableCell(m.measurement.Status),
			safeTableCell(formatMeasurementValue(m.measurement)),
			safeTableCell(m.measurement.Scope),
			safeTableCell(m.measurement.Source),
		))
	}

	if len(report.Evidence.Provenance) > 0 {
		b.WriteString("\n### Query Provenance\n\n")
		b.WriteString("| Source | Scope | Status | Service | Window | Query |\n")
		b.WriteString("| --- | --- | --- | --- | --- | --- |\n")
		for _, source := range report.Evidence.Provenance {
			b.WriteString(fmt.Sprintf("| %s | %s | %s | %s | %s | `%s` |\n",
				safeTableCell(source.Source),
				safeTableCell(source.Scope),
				safeTableCell(source.Status),
				safeTableCell(source.ServiceName),
				safeTableCell(source.Window),
				safeTableCell(source.Query),
			))
		}
	}

	if report.SoakResult != nil {
		b.WriteString("\n## Sustained Soak Performance\n\n")
		concurrency := report.SoakResult.Concurrency
		if concurrency <= 0 {
			concurrency = 1
		}
		b.WriteString(fmt.Sprintf("- Duration: %s\n", report.SoakResult.DurationElapsed.Round(time.Millisecond)))
		b.WriteString(fmt.Sprintf("- Concurrency: %d\n", concurrency))
		b.WriteString(fmt.Sprintf("- Total requests: %d\n", report.SoakResult.TotalRequests))
		b.WriteString(fmt.Sprintf("- Throughput: %.1f RPS\n", report.SoakResult.RPS))
		if len(report.SoakResult.StatusDistribution) > 0 {
			b.WriteString("\n### Status Distribution\n\n")
			b.WriteString("| Status Code | Count |\n")
			b.WriteString("| --- | --- |\n")
			codes := make([]int, 0, len(report.SoakResult.StatusDistribution))
			for code := range report.SoakResult.StatusDistribution {
				codes = append(codes, code)
			}
			sort.Ints(codes)
			for _, code := range codes {
				b.WriteString(fmt.Sprintf("| %d | %d |\n", code, report.SoakResult.StatusDistribution[code]))
			}
		}
		if report.Comparison != nil && report.Comparison.LoadSummary != "" {
			b.WriteString(fmt.Sprintf("\n- Baseline Comparison: %s\n", safeInline(report.Comparison.LoadSummary)))
		}
	}

	b.WriteString("\n## Evidence Summary\n\n")
	b.WriteString(fmt.Sprintf("- Traces: %d\n", len(report.Evidence.Traces)))
	b.WriteString(fmt.Sprintf("- Spans: %d\n", len(report.Evidence.Spans)))
	b.WriteString(fmt.Sprintf("- Target metrics: %d\n", len(report.Evidence.Metrics)))
	b.WriteString(fmt.Sprintf("- Log events: %d\n", len(report.Evidence.Logs)))
	b.WriteString(fmt.Sprintf("- Error/warning anomalies: %d\n", len(report.Evidence.LogAnomalies)))
	b.WriteString(fmt.Sprintf("- Database findings: %d\n", len(report.Evidence.DBFindings)))
	b.WriteString(fmt.Sprintf("- External HTTP findings: %d\n", len(report.Evidence.ExternalHTTP)))
	b.WriteString(fmt.Sprintf("- Queue findings: %d\n", len(report.Evidence.QueueFindings)))

	if len(report.Evidence.Traces) > 0 {
		b.WriteString("\n## Trace Examples\n\n")
		b.WriteString("| Trace ID | Service | Root Operation | Duration |\n")
		b.WriteString("| --- | --- | --- | --- |\n")
		for _, trace := range report.Evidence.Traces {
			b.WriteString(fmt.Sprintf("| `%s` | %s | `%s` | %.0fms |\n",
				safeTableCell(trace.TraceID),
				safeTableCell(trace.RootServiceName),
				safeTableCell(trace.RootTraceName),
				trace.DurationMS,
			))
		}
	} else {
		b.WriteString("\n## Trace Examples\n\n- No target-service traces were returned.\n")
	}

	if len(report.Evidence.DBFindings) > 0 {
		b.WriteString("\n## Database Findings\n\n")
		b.WriteString("| System | Normalized Statement | Repeat Count | Total Duration | Trace ID |\n")
		b.WriteString("| --- | --- | --- | --- | --- |\n")
		for _, finding := range report.Evidence.DBFindings {
			b.WriteString(fmt.Sprintf("| %s | `%s` | %d | %.1fms | `%s` |\n",
				safeTableCell(finding.System),
				safeTableCell(finding.NormalizedStatement),
				finding.RepeatCount,
				finding.TotalDurationMS,
				safeTableCell(finding.TraceID),
			))
		}
	} else {
		b.WriteString("\n## Database Findings\n\n- No database spans or slow/repeated database operations were observed.\n")
	}

	if len(report.Evidence.ExternalHTTP) > 0 {
		b.WriteString("\n## External HTTP Findings\n\n")
		b.WriteString("| Destination | Duration | Trace ID |\n")
		b.WriteString("| --- | --- | --- |\n")
		for _, finding := range report.Evidence.ExternalHTTP {
			b.WriteString(fmt.Sprintf("| `%s` | %.1fms | `%s` |\n",
				safeTableCell(finding.Destination),
				finding.DurationMS,
				safeTableCell(finding.TraceID),
			))
		}
	} else {
		b.WriteString("\n## External HTTP Findings\n\n- No external HTTP spans were observed.\n")
	}

	if len(report.Evidence.QueueFindings) > 0 {
		b.WriteString("\n## Queue Findings\n\n")
		b.WriteString("| System | Name | Duration | Trace ID |\n")
		b.WriteString("| --- | --- | --- | --- |\n")
		for _, finding := range report.Evidence.QueueFindings {
			b.WriteString(fmt.Sprintf("| %s | `%s` | %.1fms | `%s` |\n",
				safeTableCell(finding.System),
				safeTableCell(finding.Name),
				finding.DurationMS,
				safeTableCell(finding.TraceID),
			))
		}
	} else {
		b.WriteString("\n## Queue Findings\n\n- No queue or messaging spans were observed.\n")
	}

	if len(report.Evidence.Metrics) > 0 {
		b.WriteString("\n## Metric Evidence\n\n")
		b.WriteString("| Metric Name | Value / Unit | Labels |\n")
		b.WriteString("| --- | --- | --- |\n")
		for _, metric := range report.Evidence.Metrics {
			unit := metric.Unit
			if unit == "" {
				unit = "value"
			}
			labels := formatMetricLabels(metric.Metric)
			b.WriteString(fmt.Sprintf("| `%s` | %.3f %s | %s |\n",
				safeTableCell(metric.Name),
				metric.Value,
				safeTableCell(unit),
				safeTableCell(labels),
			))
		}
	} else {
		b.WriteString("\n## Metric Evidence\n\n- No target-service metric samples were returned.\n")
	}

	if len(report.Evidence.Spans) > 0 {
		b.WriteString(fmt.Sprintf("\n<details>\n<summary>Trace Span Details (%d)</summary>\n\n", len(report.Evidence.Spans)))
		b.WriteString("| Span Name | Kind | Duration | Trace ID | Span ID | Context |\n")
		b.WriteString("| --- | --- | --- | --- | --- | --- |\n")
		for _, span := range report.Evidence.Spans {
			b.WriteString(fmt.Sprintf("| `%s` | %s | %.1fms | `%s` | `%s` | %s |\n",
				safeTableCell(span.Name),
				safeTableCell(span.Kind),
				span.DurationMS,
				safeTableCell(span.TraceID),
				safeTableCell(span.SpanID),
				safeTableCell(spanContext(span.Attributes)),
			))
		}
		b.WriteString("\n</details>\n")
	} else {
		b.WriteString("\n## Trace Span Details\n\n- No span details were returned for the target traces.\n")
	}

	if len(report.Evidence.Logs) > 0 {
		b.WriteString(fmt.Sprintf("\n<details>\n<summary>Log Events (%d)</summary>\n\n", len(report.Evidence.Logs)))
		b.WriteString("| Level | Service | Trace ID | Span ID | Request ID | Message |\n")
		b.WriteString("| --- | --- | --- | --- | --- | --- |\n")
		for _, event := range report.Evidence.Logs {
			level := event.Level
			if level == "" {
				level = "info"
			}
			b.WriteString(fmt.Sprintf("| %s | %s | `%s` | `%s` | `%s` | %s |\n",
				safeTableCell(level),
				safeTableCell(event.ServiceName),
				safeTableCell(event.TraceID),
				safeTableCell(event.SpanID),
				safeTableCell(event.RequestID),
				safeTableCell(event.Message),
			))
		}
		b.WriteString("\n</details>\n")
	} else {
		b.WriteString("\n## Log Events\n\n- No target-service log events were returned.\n")
	}

	b.WriteString("\n### Error/Warning Anomalies\n\n")
	if len(report.Evidence.LogAnomalies) > 0 {
		b.WriteString("| Service | Problem | Level | Trace ID | Message |\n")
		b.WriteString("| --- | --- | --- | --- | --- |\n")
		for _, anomaly := range report.Evidence.LogAnomalies {
			b.WriteString(fmt.Sprintf("| %s | %s | %s | `%s` | %s |\n",
				safeTableCell(anomaly.ServiceName),
				safeTableCell(anomaly.Problem),
				safeTableCell(anomaly.Level),
				safeTableCell(anomaly.TraceID),
				safeTableCell(anomaly.Message),
			))
		}
	} else {
		b.WriteString("- None observed.\n")
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
			b.WriteString("- " + safeInline(warning) + "\n")
		}
	}
	if report.ApplicationData != nil {
		if data, err := json.MarshalIndent(report.ApplicationData, "", "  "); err == nil {
			b.WriteString("\n<details>\n<summary>Gathered Application Data</summary>\n\n")
			b.WriteString("```json\n")
			b.Write(data)
			b.WriteString("\n```\n\n</details>\n")
		}
	}
	return b.String()
}

type namedMeasurement struct {
	key         string
	label       string
	measurement Measurement
}

func orderedMeasurements(m map[string]Measurement) []namedMeasurement {
	standard := []struct {
		key   string
		label string
	}{
		{key: "httpP95", label: "HTTP p95 latency"},
		{key: "dbP95", label: "DB p95 latency"},
		{key: "containerCPU", label: "Container CPU pressure"},
		{key: "hostCPU", label: "Host CPU pressure"},
		{key: "soakThroughput", label: "Soak throughput"},
		{key: "soakTotalRequests", label: "Soak total requests"},
		{key: "soakErrorRate", label: "Soak error rate"},
	}
	seen := map[string]bool{}
	var result []namedMeasurement
	for _, item := range standard {
		if val, ok := m[item.key]; ok {
			result = append(result, namedMeasurement{key: item.key, label: item.label, measurement: val})
			seen[item.key] = true
		}
	}
	var extraKeys []string
	for k := range m {
		if !seen[k] {
			extraKeys = append(extraKeys, k)
		}
	}
	sort.Strings(extraKeys)
	for _, k := range extraKeys {
		result = append(result, namedMeasurement{key: k, label: k, measurement: m[k]})
	}
	return result
}

func formatMeasurementValue(m Measurement) string {
	if m.Status != "measured" {
		return "-"
	}
	if m.Unit == "ratio" {
		return fmt.Sprintf("%.0f%%", m.Value*100)
	}
	if m.Unit == "requests" {
		return fmt.Sprintf("%.0f requests", m.Value)
	}
	if m.Unit != "" {
		return fmt.Sprintf("%.3f %s", m.Value, m.Unit)
	}
	return fmt.Sprintf("%.3f", m.Value)
}

func RenderHTML(report Report, window string) string {
	if strings.TrimSpace(window) == "" {
		window = "30m"
	}
	var b strings.Builder
	b.WriteString("<!doctype html>\n<html lang=\"en\">\n<head>\n<meta charset=\"utf-8\">\n")
	b.WriteString("<meta name=\"viewport\" content=\"width=device-width, initial-scale=1.0\">\n")
	b.WriteString("<title>Extent Bottleneck Report</title>\n")
	b.WriteString("<style>\n")
	b.WriteString("body{font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Helvetica,Arial,sans-serif;max-width:960px;margin:32px auto;padding:0 20px;line-height:1.6;color:#1f2328;background-color:#fff}\n")
	b.WriteString("h1,h2,h3{color:#1f2328;margin-top:24px;margin-bottom:12px;font-weight:600}\n")
	b.WriteString("h1{font-size:28px;border-bottom:1px solid #d0d7de;padding-bottom:8px}\n")
	b.WriteString("h2{font-size:20px;border-bottom:1px solid #eaeef2;padding-bottom:6px}\n")
	b.WriteString("h3{font-size:16px}\n")
	b.WriteString("p{margin:8px 0}\n")
	b.WriteString("ul{margin:8px 0;padding-left:24px}\n")
	b.WriteString("li{margin:4px 0}\n")
	b.WriteString("code{font-family:ui-monospace,SFMono-Regular,Consolas,monospace;background:#f6f8fa;padding:2px 6px;border-radius:4px;font-size:13px}\n")
	b.WriteString("pre{background:#f6f8fa;padding:14px;border-radius:6px;overflow-x:auto;font-family:ui-monospace,SFMono-Regular,Consolas,monospace;font-size:13px;border:1px solid #d0d7de}\n")
	b.WriteString("table{width:100%;border-collapse:collapse;margin:14px 0;font-size:14px}\n")
	b.WriteString("th,td{border:1px solid #d0d7de;padding:8px 12px;text-align:left;vertical-align:top}\n")
	b.WriteString("th{background-color:#f6f8fa;font-weight:600}\n")
	b.WriteString("tr:nth-child(even){background-color:#fcfcfc}\n")
	b.WriteString("details{margin:16px 0;border:1px solid #d0d7de;border-radius:6px;padding:10px 14px;background:#fafbfc}\n")
	b.WriteString("summary{font-weight:600;cursor:pointer;padding:4px 0;outline:none}\n")
	b.WriteString(".status-pill{display:inline-block;padding:2px 8px;border-radius:12px;font-size:12px;font-weight:600;text-transform:uppercase}\n")
	b.WriteString(".status-green{background:#dafbe1;color:#1a7f37}\n")
	b.WriteString(".status-yellow{background:#fff8c5;color:#9a6700}\n")
	b.WriteString(".status-unknown{background:#f6f8fa;color:#656d76}\n")
	b.WriteString("</style>\n</head>\n<body>\n")

	b.WriteString("<h1>Extent Bottleneck Report</h1>\n")
	health := healthStatus(report)
	statusClass := "status-" + health
	b.WriteString(fmt.Sprintf("<p><strong>Health status:</strong> <span class=\"status-pill %s\">%s</span></p>\n", statusClass, html.EscapeString(health)))
	b.WriteString(fmt.Sprintf("<p><strong>Window:</strong> last %s</p>\n", html.EscapeString(window)))
	if report.ServiceName != "" {
		b.WriteString(fmt.Sprintf("<p><strong>Target service:</strong> <code>%s</code></p>\n", html.EscapeString(report.ServiceName)))
	}
	if report.SlowestPath != "" {
		b.WriteString(fmt.Sprintf("<p><strong>Slowest path:</strong> <code>%s</code></p>\n", html.EscapeString(report.SlowestPath)))
	}

	b.WriteString("<h2>Executive Summary</h2>\n")
	b.WriteString(fmt.Sprintf("<p>%s</p>\n", html.EscapeString(report.Summary)))

	b.WriteString("<h2>Measurements</h2>\n")
	b.WriteString("<table>\n<thead>\n<tr><th>Metric</th><th>Status</th><th>Value / Unit</th><th>Scope</th><th>Source</th></tr>\n</thead>\n<tbody>\n")
	for _, m := range orderedMeasurements(report.Measurements) {
		b.WriteString(fmt.Sprintf("<tr><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td>%s</td></tr>\n",
			html.EscapeString(m.label),
			html.EscapeString(m.measurement.Status),
			html.EscapeString(formatMeasurementValue(m.measurement)),
			html.EscapeString(m.measurement.Scope),
			html.EscapeString(m.measurement.Source),
		))
	}
	b.WriteString("</tbody>\n</table>\n")

	if len(report.Evidence.Provenance) > 0 {
		b.WriteString("<h3>Query Provenance</h3>\n")
		b.WriteString("<table>\n<thead>\n<tr><th>Source</th><th>Scope</th><th>Status</th><th>Service</th><th>Window</th><th>Query</th></tr>\n</thead>\n<tbody>\n")
		for _, source := range report.Evidence.Provenance {
			b.WriteString(fmt.Sprintf("<tr><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td><code>%s</code></td></tr>\n",
				html.EscapeString(source.Source),
				html.EscapeString(source.Scope),
				html.EscapeString(source.Status),
				html.EscapeString(source.ServiceName),
				html.EscapeString(source.Window),
				html.EscapeString(source.Query),
			))
		}
		b.WriteString("</tbody>\n</table>\n")
	}

	if report.SoakResult != nil {
		b.WriteString("<h2>Sustained Soak Performance</h2>\n")
		concurrency := report.SoakResult.Concurrency
		if concurrency <= 0 {
			concurrency = 1
		}
		b.WriteString("<ul>\n")
		b.WriteString(fmt.Sprintf("<li><strong>Duration:</strong> %s</li>\n", html.EscapeString(report.SoakResult.DurationElapsed.Round(time.Millisecond).String())))
		b.WriteString(fmt.Sprintf("<li><strong>Concurrency:</strong> %d</li>\n", concurrency))
		b.WriteString(fmt.Sprintf("<li><strong>Total requests:</strong> %d</li>\n", report.SoakResult.TotalRequests))
		b.WriteString(fmt.Sprintf("<li><strong>Throughput:</strong> %.1f RPS</li>\n", report.SoakResult.RPS))
		b.WriteString("</ul>\n")
		if len(report.SoakResult.StatusDistribution) > 0 {
			b.WriteString("<h3>Status Distribution</h3>\n")
			b.WriteString("<table>\n<thead>\n<tr><th>Status Code</th><th>Count</th></tr>\n</thead>\n<tbody>\n")
			codes := make([]int, 0, len(report.SoakResult.StatusDistribution))
			for code := range report.SoakResult.StatusDistribution {
				codes = append(codes, code)
			}
			sort.Ints(codes)
			for _, code := range codes {
				b.WriteString(fmt.Sprintf("<tr><td>%d</td><td>%d</td></tr>\n", code, report.SoakResult.StatusDistribution[code]))
			}
			b.WriteString("</tbody>\n</table>\n")
		}
		if report.Comparison != nil && report.Comparison.LoadSummary != "" {
			b.WriteString(fmt.Sprintf("<p><strong>Baseline Comparison:</strong> %s</p>\n", html.EscapeString(report.Comparison.LoadSummary)))
		}
	}

	b.WriteString("<h2>Evidence Summary</h2>\n")
	b.WriteString("<ul>\n")
	b.WriteString(fmt.Sprintf("<li>Traces: %d</li>\n", len(report.Evidence.Traces)))
	b.WriteString(fmt.Sprintf("<li>Spans: %d</li>\n", len(report.Evidence.Spans)))
	b.WriteString(fmt.Sprintf("<li>Target metrics: %d</li>\n", len(report.Evidence.Metrics)))
	b.WriteString(fmt.Sprintf("<li>Log events: %d</li>\n", len(report.Evidence.Logs)))
	b.WriteString(fmt.Sprintf("<li>Error/warning anomalies: %d</li>\n", len(report.Evidence.LogAnomalies)))
	b.WriteString(fmt.Sprintf("<li>Database findings: %d</li>\n", len(report.Evidence.DBFindings)))
	b.WriteString(fmt.Sprintf("<li>External HTTP findings: %d</li>\n", len(report.Evidence.ExternalHTTP)))
	b.WriteString(fmt.Sprintf("<li>Queue findings: %d</li>\n", len(report.Evidence.QueueFindings)))
	b.WriteString("</ul>\n")

	if len(report.Evidence.Traces) > 0 {
		b.WriteString("<h2>Trace Examples</h2>\n")
		b.WriteString("<table>\n<thead>\n<tr><th>Trace ID</th><th>Service</th><th>Root Operation</th><th>Duration</th></tr>\n</thead>\n<tbody>\n")
		for _, trace := range report.Evidence.Traces {
			b.WriteString(fmt.Sprintf("<tr><td><code>%s</code></td><td>%s</td><td><code>%s</code></td><td>%.0fms</td></tr>\n",
				html.EscapeString(trace.TraceID),
				html.EscapeString(trace.RootServiceName),
				html.EscapeString(trace.RootTraceName),
				trace.DurationMS,
			))
		}
		b.WriteString("</tbody>\n</table>\n")
	} else {
		b.WriteString("<h2>Trace Examples</h2>\n<p>No target-service traces were returned.</p>\n")
	}

	if len(report.Evidence.DBFindings) > 0 {
		b.WriteString("<h2>Database Findings</h2>\n")
		b.WriteString("<table>\n<thead>\n<tr><th>System</th><th>Normalized Statement</th><th>Repeat Count</th><th>Total Duration</th><th>Trace ID</th></tr>\n</thead>\n<tbody>\n")
		for _, finding := range report.Evidence.DBFindings {
			b.WriteString(fmt.Sprintf("<tr><td>%s</td><td><code>%s</code></td><td>%d</td><td>%.1fms</td><td><code>%s</code></td></tr>\n",
				html.EscapeString(finding.System),
				html.EscapeString(finding.NormalizedStatement),
				finding.RepeatCount,
				finding.TotalDurationMS,
				html.EscapeString(finding.TraceID),
			))
		}
		b.WriteString("</tbody>\n</table>\n")
	} else {
		b.WriteString("<h2>Database Findings</h2>\n<p>No database spans or slow/repeated database operations were observed.</p>\n")
	}

	if len(report.Evidence.ExternalHTTP) > 0 {
		b.WriteString("<h2>External HTTP Findings</h2>\n")
		b.WriteString("<table>\n<thead>\n<tr><th>Destination</th><th>Duration</th><th>Trace ID</th></tr>\n</thead>\n<tbody>\n")
		for _, finding := range report.Evidence.ExternalHTTP {
			b.WriteString(fmt.Sprintf("<tr><td><code>%s</code></td><td>%.1fms</td><td><code>%s</code></td></tr>\n",
				html.EscapeString(finding.Destination),
				finding.DurationMS,
				html.EscapeString(finding.TraceID),
			))
		}
		b.WriteString("</tbody>\n</table>\n")
	} else {
		b.WriteString("<h2>External HTTP Findings</h2>\n<p>No external HTTP spans were observed.</p>\n")
	}

	if len(report.Evidence.QueueFindings) > 0 {
		b.WriteString("<h2>Queue Findings</h2>\n")
		b.WriteString("<table>\n<thead>\n<tr><th>System</th><th>Name</th><th>Duration</th><th>Trace ID</th></tr>\n</thead>\n<tbody>\n")
		for _, finding := range report.Evidence.QueueFindings {
			b.WriteString(fmt.Sprintf("<tr><td>%s</td><td><code>%s</code></td><td>%.1fms</td><td><code>%s</code></td></tr>\n",
				html.EscapeString(finding.System),
				html.EscapeString(finding.Name),
				finding.DurationMS,
				html.EscapeString(finding.TraceID),
			))
		}
		b.WriteString("</tbody>\n</table>\n")
	} else {
		b.WriteString("<h2>Queue Findings</h2>\n<p>No queue or messaging spans were observed.</p>\n")
	}

	if len(report.Evidence.Metrics) > 0 {
		b.WriteString("<h2>Metric Evidence</h2>\n")
		b.WriteString("<table>\n<thead>\n<tr><th>Metric Name</th><th>Value / Unit</th><th>Labels</th></tr>\n</thead>\n<tbody>\n")
		for _, metric := range report.Evidence.Metrics {
			unit := metric.Unit
			if unit == "" {
				unit = "value"
			}
			labels := formatMetricLabels(metric.Metric)
			b.WriteString(fmt.Sprintf("<tr><td><code>%s</code></td><td>%.3f %s</td><td>%s</td></tr>\n",
				html.EscapeString(metric.Name),
				metric.Value,
				html.EscapeString(unit),
				html.EscapeString(labels),
			))
		}
		b.WriteString("</tbody>\n</table>\n")
	} else {
		b.WriteString("<h2>Metric Evidence</h2>\n<p>No target-service metric samples were returned.</p>\n")
	}

	if len(report.Evidence.Spans) > 0 {
		b.WriteString(fmt.Sprintf("<details>\n<summary>Trace Span Details (%d)</summary>\n", len(report.Evidence.Spans)))
		b.WriteString("<table>\n<thead>\n<tr><th>Span Name</th><th>Kind</th><th>Duration</th><th>Trace ID</th><th>Span ID</th><th>Context</th></tr>\n</thead>\n<tbody>\n")
		for _, span := range report.Evidence.Spans {
			b.WriteString(fmt.Sprintf("<tr><td><code>%s</code></td><td>%s</td><td>%.1fms</td><td><code>%s</code></td><td><code>%s</code></td><td>%s</td></tr>\n",
				html.EscapeString(span.Name),
				html.EscapeString(span.Kind),
				span.DurationMS,
				html.EscapeString(span.TraceID),
				html.EscapeString(span.SpanID),
				html.EscapeString(spanContext(span.Attributes)),
			))
		}
		b.WriteString("</tbody>\n</table>\n</details>\n")
	} else {
		b.WriteString("<h2>Trace Span Details</h2>\n<p>No span details were returned for the target traces.</p>\n")
	}

	if len(report.Evidence.Logs) > 0 {
		b.WriteString(fmt.Sprintf("<details>\n<summary>Log Events (%d)</summary>\n", len(report.Evidence.Logs)))
		b.WriteString("<table>\n<thead>\n<tr><th>Level</th><th>Service</th><th>Trace ID</th><th>Span ID</th><th>Request ID</th><th>Message</th></tr>\n</thead>\n<tbody>\n")
		for _, event := range report.Evidence.Logs {
			level := event.Level
			if level == "" {
				level = "info"
			}
			reqID := event.RequestID
			if reqID == "" {
				reqID = "-"
			}
			b.WriteString(fmt.Sprintf("<tr><td>%s</td><td>%s</td><td><code>%s</code></td><td><code>%s</code></td><td><code>%s</code></td><td>%s</td></tr>\n",
				html.EscapeString(level),
				html.EscapeString(event.ServiceName),
				html.EscapeString(event.TraceID),
				html.EscapeString(event.SpanID),
				html.EscapeString(reqID),
				html.EscapeString(event.Message),
			))
		}
		b.WriteString("</tbody>\n</table>\n</details>\n")
	} else {
		b.WriteString("<h2>Log Events</h2>\n<p>No target-service log events were returned.</p>\n")
	}

	b.WriteString("<h3>Error/Warning Anomalies</h3>\n")
	if len(report.Evidence.LogAnomalies) > 0 {
		b.WriteString("<table>\n<thead>\n<tr><th>Service</th><th>Problem</th><th>Level</th><th>Trace ID</th><th>Message</th></tr>\n</thead>\n<tbody>\n")
		for _, anomaly := range report.Evidence.LogAnomalies {
			b.WriteString(fmt.Sprintf("<tr><td>%s</td><td>%s</td><td>%s</td><td><code>%s</code></td><td>%s</td></tr>\n",
				html.EscapeString(anomaly.ServiceName),
				html.EscapeString(anomaly.Problem),
				html.EscapeString(anomaly.Level),
				html.EscapeString(anomaly.TraceID),
				html.EscapeString(anomaly.Message),
			))
		}
		b.WriteString("</tbody>\n</table>\n")
	} else {
		b.WriteString("<p>None observed.</p>\n")
	}

	if report.Comparison != nil {
		b.WriteString("<h2>Before Vs After</h2>\n")
		b.WriteString(fmt.Sprintf("<p>%s</p>\n", html.EscapeString(report.Comparison.Summary)))
	}

	b.WriteString("<h2>Suggested Fixes</h2>\n<ul>\n")
	if len(report.Recommendations) > 0 {
		for _, item := range report.Recommendations {
			b.WriteString(fmt.Sprintf("<li><strong>%s:</strong> %s %s</li>\n", html.EscapeString(item.Title), html.EscapeString(item.Evidence), html.EscapeString(item.Suggestion)))
		}
	} else {
		for _, suggestion := range suggestions(report) {
			b.WriteString(fmt.Sprintf("<li>%s</li>\n", html.EscapeString(suggestion)))
		}
	}
	b.WriteString("</ul>\n")

	if len(report.Warnings) > 0 {
		b.WriteString("<h2>Verification Warnings</h2>\n<ul>\n")
		for _, warning := range report.Warnings {
			b.WriteString(fmt.Sprintf("<li>%s</li>\n", html.EscapeString(warning)))
		}
		b.WriteString("</ul>\n")
	}

	if report.ApplicationData != nil {
		if data, err := json.MarshalIndent(report.ApplicationData, "", "  "); err == nil {
			b.WriteString("<details>\n<summary>Gathered Application Data</summary>\n")
			b.WriteString(fmt.Sprintf("<pre><code>%s</code></pre>\n</details>\n", html.EscapeString(string(data))))
		}
	}

	b.WriteString("</body>\n</html>\n")
	return b.String()
}

func healthStatus(report Report) string {
	if metric, ok := report.Measurements["httpP95"]; ok && metric.Status != "measured" {
		return "yellow"
	}
	if report.OK {
		return "green"
	}
	if len(report.Warnings) > 0 {
		return "yellow"
	}
	return "unknown"
}

func spanContext(attributes map[string]string) string {
	parts := make([]string, 0, 6)
	for _, item := range []struct {
		label string
		keys  []string
	}{
		{label: "method", keys: []string{"http.request.method", "http.method"}},
		{label: "route", keys: []string{"http.route", "http.target"}},
		{label: "status", keys: []string{"http.response.status_code", "http.status_code"}},
		{label: "db", keys: []string{"db.system", "db.system.name"}},
		{label: "destination", keys: []string{"server.address"}},
		{label: "messaging", keys: []string{"messaging.system"}},
	} {
		for _, key := range item.keys {
			if value := strings.TrimSpace(attributes[key]); value != "" {
				parts = append(parts, item.label+"="+safeInline(value))
				break
			}
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return " (" + strings.Join(parts, ", ") + ")"
}

func formatMetricLabels(labels map[string]string) string {
	if len(labels) == 0 {
		return ""
	}
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+safeInline(labels[key]))
	}
	return " (" + strings.Join(parts, ", ") + ")"
}

func safeTableCell(value string) string {
	value = strings.ReplaceAll(value, "\r\n", " ")
	value = strings.ReplaceAll(value, "\r", " ")
	value = strings.ReplaceAll(value, "\n", " ")
	value = strings.ReplaceAll(value, "\t", " ")
	value = strings.ReplaceAll(value, "|", "\\|")
	value = strings.ReplaceAll(value, "`", "'")
	value = strings.TrimSpace(value)
	if value == "" {
		return "-"
	}
	return value
}

func safeInline(value string) string {
	value = strings.ReplaceAll(value, "\r\n", " ")
	value = strings.ReplaceAll(value, "\r", " ")
	value = strings.ReplaceAll(value, "\n", " ")
	value = strings.ReplaceAll(value, "\t", " ")
	value = strings.ReplaceAll(value, "`", "'")
	return strings.TrimSpace(value)
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
	snapshot := baseline.Snapshot{
		ServiceName:  report.ServiceName,
		Measurements: measurements,
	}
	if report.SoakResult != nil && report.SoakResult.TotalRequests > 0 {
		var errCount int
		for code, count := range report.SoakResult.StatusDistribution {
			if code >= 400 {
				errCount += count
			}
		}
		errorRate := float64(errCount) / float64(report.SoakResult.TotalRequests)
		concurrency := report.SoakResult.Concurrency
		if concurrency <= 0 {
			concurrency = 1
		}
		snapshot.LoadProfile = &baseline.LoadProfile{
			DurationSeconds: report.SoakResult.DurationElapsed.Seconds(),
			Concurrency:     concurrency,
			ThroughputRPS:   report.SoakResult.RPS,
			TotalRequests:   report.SoakResult.TotalRequests,
			ErrorRate:       errorRate,
			P99LatencyMS:    report.SlowestPathLatency,
		}
	}
	return snapshot
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
