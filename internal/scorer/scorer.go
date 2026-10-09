package scorer

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Anubisx404/Extent/internal/observability"
)

type Config struct {
	PrometheusURL string
	// ServiceName enables the service-scoped dimensions. Without it those
	// dimensions are reported as unknown and excluded from the score.
	ServiceName string
	// TempoURL is optional. When set, a correlated log's trace ID is resolved
	// in Tempo and the result is reported as detail only; it does not change
	// the score.
	TempoURL string
	// LokiURL enables the log/trace correlation dimension. Unset means the
	// dimension is unavailable.
	LokiURL string
	// Lookback is the Loki correlation window. Empty means 15m.
	Lookback string
}

type Score struct {
	Total       int              `json:"total"`
	Coverage    float64          `json:"coverage"`
	Summary     string           `json:"summary"`
	Dimensions  []DimensionScore `json:"dimensions"`
	Suggestions []string         `json:"suggestions"`
}

type DimensionScore struct {
	Name   string `json:"name"`
	Score  int    `json:"score"`
	Status string `json:"status"`
	Detail string `json:"detail"`
	Query  string `json:"query,omitempty"`
}

const (
	identityDimension    = "Service identity"
	correlationDimension = "Log/trace correlation"
)

func Build(config Config) Score {
	if config.PrometheusURL == "" {
		config.PrometheusURL = "http://localhost:9090"
	}
	if config.Lookback == "" {
		config.Lookback = "15m"
	}
	client := &http.Client{Timeout: 5 * time.Second}
	service := strings.TrimSpace(config.ServiceName)

	identity := DimensionScore{Name: identityDimension, Status: "unknown", Detail: "global Collector counters do not prove telemetry for the requested service; pass --service to measure it"}
	correlation := DimensionScore{Name: correlationDimension, Status: "unknown", Detail: "correlation requires matched service-specific logs and traces; pass --service and --loki to measure it"}
	if service != "" {
		identity = check(client, config.PrometheusURL, identityDimension,
			fmt.Sprintf(`sum(http_server_duration_milliseconds_count{service_name=%s}) > 0`, strconv.Quote(service)),
			"service-labeled HTTP server metrics exist for "+service)
		correlation = correlationCheck(client, config, service)
	}

	dimensions := []DimensionScore{
		check(client, config.PrometheusURL, "Prometheus endpoint", `up`, "Prometheus returned measured targets"),
		check(client, config.PrometheusURL, "Collector trace ingestion", `sum(otelcol_receiver_accepted_spans)`, "the Collector has accepted spans globally"),
		check(client, config.PrometheusURL, "Collector metric ingestion", `sum(otelcol_receiver_accepted_metric_points)`, "the Collector has accepted metric points globally"),
		check(client, config.PrometheusURL, "Collector log ingestion", `sum(otelcol_receiver_accepted_log_records)`, "the Collector has accepted log records globally"),
		optionalHostMetrics(check(client, config.PrometheusURL, "Optional host metrics", `up{job=~"cadvisor|node-exporter"}`, "infrastructure exporters are scraped")),
		identity,
		correlation,
	}
	// Unknown, unavailable, and other non-measured dimensions are excluded from
	// both the numerator and the denominator; only measured or missing
	// dimensions contribute to the total.
	total := 0
	scored := 0
	for _, dimension := range dimensions {
		if dimension.Status == "measured" || dimension.Status == "missing" {
			total += dimension.Score
			scored++
		}
	}
	if scored > 0 {
		total /= scored
	}
	coverage := float64(scored) / float64(len(dimensions))
	suggestions := []string{}
	for _, dimension := range dimensions {
		if dimension.Status != "measured" {
			suggestions = append(suggestions, "Improve "+dimension.Name+": "+dimension.Detail)
		}
	}
	return Score{
		Total:       total,
		Coverage:    coverage,
		Summary:     fmt.Sprintf("Telemetry Quality Score: %d/100 across %d/%d measured dimensions (unknown and unavailable dimensions are excluded from the score)", total, scored, len(dimensions)),
		Dimensions:  dimensions,
		Suggestions: suggestions,
	}
}

func check(client *http.Client, baseURL, name, expr, okDetail string) DimensionScore {
	value, err := promValue(client, baseURL, expr)
	if err != nil {
		return DimensionScore{Name: name, Status: "unavailable", Detail: err.Error(), Query: expr}
	}
	if value > 0 {
		return DimensionScore{Name: name, Score: 100, Status: "measured", Detail: okDetail, Query: expr}
	}
	return DimensionScore{Name: name, Score: 0, Status: "missing", Detail: "query succeeded but returned no positive samples", Query: expr}
}

