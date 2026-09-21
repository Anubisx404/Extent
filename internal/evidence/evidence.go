package evidence

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Anubisx404/Extent/internal/observability"
)

type Config struct {
	PrometheusURL string
	LokiURL       string
	TempoURL      string
	Window        string
	ServiceName   string
}

type Result struct {
	Traces        []TraceExample      `json:"traces,omitempty"`
	Spans         []SpanEvidence      `json:"spans,omitempty"`
	DBFindings    []DBFinding         `json:"dbFindings,omitempty"`
	ExternalHTTP  []HTTPFinding       `json:"externalHttp,omitempty"`
	QueueFindings []QueueFinding      `json:"queueFindings,omitempty"`
	Logs          []LogEvent          `json:"logs,omitempty"`
	LogAnomalies  []LogAnomaly        `json:"logAnomalies,omitempty"`
	Metrics       []MetricSample      `json:"metrics,omitempty"`
	Saturation    *SaturationEvidence `json:"saturation,omitempty"`
	Provenance    []Provenance        `json:"provenance,omitempty"`
	Warnings      []string            `json:"warnings,omitempty"`
}

type SaturationEvidence struct {
	RateLimit429Count    float64 `json:"rateLimit429Count,omitempty"`
	DBPoolWaiting        float64 `json:"dbPoolWaiting,omitempty"`
	DBPoolActive         float64 `json:"dbPoolActive,omitempty"`
	DBPoolIdle           float64 `json:"dbPoolIdle,omitempty"`
	QueueLag             float64 `json:"queueLag,omitempty"`
	MemoryGrowthBytesSec float64 `json:"memoryGrowthBytesSec,omitempty"`
}

type Provenance struct {
	Source      string    `json:"source"`
	Scope       string    `json:"scope"`
	ServiceName string    `json:"serviceName,omitempty"`
	Window      string    `json:"window"`
	Query       string    `json:"query"`
	RetrievedAt time.Time `json:"retrievedAt"`
	Status      string    `json:"status"`
}

type TraceExample struct {
	TraceID         string  `json:"traceId"`
	RootServiceName string  `json:"rootServiceName,omitempty"`
	RootTraceName   string  `json:"rootTraceName,omitempty"`
	DurationMS      float64 `json:"durationMs,omitempty"`
}

type LogAnomaly struct {
	ServiceName string `json:"serviceName,omitempty"`
	Level       string `json:"level,omitempty"`
	TraceID     string `json:"traceId,omitempty"`
	SpanID      string `json:"spanId,omitempty"`
	Message     string `json:"message,omitempty"`
	Problem     string `json:"problem,omitempty"`
}

type LogEvent struct {
	ServiceName string `json:"serviceName,omitempty"`
	Level       string `json:"level,omitempty"`
	TraceID     string `json:"traceId,omitempty"`
	SpanID      string `json:"spanId,omitempty"`
	RequestID   string `json:"requestId,omitempty"`
	Message     string `json:"message,omitempty"`
}

type MetricSample struct {
	Name   string            `json:"name"`
	Metric map[string]string `json:"metric,omitempty"`
	Unit   string            `json:"unit,omitempty"`
	Value  float64           `json:"value"`
}

type SpanEvidence struct {
	TraceID    string            `json:"traceId,omitempty"`
	SpanID     string            `json:"spanId,omitempty"`
	Name       string            `json:"name,omitempty"`
	Kind       string            `json:"kind,omitempty"`
	DurationMS float64           `json:"durationMs,omitempty"`
	Attributes map[string]string `json:"attributes,omitempty"`
}

type DBFinding struct {
	TraceID             string  `json:"traceId,omitempty"`
	System              string  `json:"system,omitempty"`
	NormalizedStatement string  `json:"normalizedStatement,omitempty"`
	RepeatCount         int     `json:"repeatCount"`
	TotalDurationMS     float64 `json:"totalDurationMs"`
}

type HTTPFinding struct {
	TraceID     string  `json:"traceId,omitempty"`
	Destination string  `json:"destination,omitempty"`
	DurationMS  float64 `json:"durationMs"`
}

