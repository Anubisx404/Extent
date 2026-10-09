package reporter

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

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