// correlationCheck verifies that the service has at least one log line with a
// non-empty trace_id in the lookback window.
func correlationCheck(client *http.Client, config Config, service string) DimensionScore {
	expr := fmt.Sprintf(`{service_name=%s} | trace_id != ""`, strconv.Quote(service))
	if strings.TrimSpace(config.LokiURL) == "" {
		return DimensionScore{Name: correlationDimension, Status: "unavailable", Detail: "--loki is not set; log/trace correlation was not measured", Query: expr}
	}
	window, err := time.ParseDuration(config.Lookback)
	if err != nil || window <= 0 {
		return DimensionScore{Name: correlationDimension, Status: "unavailable", Detail: fmt.Sprintf("invalid lookback %q", config.Lookback), Query: expr}
	}
	traceID, found, err := lokiTraceMatch(client, config.LokiURL, expr, window)
	if err != nil {
		return DimensionScore{Name: correlationDimension, Status: "unavailable", Detail: err.Error(), Query: expr}
	}
	if !found {
		return DimensionScore{Name: correlationDimension, Score: 0, Status: "missing", Detail: "no " + service + " log with a non-empty trace_id in the last " + config.Lookback, Query: expr}
	}
	detail := "a " + service + " log carries a non-empty trace_id"
	if traceID != "" && strings.TrimSpace(config.TempoURL) != "" {
		detail += "; " + tempoDetail(client, config.TempoURL, traceID)
	}
	return DimensionScore{Name: correlationDimension, Score: 100, Status: "measured", Detail: detail, Query: expr}
}

// lokiTraceMatch returns whether any matching log exists, plus the trace_id of
// that log when it can be read from stream labels or a JSON line.
func lokiTraceMatch(client *http.Client, baseURL, expr string, window time.Duration) (string, bool, error) {
	now := time.Now()
	values := url.Values{}
	values.Set("query", expr)
	values.Set("limit", "1")
	values.Set("start", strconv.FormatInt(now.Add(-window).UnixNano(), 10))
	values.Set("end", strconv.FormatInt(now.UnixNano(), 10))
	var payload struct {
		Status string `json:"status"`
		Data   struct {
			Result []struct {
				Stream map[string]string `json:"stream"`
				Values [][]string        `json:"values"`
			} `json:"result"`
		} `json:"data"`
	}
	if err := getJSON(client, baseURL, "/loki/api/v1/query_range", values, &payload); err != nil {
		return "", false, err
	}
	if payload.Status != "success" {
		return "", false, fmt.Errorf("loki returned status %q", payload.Status)
	}
	if len(payload.Data.Result) == 0 {
		return "", false, nil
	}
	traceID := payload.Data.Result[0].Stream["trace_id"]
	if traceID == "" {
		for _, entry := range payload.Data.Result[0].Values {
			if len(entry) < 2 {
				continue
			}
			var structured map[string]any
			if json.Unmarshal([]byte(entry[1]), &structured) == nil {
				traceID, _ = structured["trace_id"].(string)
			}
			if traceID != "" {
				break
			}
		}
	}
	return traceID, true, nil
}

// tempoDetail resolves a correlated trace ID in Tempo. It is informational only.
func tempoDetail(client *http.Client, baseURL, traceID string) string {
	base, err := observability.ValidateURL(baseURL)
	if err != nil {
		return "Tempo lookup skipped: " + err.Error()
	}
	bounded := &observability.Client{HTTP: client, BodyLimit: observability.DefaultBodyLimit}
	if _, err := bounded.Do(context.Background(), http.MethodGet, base, "/api/traces/"+url.PathEscape(traceID), nil, nil); err != nil {
		return fmt.Sprintf("trace %s was not resolvable in Tempo", traceID)
	}
	return fmt.Sprintf("trace %s resolves in Tempo", traceID)
}

func promValue(client *http.Client, baseURL, expr string) (float64, error) {
	var payload struct {
		Status string `json:"status"`
		Data   struct {
			Result []struct {
				Value []any `json:"value"`
			} `json:"result"`
		} `json:"data"`
	}
	if err := getJSON(client, baseURL, "/api/v1/query", url.Values{"query": {expr}}, &payload); err != nil {
		return 0, err
	}
	if payload.Status != "success" || len(payload.Data.Result) == 0 || len(payload.Data.Result[0].Value) < 2 {
		return 0, nil
	}
	raw, ok := payload.Data.Result[0].Value[1].(string)
	if !ok {
		return 0, nil
	}
	return strconv.ParseFloat(raw, 64)
}

func getJSON(client *http.Client, baseURL, path string, values url.Values, target any) error {
	base, err := observability.ValidateURL(baseURL)
	if err != nil {
		return err
	}
	bounded := &observability.Client{HTTP: client, BodyLimit: observability.DefaultBodyLimit}
	response, err := bounded.Do(context.Background(), http.MethodGet, base, path, values, nil)
	if err != nil {
		return err
	}
	return json.Unmarshal(response.Body, target)
}

// optionalHostMetrics keeps a stack without the host-metrics profile from
// losing points: missing exporters are expected there, not a quality gap.
func optionalHostMetrics(dimension DimensionScore) DimensionScore {
	if dimension.Status == "missing" {
		dimension.Status = "unknown"
		dimension.Detail = "no cadvisor or node-exporter targets; enable the host-metrics profile to score this"
	}
	return dimension
}
