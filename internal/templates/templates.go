package templates

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Anubisx404/Extent/internal/config"
	"github.com/Anubisx404/Extent/internal/fileops"
	"github.com/Anubisx404/Extent/internal/planner"
	"github.com/Anubisx404/Extent/internal/stack"
)

type WriteOptions struct {
	Overwrite         bool
	ContractOverwrite bool
	Profile           string
	Contract          string
}

func WriteLGTM(root string, plan planner.Plan, opts WriteOptions) ([]string, error) {
	transaction, err := PlanLGTM(root, plan, opts)
	if err != nil {
		return nil, err
	}
	_, err = fileops.Apply(transaction)
	if err != nil {
		return nil, err
	}
	written := make([]string, 0, len(transaction.Steps))
	for _, step := range transaction.Steps {
		written = append(written, step.Path)
	}
	return written, nil
}

// PlanLGTM renders and preflights the complete stack without mutating the project.
func PlanLGTM(root string, plan planner.Plan, opts WriteOptions) (fileops.Plan, error) {
	profile := strings.ToLower(strings.TrimSpace(opts.Profile))
	resolved, err := stack.ResolveProfile(profile)
	if err != nil {
		return fileops.Plan{}, err
	}
	envPath := filepath.Join(root, stack.EnvFile)
	existingEnv, envExists, err := readOptionalFile(envPath)
	if err != nil {
		return fileops.Plan{}, err
	}
	envContent, addedPassword, err := observabilityEnv(existingEnv, envExists)
	if err != nil {
		return fileops.Plan{}, err
	}
	files := map[string]string{
		"docker-compose.observability.yml": composeYAMLForProfile(resolved),
		"otel-collector.yml":               collectorYAMLForProfile(profile),
		"tempo.yml":                        tempoYAML,
		"loki.yml":                         lokiYAML,
		"prometheus.yml":                   prometheusYAMLForProfile(resolved),
		"prometheus-alerts.yml":            prometheusAlertsYAML,
		"extent.yaml":                      contractOrDefault(plan, opts.Contract),
		stack.EnvFile:                      envContent,
		"grafana/provisioning/datasources/datasources.yml": datasourcesYAML,
		"grafana/provisioning/dashboards/dashboards.yml":   dashboardsYAML,
		"grafana/dashboards/service-overview.json":         dashboardJSON,
		".extent/report.md":                                reportMarkdown(plan),
	}

	planBytes, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return fileops.Plan{}, err
	}
	files[".extent/plan.json"] = string(planBytes) + "\n"

	paths := make([]string, 0, len(files))
	for rel := range files {
		paths = append(paths, rel)
	}
	sort.Strings(paths)

	steps := make([]fileops.Step, 0, len(paths))
	for _, rel := range paths {
		content := files[rel]
		if content == "" {
			return fileops.Plan{}, errors.New("empty generated output: " + rel)
		}
		target := filepath.Join(root, filepath.FromSlash(rel))
		action := fileops.Create
		if existing, readErr := os.ReadFile(target); readErr == nil {
			if string(existing) == content {
				continue
			}
			// Adding a generated password to an env file written by an older apply
			// only appends a line; it never replaces what the user already set.
			authorized := opts.Overwrite || rel == "extent.yaml" && opts.ContractOverwrite || rel == stack.EnvFile && addedPassword
			if !authorized {
				return fileops.Plan{}, errors.New("refusing to overwrite existing file: " + rel)
			}
			action = fileops.Update
		} else if !os.IsNotExist(readErr) {
			return fileops.Plan{}, readErr
		}
		steps = append(steps, fileops.Step{Path: rel, Action: action, Data: []byte(content), Mode: 0644})
	}
	return fileops.NewPlan(root, "stack-config", steps)
}