type QueueFinding struct {
	TraceID    string  `json:"traceId,omitempty"`
	System     string  `json:"system,omitempty"`
	Name       string  `json:"name,omitempty"`
	DurationMS float64 `json:"durationMs"`
}

func Collect(config Config) Result {
	if config.PrometheusURL == "" {
		config.PrometheusURL = "http://localhost:9090"
	}
	if config.LokiURL == "" {
		config.LokiURL = "http://localhost:3100"
	}
	if config.TempoURL == "" {
		config.TempoURL = "http://localhost:3200"
	}
	if config.Window == "" {
		config.Window = "30m"
	}
	window, err := time.ParseDuration(config.Window)
	if err != nil || window <= 0 || window > 30*24*time.Hour {
		return Result{Warnings: []string{"invalid evidence window: " + config.Window}}
	}
	if strings.TrimSpace(config.ServiceName) == "" {
		return Result{Warnings: []string{"service name is required for target-scoped evidence collection"}}
	}
	client := &http.Client{Timeout: 5 * time.Second}
	var result Result
	traces, err := queryTempo(client, config.TempoURL, config.ServiceName, window)
	result.Provenance = append(result.Provenance, provenance("tempo", config, "service trace search", err))
	if err == nil && len(traces) == 0 {
		result.Provenance[len(result.Provenance)-1].Status = "missing"
	}
	if err != nil {
		result.Warnings = append(result.Warnings, "tempo evidence query failed: "+err.Error())
	} else {
		result.Traces = traces
		for _, trace := range traces {
			spans, err := queryTempoTrace(client, config.TempoURL, trace.TraceID)
			if err != nil {
				result.Warnings = append(result.Warnings, "tempo trace detail query failed: "+err.Error())
				continue
			}
			result.Spans = append(result.Spans, spans...)
		}
		result.DBFindings = detectDBFindings(result.Spans)
		result.ExternalHTTP = detectHTTPFindings(result.Spans)
		result.QueueFindings = detectQueueFindings(result.Spans)
	}
	logs, anomalies, err := queryLoki(client, config.LokiURL, config.ServiceName, window)
	result.Provenance = append(result.Provenance, provenance("loki", config, "service logs and error/warning anomalies", err))
	if err == nil && len(logs) == 0 {
		result.Provenance[len(result.Provenance)-1].Status = "missing"
	}
	if err != nil {
		result.Warnings = append(result.Warnings, "loki evidence query failed: "+err.Error())
	} else {
		result.Logs = logs
		result.LogAnomalies = anomalies
	}
	metrics, err := queryPrometheus(client, config.PrometheusURL, config.ServiceName, config.Window)
	result.Provenance = append(result.Provenance, provenance("prometheus", config, "service HTTP p95 latency", err))
	if err == nil && len(metrics) == 0 {
		result.Provenance[len(result.Provenance)-1].Status = "missing"
	}
	if err != nil {
		result.Warnings = append(result.Warnings, "prometheus evidence query failed: "+err.Error())
	} else {
		result.Metrics = metrics
	}
	saturation, satMetrics, satErr := querySaturation(client, config.PrometheusURL, config.ServiceName, config.Window)
	result.Provenance = append(result.Provenance, provenance("prometheus", config, "service saturation queries", satErr))
	if satErr == nil && saturation == nil {
		result.Provenance[len(result.Provenance)-1].Status = "missing"
	}
	if satErr != nil {
		result.Warnings = append(result.Warnings, "prometheus saturation query failed: "+satErr.Error())
	} else {
		result.Saturation = saturation
		result.Metrics = append(result.Metrics, satMetrics...)
	}
	return result
}

func provenance(source string, config Config, query string, err error) Provenance {
	status := "measured"
	if err != nil {
		status = "unavailable"
	}
	return Provenance{Source: source, Scope: "target", ServiceName: config.ServiceName, Window: config.Window, Query: query, RetrievedAt: time.Now().UTC(), Status: status}
}

