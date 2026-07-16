package scorer

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

type Config struct {
	PrometheusURL string
}

type Score struct {
	Total       int              `json:"total"`
	Summary     string           `json:"summary"`
	Dimensions  []DimensionScore `json:"dimensions"`
	Suggestions []string         `json:"suggestions"`
}

type DimensionScore struct {
	Name   string `json:"name"`
	Score  int    `json:"score"`
	Detail string `json:"detail"`
}

func Build(config Config) Score {
	if config.PrometheusURL == "" {
		config.PrometheusURL = "http://localhost:9090"
	}
	client := &http.Client{Timeout: 5 * time.Second}
	dimensions := []DimensionScore{
		check(client, config.PrometheusURL, "coverage", `sum(otelcol_receiver_accepted_spans)`, "traces are reaching the Collector"),
		check(client, config.PrometheusURL, "metrics", `sum(otelcol_receiver_accepted_metric_points)`, "metrics are reaching the Collector"),
		check(client, config.PrometheusURL, "logs", `sum(otelcol_receiver_accepted_log_records)`, "logs are reaching the Collector"),
		check(client, config.PrometheusURL, "host/container", `up{job=~"cadvisor|node-exporter"}`, "infrastructure exporters are scraped"),
		check(client, config.PrometheusURL, "correlation readiness", `sum(otelcol_receiver_accepted_spans)`, "trace IDs can be used for log/metric links"),
	}
	total := 0
	for _, dimension := range dimensions {
		total += dimension.Score
	}
	total = total / len(dimensions)
	suggestions := []string{}
	for _, dimension := range dimensions {
		if dimension.Score < 100 {
			suggestions = append(suggestions, "Improve "+dimension.Name+": "+dimension.Detail)
		}
	}
	return Score{
		Total:       total,
		Summary:     fmt.Sprintf("Telemetry Quality Score: %d/100", total),
		Dimensions:  dimensions,
		Suggestions: suggestions,
	}
}

func check(client *http.Client, baseURL, name, expr, okDetail string) DimensionScore {
	value, err := query(client, baseURL, expr)
	if err != nil {
		return DimensionScore{Name: name, Score: 0, Detail: err.Error()}
	}
	if value > 0 {
		return DimensionScore{Name: name, Score: 100, Detail: okDetail}
	}
	return DimensionScore{Name: name, Score: 35, Detail: "signal missing or not yet scraped"}
}

func query(client *http.Client, baseURL, expr string) (float64, error) {
	endpoint, err := url.Parse(baseURL)
	if err != nil {
		return 0, err
	}
	endpoint.Path = "/api/v1/query"
	values := endpoint.Query()
	values.Set("query", expr)
	endpoint.RawQuery = values.Encode()
	resp, err := client.Get(endpoint.String())
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	var payload struct {
		Status string `json:"status"`
		Data   struct {
			Result []struct {
				Value []any `json:"value"`
			} `json:"result"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
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
