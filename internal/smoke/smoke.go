package smoke

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

type Config struct {
	URL           string
	PrometheusURL string
	Requests      int
}

type Report struct {
	OK     bool    `json:"ok"`
	Checks []Check `json:"checks"`
}

type Check struct {
	Name   string  `json:"name"`
	OK     bool    `json:"ok"`
	Value  float64 `json:"value,omitempty"`
	Detail string  `json:"detail,omitempty"`
}

func Run(config Config) Report {
	if config.PrometheusURL == "" {
		config.PrometheusURL = "http://localhost:9090"
	}
	if config.Requests <= 0 {
		config.Requests = 3
	}
	client := &http.Client{Timeout: 5 * time.Second}
	var checks []Check
	for i := 0; i < config.Requests; i++ {
		resp, err := client.Get(config.URL)
		if err != nil {
			return Report{OK: false, Checks: []Check{{Name: "app request", OK: false, Detail: err.Error()}}}
		}
		resp.Body.Close()
	}
	checks = append(checks, Check{Name: "app requests", OK: true, Value: float64(config.Requests)})

	for _, query := range []struct {
		name string
		expr string
	}{
		{name: "traces accepted", expr: "sum(otelcol_receiver_accepted_spans)"},
		{name: "logs accepted", expr: "sum(otelcol_receiver_accepted_log_records)"},
		{name: "metrics accepted", expr: "sum(otelcol_receiver_accepted_metric_points)"},
	} {
		value, err := queryPrometheus(client, config.PrometheusURL, query.expr)
		check := Check{Name: query.name, Value: value}
		if err != nil {
			check.Detail = err.Error()
		}
		check.OK = err == nil && value > 0
		checks = append(checks, check)
	}

	ok := true
	for _, check := range checks {
		if !check.OK {
			ok = false
			break
		}
	}
	return Report{OK: ok, Checks: checks}
}

func queryPrometheus(client *http.Client, baseURL, expr string) (float64, error) {
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
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, fmt.Errorf("prometheus returned %s", resp.Status)
	}

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