func queryTempoTrace(client *http.Client, baseURL, traceID string) ([]SpanEvidence, error) {
	if traceID == "" {
		return nil, nil
	}
	if !traceIDPattern.MatchString(traceID) {
		return nil, fmt.Errorf("invalid Tempo trace ID")
	}
	var payload struct {
		Batches []struct {
			ScopeSpans []struct {
				Spans []tempoSpan `json:"spans"`
			} `json:"scopeSpans"`
			InstrumentationLibrarySpans []struct {
				Spans []tempoSpan `json:"spans"`
			} `json:"instrumentationLibrarySpans"`
		} `json:"batches"`
		ResourceSpans []struct {
			ScopeSpans []struct {
				Spans []tempoSpan `json:"spans"`
			} `json:"scopeSpans"`
		} `json:"resourceSpans"`
	}
	if err := getJSON(client, baseURL, "/api/traces/"+traceID, nil, &payload); err != nil {
		return nil, err
	}
	var out []SpanEvidence
	for _, batch := range payload.Batches {
		for _, scope := range batch.ScopeSpans {
			out = append(out, convertTempoSpans(scope.Spans)...)
		}
		for _, scope := range batch.InstrumentationLibrarySpans {
			out = append(out, convertTempoSpans(scope.Spans)...)
		}
	}
	for _, resource := range payload.ResourceSpans {
		for _, scope := range resource.ScopeSpans {
			out = append(out, convertTempoSpans(scope.Spans)...)
		}
	}
	return out, nil
}

type tempoSpan struct {
	TraceID       string           `json:"traceID"`
	SpanID        string           `json:"spanID"`
	Name          string           `json:"name"`
	DurationNanos float64          `json:"durationNanos"`
	Attributes    []tempoAttribute `json:"attributes"`
}

type tempoAttribute struct {
	Key   string     `json:"key"`
	Value tempoValue `json:"value"`
}

type tempoValue struct {
	StringValue string  `json:"stringValue"`
	IntValue    string  `json:"intValue"`
	DoubleValue float64 `json:"doubleValue"`
	BoolValue   bool    `json:"boolValue"`
}

func convertTempoSpans(spans []tempoSpan) []SpanEvidence {
	out := make([]SpanEvidence, 0, len(spans))
	for _, span := range spans {
		attrs := map[string]string{}
		for _, attr := range span.Attributes {
			attrs[attr.Key] = attr.Value.String()
		}
		out = append(out, SpanEvidence{
			TraceID:    span.TraceID,
			SpanID:     span.SpanID,
			Name:       span.Name,
			Kind:       classifySpan(attrs),
			DurationMS: span.DurationNanos / 1_000_000,
			Attributes: attrs,
		})
	}
	return out
}

func (v tempoValue) String() string {
	switch {
	case v.StringValue != "":
		return v.StringValue
	case v.IntValue != "":
		return v.IntValue
	case v.DoubleValue != 0:
		return strconv.FormatFloat(v.DoubleValue, 'f', -1, 64)
	case v.BoolValue:
		return "true"
	default:
		return ""
	}
}

func classifySpan(attrs map[string]string) string {
	switch {
	case attrs["db.system"] != "":
		return "database"
	case attrs["messaging.system"] != "":
		return "queue"
	case attrs["server.address"] != "" || attrs["http.request.method"] != "":
		return "external_http"
	default:
		return "span"
	}
}

func detectDBFindings(spans []SpanEvidence) []DBFinding {
	grouped := map[string]DBFinding{}
	for _, span := range spans {
		if span.Kind != "database" {
			continue
		}
		statement := normalizeStatement(firstNonEmpty(span.Attributes["db.statement"], span.Name))
		key := span.TraceID + "|" + statement
		finding := grouped[key]
		finding.TraceID = span.TraceID
		finding.System = span.Attributes["db.system"]
		finding.NormalizedStatement = statement
		finding.RepeatCount++
		finding.TotalDurationMS += span.DurationMS
		grouped[key] = finding
	}
	var out []DBFinding
	for _, finding := range grouped {
		if finding.RepeatCount > 1 || finding.TotalDurationMS >= 250 {
			out = append(out, finding)
		}
	}
	return out
}

