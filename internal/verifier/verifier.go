package verifier

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Root          string
	URL           string
	PrometheusURL string
	LokiURL       string
	TempoURL      string
	GrafanaURL    string
	Requests      int
}

type Report struct {
	OK     bool    `json:"ok"`
	Checks []Check `json:"checks"`
}

type Check struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
}

func Verify(root string) Report {
	return VerifyConfig(Config{Root: root})
}

func VerifyConfig(config Config) Report {
	root := config.Root
	if root == "" {
		root = "."
	}
	checks := []Check{
		fileCheck(root, "docker-compose.observability.yml"),
		fileCheck(root, "otel-collector.yml"),
		fileCheck(root, "tempo.yml"),
		fileCheck(root, "loki.yml"),
		fileCheck(root, "prometheus.yml"),
		fileCheck(root, "prometheus-alerts.yml"),
		fileCheck(root, "extent.yaml"),
		fileCheck(root, ".env.observability"),
		fileCheck(root, "grafana/provisioning/datasources/datasources.yml"),
		fileCheck(root, "grafana/dashboards/service-overview.json"),
		grafanaCorrelationCheck(root),
		commandCheck("docker", "docker CLI available"),
		commandCheck("git", "git CLI available"),
	}
	if config.GrafanaURL != "" {
		checks = append(checks, grafanaAPICorrelationCheck(config.GrafanaURL))
	}
	if config.URL != "" {
		checks = append(checks, stackChecks(config)...)
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

func stackChecks(config Config) []Check {
	if config.PrometheusURL == "" {
		config.PrometheusURL = "http://localhost:9090"
	}
	if config.LokiURL == "" {
		config.LokiURL = "http://localhost:3100"
	}
	if config.TempoURL == "" {
		config.TempoURL = "http://localhost:3200"
	}
	if config.Requests <= 0 {
		config.Requests = 3
	}
	client := &http.Client{Timeout: 5 * time.Second}
	checks := []Check{requestCheck(client, config.URL, config.Requests)}
	checks = append(checks,
		prometheusCheck(client, config.PrometheusURL, "Prometheus received traces", "sum(otelcol_receiver_accepted_spans)"),
		prometheusCheck(client, config.PrometheusURL, "Prometheus received logs", "sum(otelcol_receiver_accepted_log_records)"),
		prometheusCheck(client, config.PrometheusURL, "Prometheus received metrics", "sum(otelcol_receiver_accepted_metric_points)"),
		readyCheck(client, config.LokiURL, "Loki ready"),
		readyCheck(client, config.TempoURL, "Tempo ready"),
	)
	return checks
}

func requestCheck(client *http.Client, target string, count int) Check {
	for i := 0; i < count; i++ {
		resp, err := client.Get(target)
		if err != nil {
			return Check{Name: "synthetic app requests", OK: false, Detail: err.Error()}
		}
		resp.Body.Close()
		if resp.StatusCode >= 500 {
			return Check{Name: "synthetic app requests", OK: false, Detail: fmt.Sprintf("request returned %s", resp.Status)}
		}
	}
	return Check{Name: "synthetic app requests", OK: true, Detail: fmt.Sprintf("%d request(s)", count)}
}

func readyCheck(client *http.Client, baseURL, name string) Check {
	endpoint, err := url.Parse(baseURL)
	if err != nil {
		return Check{Name: name, OK: false, Detail: err.Error()}
	}
	endpoint.Path = "/ready"
	resp, err := client.Get(endpoint.String())
	if err != nil {
		return Check{Name: name, OK: false, Detail: err.Error()}
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Check{Name: name, OK: false, Detail: resp.Status}
	}
	return Check{Name: name, OK: true}
}

func prometheusCheck(client *http.Client, baseURL, name, expr string) Check {
	value, err := queryPrometheus(client, baseURL, expr)
	if err != nil {
		return Check{Name: name, OK: false, Detail: err.Error()}
	}
	if value <= 0 {
		return Check{Name: name, OK: false, Detail: "no samples found"}
	}
	return Check{Name: name, OK: true, Detail: fmt.Sprintf("%.0f sample(s)", value)}
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

func fileCheck(root, rel string) Check {
	path := filepath.Join(root, filepath.FromSlash(rel))
	if _, err := os.Stat(path); err != nil {
		return Check{Name: rel, OK: false, Detail: "missing"}
	}
	return Check{Name: rel, OK: true}
}

func grafanaCorrelationCheck(root string) Check {
	path := filepath.Join(root, "grafana", "provisioning", "datasources", "datasources.yml")
	data, err := os.ReadFile(path)
	if err != nil {
		return Check{Name: "Grafana trace/log/metric correlation", OK: false, Detail: "datasource file missing"}
	}
	text := string(data)
	required := []string{
		"tracesToLogsV2",
		"filterByTraceID: true",
		"filterBySpanID: true",
		"tracesToMetrics",
		"serviceMap",
		"datasourceUid: loki",
		"datasourceUid: prometheus",
	}
	var missing []string
	for _, needle := range required {
		if !strings.Contains(text, needle) {
			missing = append(missing, needle)
		}
	}
	if len(missing) > 0 {
		return Check{Name: "Grafana trace/log/metric correlation", OK: false, Detail: "missing " + strings.Join(missing, ", ")}
	}
	return Check{Name: "Grafana trace/log/metric correlation", OK: true}
}

func grafanaAPICorrelationCheck(baseURL string) Check {
	endpoint, err := url.Parse(baseURL)
	if err != nil {
		return Check{Name: "Grafana API Tempo correlation", OK: false, Detail: err.Error()}
	}
	endpoint.Path = "/api/datasources/uid/tempo"
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(endpoint.String())
	if err != nil {
		return Check{Name: "Grafana API Tempo correlation", OK: false, Detail: err.Error()}
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Check{Name: "Grafana API Tempo correlation", OK: false, Detail: resp.Status}
	}
	var payload struct {
		JSONData struct {
			TracesToLogsV2 struct {
				DatasourceUID   string `json:"datasourceUid"`
				FilterByTraceID bool   `json:"filterByTraceID"`
				FilterBySpanID  bool   `json:"filterBySpanID"`
			} `json:"tracesToLogsV2"`
			TracesToMetrics struct {
				DatasourceUID string `json:"datasourceUid"`
			} `json:"tracesToMetrics"`
			ServiceMap struct {
				DatasourceUID string `json:"datasourceUid"`
			} `json:"serviceMap"`
		} `json:"jsonData"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return Check{Name: "Grafana API Tempo correlation", OK: false, Detail: err.Error()}
	}
	if payload.JSONData.TracesToLogsV2.DatasourceUID != "loki" ||
		!payload.JSONData.TracesToLogsV2.FilterByTraceID ||
		!payload.JSONData.TracesToLogsV2.FilterBySpanID ||
		payload.JSONData.TracesToMetrics.DatasourceUID != "prometheus" ||
		payload.JSONData.ServiceMap.DatasourceUID != "prometheus" {
		return Check{Name: "Grafana API Tempo correlation", OK: false, Detail: "Tempo datasource lacks trace-to-logs, trace-to-metrics, or service-map config"}
	}
	return Check{Name: "Grafana API Tempo correlation", OK: true}
}

func commandCheck(command, name string) Check {
	if _, err := exec.LookPath(command); err != nil {
		return Check{Name: name, OK: false, Detail: "not found in PATH"}
	}
	return Check{Name: name, OK: true}
}