func reportMarkdown(plan planner.Plan) string {
	var b strings.Builder
	b.WriteString("# Extent Report\n\n")
	b.WriteString("Generated observability stack files for:\n\n")
	for _, detected := range plan.Detected {
		b.WriteString("- " + detected + "\n")
	}
	if len(plan.Warnings) > 0 {
		b.WriteString("\n## Warnings\n\n")
		for _, warning := range plan.Warnings {
			b.WriteString("- " + warning + "\n")
		}
	}
	b.WriteString("\n## Run\n\n")
	b.WriteString("```powershell\n")
	b.WriteString("docker compose -f docker-compose.observability.yml up -d\n")
	b.WriteString("```\n\n")
	b.WriteString("Grafana: http://localhost:3000\n\n")
	b.WriteString("Login: user admin. The generated password is GRAFANA_ADMIN_PASSWORD in .env.observability.\n\n")
	b.WriteString("## Rollback\n\n")
	b.WriteString("Delete the generated files or discard the observability branch.\n")
	return b.String()
}

// envObservabilityBase is the env file body that does not depend on generated secrets.
const envObservability = `# For app containers on the same Docker Compose network as the observability stack.
OTEL_SERVICE_NAME=your-service-name
OTEL_EXPORTER_OTLP_ENDPOINT=http://otel-collector:4318
OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf
OTEL_RESOURCE_ATTRIBUTES=deployment.environment=local,service.namespace=extent

# For apps running directly on the host, use:
# OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318
`

const tempoYAML = `server:
  http_listen_port: 3200

distributor:
  receivers:
    otlp:
      protocols:
        grpc:
          endpoint: 0.0.0.0:4317
        http:
          endpoint: 0.0.0.0:4318

storage:
  trace:
    backend: local
    wal:
      path: /tmp/tempo/wal
    local:
      path: /tmp/tempo/blocks
`

const lokiYAML = `auth_enabled: false

server:
  http_listen_port: 3100

common:
  path_prefix: /loki
  replication_factor: 1
  ring:
    kvstore:
      store: inmemory
  storage:
    filesystem:
      chunks_directory: /loki/chunks
      rules_directory: /loki/rules

schema_config:
  configs:
    - from: 2024-01-01
      store: tsdb
      object_store: filesystem
      schema: v13
      index:
        prefix: index_
        period: 24h

limits_config:
  allow_structured_metadata: true
`

func contractOrDefault(plan planner.Plan, contract string) string {
	if contract != "" {
		return contract
	}
	c := config.Defaults()
	if plan.Root != "" {
		c.Service.Name = filepath.Base(plan.Root)
	}
	b, _ := config.Marshal(c)
	return "# Generated by Extent. Edit intentionally; Extent treats this as the observability contract.\n" + string(b)
}

const prometheusAlertsYAML = `groups:
  - name: extent.rules
    rules:
      - alert: HighErrorRate
        expr: sum by (service_name) (rate(http_server_duration_milliseconds_count{http_status_code=~"5.."}[5m])) / sum by (service_name) (rate(http_server_duration_milliseconds_count[5m])) > 0.01
        for: 5m
        labels:
          severity: warning
        annotations:
          summary: High HTTP error rate
      - alert: HighP95Latency
        expr: histogram_quantile(0.95, sum(rate(http_server_duration_milliseconds_bucket[5m])) by (le, http_route)) > 300
        for: 5m
        labels:
          severity: warning
        annotations:
          summary: High route p95 latency
      - alert: DatabaseSlowQueries
        expr: histogram_quantile(0.95, sum(rate(db_client_operation_duration_milliseconds_bucket[5m])) by (le, db_system)) > 250
        for: 5m
        labels:
          severity: warning
        annotations:
          summary: Database query latency is high
      - alert: QueueBacklogGrowing
        expr: increase(queue_depth[10m]) > 100
        for: 10m
        labels:
          severity: warning
        annotations:
          summary: Queue backlog is growing
      - alert: AppMemoryLeakSuspected
        expr: increase(process_resident_memory_bytes[30m]) > 100000000
        for: 30m
        labels:
          severity: warning
        annotations:
          summary: App memory usage is steadily increasing
      - alert: NoTelemetryReceived
        expr: absent(otelcol_receiver_accepted_spans)
        for: 5m
        labels:
          severity: critical
        annotations:
          summary: No traces received by Collector
      - alert: NoLogsForService
        expr: absent(otelcol_receiver_accepted_log_records)
        for: 5m
        labels:
          severity: warning
        annotations:
          summary: No logs received by Collector
      - alert: NoTracesForService
        expr: absent(otelcol_receiver_accepted_spans)
        for: 5m
        labels:
          severity: warning
        annotations:
          summary: No traces received for service
`