func detectHTTPFindings(spans []SpanEvidence) []HTTPFinding {
	var out []HTTPFinding
	for _, span := range spans {
		if span.Kind == "external_http" {
			out = append(out, HTTPFinding{TraceID: span.TraceID, Destination: span.Attributes["server.address"], DurationMS: span.DurationMS})
		}
	}
	return out
}

func detectQueueFindings(spans []SpanEvidence) []QueueFinding {
	var out []QueueFinding
	for _, span := range spans {
		if span.Kind == "queue" {
			out = append(out, QueueFinding{TraceID: span.TraceID, System: span.Attributes["messaging.system"], Name: span.Name, DurationMS: span.DurationMS})
		}
	}
	return out
}

func normalizeStatement(statement string) string {
	fields := strings.Fields(statement)
	return strings.ToLower(strings.Join(fields, " "))
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

var traceIDPattern = regexp.MustCompile(`^[0-9a-zA-Z_-]{1,64}$`)

func getJSON(client *http.Client, baseURL, path string, q url.Values, target any) error {
	base, err := observability.ValidateURL(baseURL)
	if err != nil {
		return err
	}
	bounded := &observability.Client{HTTP: client, BodyLimit: observability.DefaultBodyLimit}
	response, err := bounded.Do(context.Background(), http.MethodGet, base, path, q, nil)
	if err != nil {
		return err
	}
	if err := json.NewDecoder(bytes.NewReader(response.Body)).Decode(target); err != nil {
		return fmt.Errorf("decode observability response: %w", err)
	}
	return nil
}

func queryTempo(client *http.Client, baseURL, service string, window time.Duration) ([]TraceExample, error) {
	values := url.Values{}
	values.Set("limit", "5")
	values.Set("q", `{ resource.service.name = `+strconv.Quote(service)+` }`)
	now := time.Now()
	values.Set("start", strconv.FormatInt(now.Add(-window).Unix(), 10))
	values.Set("end", strconv.FormatInt(now.Unix(), 10))
	var payload struct {
		Traces []struct {
			TraceID         string  `json:"traceID"`
			RootServiceName string  `json:"rootServiceName"`
			RootTraceName   string  `json:"rootTraceName"`
			DurationMS      float64 `json:"durationMs"`
		} `json:"traces"`
	}
	if err := getJSON(client, baseURL, "/api/search", values, &payload); err != nil {
		return nil, err
	}
	out := make([]TraceExample, 0, len(payload.Traces))
	for _, trace := range payload.Traces {
		out = append(out, TraceExample{
			TraceID:         trace.TraceID,
			RootServiceName: trace.RootServiceName,
			RootTraceName:   trace.RootTraceName,
			DurationMS:      trace.DurationMS,
		})
	}
	return out, nil
}

func queryLoki(client *http.Client, baseURL, service string, window time.Duration) ([]LogEvent, []LogAnomaly, error) {
	queries := []string{
		`{service_name=` + strconv.Quote(service) + `} | http_request_header_x_request_id != ""`,
		`{service_name=` + strconv.Quote(service) + `}`,
	}
	var lastErr error
	for _, query := range queries {
		logs, anomalies, err := queryLokiQuery(client, baseURL, query, window)
		if err != nil {
			lastErr = err
			continue
		}
		if len(logs) > 0 {
			return logs, anomalies, nil
		}
	}
	return nil, nil, lastErr
}

func queryLokiQuery(client *http.Client, baseURL, query string, window time.Duration) ([]LogEvent, []LogAnomaly, error) {
	values := url.Values{}
	values.Set("query", query)
	values.Set("limit", "20")
	now := time.Now()
	values.Set("start", strconv.FormatInt(now.Add(-window).UnixNano(), 10))
	values.Set("end", strconv.FormatInt(now.UnixNano(), 10))
	var payload struct {
		Data struct {
			Result []struct {
				Stream map[string]string `json:"stream"`
				Values [][]string        `json:"values"`
			} `json:"result"`
		} `json:"data"`
	}
	if err := getJSON(client, baseURL, "/loki/api/v1/query_range", values, &payload); err != nil {
		return nil, nil, err
	}
	var logs []LogEvent
	var anomalies []LogAnomaly
	for _, stream := range payload.Data.Result {
		for _, value := range stream.Values {
			if len(value) < 2 {
				continue
			}
			event := LogEvent{
				ServiceName: stream.Stream["service_name"],
				Level:       stream.Stream["level"],
				TraceID:     stream.Stream["trace_id"],
				SpanID:      stream.Stream["span_id"],
				RequestID:   stream.Stream["http_request_header_x_request_id"],
			}
			var structured map[string]any
			if json.Unmarshal([]byte(value[1]), &structured) == nil {
				if event.TraceID == "" {
					event.TraceID, _ = structured["trace_id"].(string)
				}
				if event.SpanID == "" {
					event.SpanID, _ = structured["span_id"].(string)
				}
				if event.RequestID == "" {
					event.RequestID, _ = structured["http_request_header_x_request_id"].(string)
				}
				event.Message, _ = structured["message"].(string)
				if event.Level == "" {
					event.Level, _ = structured["severity_text"].(string)
				}
			} else {
				event.Message = value[1]
			}
			if event.Message == "" {
				event.Message = value[1]
			}
			logs = append(logs, event)
			level := strings.ToLower(strings.TrimSpace(event.Level))
			if level == "error" || level == "warn" || level == "warning" {
				problem := "error_or_warning_log"
				if event.TraceID == "" {
					problem = "log_missing_trace_id"
				}
				anomalies = append(anomalies, LogAnomaly{
					ServiceName: event.ServiceName,
					Level:       event.Level,
					TraceID:     event.TraceID,
					SpanID:      event.SpanID,
					Message:     event.Message,
					Problem:     problem,
				})
			}
		}
	}
	return logs, anomalies, nil
}

func queryPrometheus(client *http.Client, baseURL, service, window string) ([]MetricSample, error) {
	expr := `topk(1, histogram_quantile(0.95, sum(rate(http_server_duration_milliseconds_bucket{service_name=` + strconv.Quote(service) + `}[` + window + `])) by (le, http_route, route, path)))`
	sample, err := prometheusFirst(client, baseURL, expr)
	if err != nil {
		return nil, err
	}
	if sample.Name == "" {
		return nil, nil
	}
	sample.Unit = "milliseconds"
	return []MetricSample{sample}, nil
}

func prometheusFirst(client *http.Client, baseURL, expr string) (MetricSample, error) {
	val, metric, found, err := prometheusScalar(client, baseURL, expr)
	if err != nil {
		return MetricSample{}, err
	}
	if !found {
		return MetricSample{}, nil
	}
	return MetricSample{Name: "http_p95_latency", Metric: metric, Unit: "milliseconds", Value: val}, nil
}

func prometheusScalar(client *http.Client, baseURL, expr string) (float64, map[string]string, bool, error) {
	values := url.Values{}
	values.Set("query", expr)
	var payload struct {
		Status string `json:"status"`
		Data   struct {
			Result []struct {
				Metric map[string]string `json:"metric"`
				Value  []any             `json:"value"`
			} `json:"result"`
		} `json:"data"`
	}
	if err := getJSON(client, baseURL, "/api/v1/query", values, &payload); err != nil {
		return 0, nil, false, err
	}
	if payload.Status != "success" || len(payload.Data.Result) == 0 || len(payload.Data.Result[0].Value) < 2 {
		return 0, nil, false, nil
	}
	raw, ok := payload.Data.Result[0].Value[1].(string)
	if !ok {
		if f, ok := payload.Data.Result[0].Value[1].(float64); ok {
			return f, payload.Data.Result[0].Metric, true, nil
		}
		return 0, nil, false, nil
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, nil, false, err
	}
	return value, payload.Data.Result[0].Metric, true, nil
}

func querySaturation(client *http.Client, baseURL, service, window string) (*SaturationEvidence, []MetricSample, error) {
	serviceQuote := strconv.Quote(service)
	var errs []string

	rateLimit429, _, found429, err := prometheusScalar(client, baseURL, fmt.Sprintf("sum(increase(http_server_duration_milliseconds_count{service_name=%s, status=\"429\"}[%s]))", serviceQuote, window))
	if err != nil || !found429 || rateLimit429 == 0 {
		fallbackVal, _, fallbackFound, fallbackErr := prometheusScalar(client, baseURL, fmt.Sprintf("sum(rate(http_server_duration_milliseconds_count{service_name=%s, status=\"429\"}[%s]))", serviceQuote, window))
		if fallbackErr == nil && fallbackFound && fallbackVal > 0 {
			rateLimit429 = fallbackVal
		} else if err != nil && fallbackErr != nil {
			errs = append(errs, err.Error())
		}
	}

	dbWaiting, _, _, err := prometheusScalar(client, baseURL, fmt.Sprintf("sum(db_client_connections_usage{service_name=%s, state=\"waiting\"})", serviceQuote))
	if err != nil {
		errs = append(errs, err.Error())
	}

	dbActive, _, _, err := prometheusScalar(client, baseURL, fmt.Sprintf("sum(db_client_connections_usage{service_name=%s, state=\"used\"})", serviceQuote))
	if err != nil {
		errs = append(errs, err.Error())
	}

	dbIdle, _, _, err := prometheusScalar(client, baseURL, fmt.Sprintf("sum(db_client_connections_usage{service_name=%s, state=\"idle\"})", serviceQuote))
	if err != nil {
		errs = append(errs, err.Error())
	}

	queueLag, _, _, err := prometheusScalar(client, baseURL, fmt.Sprintf("sum(messaging_client_backlog_messages{service_name=%s})", serviceQuote))
	if err != nil {
		errs = append(errs, err.Error())
	}

	memSlope, _, _, err := prometheusScalar(client, baseURL, fmt.Sprintf("deriv(process_resident_memory_bytes{service_name=%s}[%s])", serviceQuote, window))
	if err != nil {
		errs = append(errs, err.Error())
	}

	var overallErr error
	if len(errs) > 0 {
		overallErr = fmt.Errorf("%s", strings.Join(errs, "; "))
	}

	var sat *SaturationEvidence
	if rateLimit429 > 0 || dbWaiting > 0 || dbActive > 0 || dbIdle > 0 || queueLag > 0 || memSlope != 0 {
		sat = &SaturationEvidence{
			RateLimit429Count:    rateLimit429,
			DBPoolWaiting:        dbWaiting,
			DBPoolActive:         dbActive,
			DBPoolIdle:           dbIdle,
			QueueLag:             queueLag,
			MemoryGrowthBytesSec: memSlope,
		}
	}

	var samples []MetricSample
	if rateLimit429 > 0 {
		samples = append(samples, MetricSample{Name: "rate_limit_429_count", Unit: "count", Value: rateLimit429})
	}
	if dbWaiting > 0 {
		samples = append(samples, MetricSample{Name: "db_pool_waiting", Unit: "connections", Value: dbWaiting})
	}
	if dbActive > 0 {
		samples = append(samples, MetricSample{Name: "db_pool_active", Unit: "connections", Value: dbActive})
	}
	if dbIdle > 0 {
		samples = append(samples, MetricSample{Name: "db_pool_idle", Unit: "connections", Value: dbIdle})
	}
	if queueLag > 0 {
		samples = append(samples, MetricSample{Name: "queue_lag", Unit: "messages", Value: queueLag})
	}
	if memSlope != 0 {
		samples = append(samples, MetricSample{Name: "memory_growth_bytes_sec", Unit: "bytes/sec", Value: memSlope})
	}

	return sat, samples, overallErr
}
