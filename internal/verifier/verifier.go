package verifier

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Anubisx404/Extent/internal/config"
	"github.com/Anubisx404/Extent/internal/observability"
	"github.com/Anubisx404/Extent/internal/smoke"
	"github.com/Anubisx404/Extent/internal/stack"
)

type Config struct {
	Root              string
	URL               string
	SkipTelemetry     bool
	ExplicitTelemetry bool
	PrometheusURL     string
	LokiURL           string
	TempoURL          string
	GrafanaURL        string
	ServiceName       string
	Requests          int
	GrafanaUser       string
	GrafanaPassword   string
	GrafanaToken      string
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

func VerifyConfig(cfg Config) Report {
	root := cfg.Root
	if root == "" {
		root = "."
	}
	isNoDocker := false
	if data, err := os.ReadFile(filepath.Join(root, "extent.yaml")); err == nil {
		if parsed, parseErr := config.Parse(data); parseErr == nil && parsed.Profile.Name == "no-docker" {
			isNoDocker = true
		}
	}
	if isNoDocker && !cfg.SkipTelemetry && !cfg.ExplicitTelemetry {
		cfg.SkipTelemetry = true
	}
	var checks []Check
	if isNoDocker {
		checks = []Check{
			fileCheck(root, "extent.yaml"),
			fileCheck(root, ".env.observability"),
			commandCheck("git", "git CLI available"),
		}
	} else {
		checks = []Check{
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
	}
	if cfg.GrafanaURL != "" {
		checks = append(checks, grafanaAPICorrelationCheck(cfg.GrafanaURL, cfg))
	}
	if cfg.URL != "" {
		checks = append(checks, stackChecks(cfg)...)
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

func stackChecks(cfg Config) []Check {
	if cfg.PrometheusURL == "" {
		cfg.PrometheusURL = "http://localhost:9090"
	}
	if cfg.LokiURL == "" {
		cfg.LokiURL = "http://localhost:3100"
	}
	if cfg.TempoURL == "" {
		cfg.TempoURL = "http://localhost:3200"
	}
	if cfg.Requests <= 0 {
		cfg.Requests = 3
	}
	smokeReport := smoke.Run(smoke.Config{
		URL:           cfg.URL,
		SkipTelemetry: cfg.SkipTelemetry,
		PrometheusURL: cfg.PrometheusURL,
		TempoURL:      cfg.TempoURL,
		LokiURL:       cfg.LokiURL,
		ServiceName:   cfg.ServiceName,
		Requests:      cfg.Requests,
	})
	checks := make([]Check, 0, len(smokeReport.Checks)+2)
	for _, evidence := range smokeReport.Checks {
		detail := evidence.Detail
		if evidence.Scope != "" {
			detail = "[" + evidence.Scope + "/" + evidence.Status + "] " + detail
		}
		checks = append(checks, Check{Name: evidence.Name, OK: evidence.OK, Detail: detail})
	}
	if cfg.SkipTelemetry {
		return checks
	}
	client := &http.Client{Timeout: 5 * time.Second}
	deadline := time.Now().Add(readyWindow)
	checks = append(checks, readyCheck(client, cfg.LokiURL, "Loki ready", deadline), readyCheck(client, cfg.TempoURL, "Tempo ready", deadline))
	return checks
}

const readyWindow = 60 * time.Second

func readyCheck(client *http.Client, baseURL, name string, deadline time.Time) Check {
	endpoint, err := observability.ValidateURL(baseURL)
	if err != nil {
		return Check{Name: name, OK: false, Detail: err.Error()}
	}
	bounded := &observability.Client{HTTP: client, BodyLimit: observability.DefaultBodyLimit}
	for {
		_, err = bounded.Do(context.Background(), http.MethodGet, endpoint, "/ready", nil, nil)
		if err == nil {
			return Check{Name: name, OK: true}
		}
		if !time.Now().Before(deadline) {
			return Check{Name: name, OK: false, Detail: err.Error()}
		}
		time.Sleep(2 * time.Second)
	}
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

func grafanaAuthHeader(cfg Config) http.Header {
	header := http.Header{}
	if cfg.GrafanaToken != "" {
		header.Set("Authorization", "Bearer "+cfg.GrafanaToken)
		return header
	}
	if token := os.Getenv("EXTENT_GRAFANA_TOKEN"); cfg.GrafanaUser == "" && token != "" {
		header.Set("Authorization", "Bearer "+token)
		return header
	}
	user, password := cfg.GrafanaUser, cfg.GrafanaPassword
	if user == "" {
		user = os.Getenv("EXTENT_GRAFANA_USER")
		if password == "" {
			password = os.Getenv("EXTENT_GRAFANA_PASSWORD")
		}
	}
	if user == "" && password == "" {
		root := cfg.Root
		if root == "" {
			root = "."
		}
		user, password = stack.GrafanaCredentials(root)
	}
	if password == "" {
		return header
	}
	header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(user+":"+password)))
	return header
}

func grafanaAPICorrelationCheck(baseURL string, cfg Config) Check {
	endpoint, err := observability.ValidateURL(baseURL)
	if err != nil {
		return Check{Name: "Grafana API Tempo correlation", OK: false, Detail: err.Error()}
	}
	client := &http.Client{Timeout: 5 * time.Second}
	bounded := &observability.Client{HTTP: client, BodyLimit: observability.DefaultBodyLimit}
	resp, err := bounded.Do(context.Background(), http.MethodGet, endpoint, "/api/datasources/uid/tempo", nil, grafanaAuthHeader(cfg))
	if err != nil {
		return Check{Name: "Grafana API Tempo correlation", OK: false, Detail: err.Error()}
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
	if err := json.NewDecoder(bytes.NewReader(resp.Body)).Decode(&payload); err != nil {
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
