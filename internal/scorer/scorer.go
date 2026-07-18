package scorer

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	PrometheusURL string
	ServiceName   string
	Lookback      string
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

func Build(config Config) Score {
	if config.PrometheusURL == "" {
		config.PrometheusURL = "http://localhost:9090"
	}
	client := &http.Client{Timeout: 5 * time.Second}
	dimensions := []DimensionScore{
		check(client, config.PrometheusURL, "Prometheus endpoint", `up`, "Prometheus returned measured targets"),
		check(client, config.PrometheusURL, "Collector trace ingestion", `sum(otelcol_receiver_accepted_spans)`, "the Collector has accepted spans globally"),
		check(client, config.PrometheusURL, "Collector metric ingestion", `sum(otelcol_receiver_accepted_metric_points)`, "the Collector has accepted metric points globally"),
		check(client, config.PrometheusURL, "Collector log ingestion", `sum(otelcol_receiver_accepted_log_records)`, "the Collector has accepted log records globally"),
		check(client, config.PrometheusURL, "Optional host metrics", `up{job=~"cadvisor|node-exporter"}`, "infrastructure exporters are scraped"),
		{Name: "Service identity", Status: "unknown", Detail: "global Collector counters do not prove telemetry for the requested service"},
		{Name: "Log/trace correlation", Status: "unknown", Detail: "correlation requires matched service-specific logs and traces"},
	}
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
		Summary:     fmt.Sprintf("Telemetry Quality Score: %d/100 across %d/%d measured dimensions", total, scored, len(dimensions)),
		Dimensions:  dimensions,
		Suggestions: suggestions,
	}
}

func check(client *http.Client, baseURL, name, expr, okDetail string) DimensionScore {
	value, err := query(client, baseURL, expr)
	if err != nil {
		return DimensionScore{Name: name, Status: "unavailable", Detail: err.Error(), Query: expr}
	}
	if value > 0 {
		return DimensionScore{Name: name, Score: 100, Status: "measured", Detail: okDetail, Query: expr}
	}
	return DimensionScore{Name: name, Score: 0, Status: "missing", Detail: "query succeeded but returned no positive samples", Query: expr}
}

func query(client *http.Client, baseURL, expr string) (float64, error) {
	endpoint, err := url.Parse(baseURL)
	if err != nil {
		return 0, err
	}
	if endpoint.Scheme != "http" && endpoint.Scheme != "https" || endpoint.Host == "" || endpoint.User != nil {
		return 0, fmt.Errorf("invalid Prometheus URL")
	}
	endpoint.Path = path.Join(strings.TrimSuffix(endpoint.Path, "/"), "api/v1/query")
	values := endpoint.Query()
	values.Set("query", expr)
	endpoint.RawQuery = values.Encode()
	resp, err := client.Get(endpoint.String())
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, fmt.Errorf("Prometheus returned %s", resp.Status)
	}
	var payload struct {
		Status string `json:"status"`
		Data   struct {
			Result []struct {
				Value []any `json:"value"`
			} `json:"result"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&payload); err != nil {
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
