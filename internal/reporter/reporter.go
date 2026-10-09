package reporter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
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