const datasourcesYAML = `apiVersion: 1
datasources:
  - name: Prometheus
    type: prometheus
    uid: prometheus
    access: proxy
    url: http://prometheus:9090
    isDefault: true
  - name: Loki
    type: loki
    uid: loki
    access: proxy
    url: http://loki:3100
  - name: Tempo
    type: tempo
    uid: tempo
    access: proxy
    url: http://tempo:3200
    jsonData:
      tracesToLogsV2:
        datasourceUid: loki
        filterByTraceID: true
        filterBySpanID: true
        tags:
          - key: service.name
            value: service_name
      tracesToMetrics:
        datasourceUid: prometheus
        tags:
          - key: service.name
            value: service_name
        queries:
          - name: Request rate
            query: sum(rate(http_server_requests_total{service_name="$${__tags.service_name}"}[5m]))
          - name: Request latency
            query: histogram_quantile(0.95, sum(rate(http_server_duration_milliseconds_bucket{service_name="$${__tags.service_name}"}[5m])) by (le))
      serviceMap:
        datasourceUid: prometheus
`

const dashboardsYAML = `apiVersion: 1
providers:
  - name: Extent
    orgId: 1
    folder: Extent
    type: file
    disableDeletion: false
    editable: true
    options:
      path: /var/lib/grafana/dashboards
`

const dashboardJSON = `{
  "title": "Extent Service Overview",
  "schemaVersion": 39,
  "version": 1,
  "refresh": "10s",
  "panels": [
    {
      "type": "timeseries",
      "title": "Incoming telemetry rate",
      "gridPos": {"x": 0, "y": 0, "w": 12, "h": 8},
      "targets": [
        {
          "datasource": {"type": "prometheus", "uid": "prometheus"},
          "expr": "rate(otelcol_receiver_accepted_spans[5m])"
        }
      ]
    },
    {
      "type": "logs",
      "title": "Recent logs",
      "gridPos": {"x": 12, "y": 0, "w": 12, "h": 8},
      "targets": [
        {
          "datasource": {"type": "loki", "uid": "loki"},
          "expr": "{service_name=~\".+\"}"
        }
      ]
    },
    {
      "type": "timeseries",
      "title": "Application Latency",
      "gridPos": {"x": 0, "y": 8, "w": 12, "h": 8},
      "targets": [
        {
          "datasource": {"type": "prometheus", "uid": "prometheus"},
          "expr": "histogram_quantile(0.95, sum(rate(http_server_duration_milliseconds_bucket[5m])) by (le, http_route))"
        }
      ]
    },
    {
      "type": "timeseries",
      "title": "Database Latency",
      "gridPos": {"x": 12, "y": 8, "w": 12, "h": 8},
      "targets": [
        {
          "datasource": {"type": "prometheus", "uid": "prometheus"},
          "expr": "histogram_quantile(0.95, sum(rate(db_client_operation_duration_milliseconds_bucket[5m])) by (le, db_system))"
        }
      ]
    },
    {
      "type": "timeseries",
      "title": "Container CPU",
      "gridPos": {"x": 0, "y": 16, "w": 8, "h": 8},
      "targets": [
        {
          "datasource": {"type": "prometheus", "uid": "prometheus"},
          "expr": "sum(rate(container_cpu_usage_seconds_total{name!=\"\"}[5m])) by (name)"
        }
      ]
    },
    {
      "type": "timeseries",
      "title": "Container Memory",
      "gridPos": {"x": 8, "y": 16, "w": 8, "h": 8},
      "targets": [
        {
          "datasource": {"type": "prometheus", "uid": "prometheus"},
          "expr": "sum(container_memory_working_set_bytes{name!=\"\"}) by (name)"
        }
      ]
    },
    {
      "type": "timeseries",
      "title": "Host CPU",
      "gridPos": {"x": 16, "y": 16, "w": 8, "h": 8},
      "targets": [
        {
          "datasource": {"type": "prometheus", "uid": "prometheus"},
          "expr": "100 - (avg(rate(node_cpu_seconds_total{mode=\"idle\"}[5m])) * 100)"
        }
      ]
    }
  ]
}
`
