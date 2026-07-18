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
	Traces        []TraceExample `json:"traces,omitempty"`
	Spans         []SpanEvidence `json:"spans,omitempty"`
	DBFindings    []DBFinding    `json:"dbFindings,omitempty"`
	ExternalHTTP  []HTTPFinding  `json:"externalHttp,omitempty"`
	QueueFindings []QueueFinding `json:"queueFindings,omitempty"`
	LogAnomalies  []LogAnomaly   `json:"logAnomalies,omitempty"`
	Metrics       []MetricSample `json:"metrics,omitempty"`
	Provenance    []Provenance   `json:"provenance,omitempty"`
	Warnings      []string       `json:"warnings,omitempty"`
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

type MetricSample struct {
	Name   string            `json:"name"`
	Metric map[string]string `json:"metric,omitempty"`
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
	logs, err := queryLoki(client, config.LokiURL, config.ServiceName, window)
	result.Provenance = append(result.Provenance, provenance("loki", config, "service error/warning logs", err))
	if err == nil && len(logs) == 0 {
		result.Provenance[len(result.Provenance)-1].Status = "missing"
	}
	if err != nil {
		result.Warnings = append(result.Warnings, "loki evidence query failed: "+err.Error())
	} else {
		result.LogAnomalies = logs
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

var traceIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{16,32}$`)

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

func queryLoki(client *http.Client, baseURL, service string, window time.Duration) ([]LogAnomaly, error) {
	values := url.Values{}
	values.Set("query", `{service_name=`+strconv.Quote(service)+`,level=~"error|warn"}`)
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
		return nil, err
	}
	var out []LogAnomaly
	for _, stream := range payload.Data.Result {
		for _, value := range stream.Values {
			if len(value) < 2 {
				continue
			}
			anomaly := LogAnomaly{
				ServiceName: stream.Stream["service_name"],
				Level:       stream.Stream["level"],
				Problem:     "error_or_warning_log",
			}
			var structured map[string]any
			if json.Unmarshal([]byte(value[1]), &structured) == nil {
				anomaly.TraceID, _ = structured["trace_id"].(string)
				anomaly.SpanID, _ = structured["span_id"].(string)
				anomaly.Message, _ = structured["message"].(string)
				if anomaly.TraceID == "" {
					anomaly.Problem = "log_missing_trace_id"
				}
			} else {
				anomaly.Message = value[1]
				if !strings.Contains(value[1], "trace_id") {
					anomaly.Problem = "unstructured_log_missing_trace_id"
				}
			}
			out = append(out, anomaly)
		}
	}
	return out, nil
}

func queryPrometheus(client *http.Client, baseURL, service, window string) ([]MetricSample, error) {
	expr := `topk(1, histogram_quantile(0.95, sum(rate(http_server_request_duration_seconds_bucket{service_name=` + strconv.Quote(service) + `}[` + window + `])) by (le, http_route, route, path)))`
	sample, err := prometheusFirst(client, baseURL, expr)
	if err != nil {
		return nil, err
	}
	if sample.Name == "" {
		return nil, nil
	}
	return []MetricSample{sample}, nil
}

func prometheusFirst(client *http.Client, baseURL, expr string) (MetricSample, error) {
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
		return MetricSample{}, err
	}
	if payload.Status != "success" || len(payload.Data.Result) == 0 || len(payload.Data.Result[0].Value) < 2 {
		return MetricSample{}, nil
	}
	raw, _ := payload.Data.Result[0].Value[1].(string)
	value, _ := strconv.ParseFloat(raw, 64)
	return MetricSample{Name: "http_p95_latency", Metric: payload.Data.Result[0].Metric, Value: value}, nil
}
