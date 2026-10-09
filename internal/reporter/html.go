package reporter

import (
	"encoding/json"
	"fmt"
	"html"
	"sort"
	"strings"
	"time"
)

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
