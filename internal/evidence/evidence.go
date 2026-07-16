package evidence

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	PrometheusURL string
	LokiURL       string
	TempoURL      string
	Window        string
}

type Result struct {
	Traces        []TraceExample `json:"traces,omitempty"`
	Spans         []SpanEvidence `json:"spans,omitempty"`
	DBFindings    []DBFinding    `json:"dbFindings,omitempty"`
	ExternalHTTP  []HTTPFinding  `json:"externalHttp,omitempty"`
	QueueFindings []QueueFinding `json:"queueFindings,omitempty"`
	LogAnomalies  []LogAnomaly   `json:"logAnomalies,omitempty"`
	Metrics       []MetricSample `json:"metrics,omitempty"`
	Warnings      []string       `json:"warnings,omitempty"`
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
	client := &http.Client{Timeout: 5 * time.Second}
	var result Result
	traces, err := queryTempo(client, config.TempoURL)
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
	logs, err := queryLoki(client, config.LokiURL)
	if err != nil {
		result.Warnings = append(result.Warnings, "loki evidence query failed: "+err.Error())
	} else {
		result.LogAnomalies = logs
	}
	metrics, err := queryPrometheus(client, config.PrometheusURL)
	if err != nil {
		result.Warnings = append(result.Warnings, "prometheus evidence query failed: "+err.Error())
	} else {
		result.Metrics = metrics
	}
	return result
}

func queryTempoTrace(client *http.Client, baseURL, traceID string) ([]SpanEvidence, error) {
	if traceID == "" {
		return nil, nil
	}
	endpoint, err := url.Parse(baseURL)
	if err != nil {
		return nil, err
	}
	endpoint.Path = "/api/traces/" + traceID
	resp, err := client.Get(endpoint.String())
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
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
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
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

func queryTempo(client *http.Client, baseURL string) ([]TraceExample, error) {
	endpoint, err := url.Parse(baseURL)
	if err != nil {
		return nil, err
	}
	endpoint.Path = "/api/search"
	values := endpoint.Query()
	values.Set("limit", "5")
	endpoint.RawQuery = values.Encode()
	resp, err := client.Get(endpoint.String())
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var payload struct {
		Traces []struct {
			TraceID         string  `json:"traceID"`
			RootServiceName string  `json:"rootServiceName"`
			RootTraceName   string  `json:"rootTraceName"`
			DurationMS      float64 `json:"durationMs"`
		} `json:"traces"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
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

func queryLoki(client *http.Client, baseURL string) ([]LogAnomaly, error) {
	endpoint, err := url.Parse(baseURL)
	if err != nil {
		return nil, err
	}
	endpoint.Path = "/loki/api/v1/query_range"
	values := endpoint.Query()
	values.Set("query", `{level=~"error|warn"} |= ""`)
	values.Set("limit", "20")
	endpoint.RawQuery = values.Encode()
	resp, err := client.Get(endpoint.String())
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var payload struct {
		Data struct {
			Result []struct {
				Stream map[string]string `json:"stream"`
				Values [][]string        `json:"values"`
			} `json:"result"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
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

func queryPrometheus(client *http.Client, baseURL string) ([]MetricSample, error) {
	sample, err := prometheusFirst(client, baseURL, `topk(1, histogram_quantile(0.95, sum(rate(http_server_duration_milliseconds_bucket[5m])) by (le, route, path)))`)
	if err != nil {
		return nil, err
	}
	if sample.Name == "" {
		return nil, nil
	}
	return []MetricSample{sample}, nil
}

func prometheusFirst(client *http.Client, baseURL, expr string) (MetricSample, error) {
	endpoint, err := url.Parse(baseURL)
	if err != nil {
		return MetricSample{}, err
	}
	endpoint.Path = "/api/v1/query"
	values := endpoint.Query()
	values.Set("query", expr)
	endpoint.RawQuery = values.Encode()
	resp, err := client.Get(endpoint.String())
	if err != nil {
		return MetricSample{}, err
	}
	defer resp.Body.Close()
	var payload struct {
		Status string `json:"status"`
		Data   struct {
			Result []struct {
				Metric map[string]string `json:"metric"`
				Value  []any             `json:"value"`
			} `json:"result"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return MetricSample{}, err
	}
	if payload.Status != "success" || len(payload.Data.Result) == 0 || len(payload.Data.Result[0].Value) < 2 {
		return MetricSample{}, nil
	}
	raw, _ := payload.Data.Result[0].Value[1].(string)
	value, _ := strconv.ParseFloat(raw, 64)
	return MetricSample{Name: "http_p95_latency", Metric: payload.Data.Result[0].Metric, Value: value}, nil
}
