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
	files := map[string]string{
		"docker-compose.observability.yml": composeYAMLForProfile(resolved),
		"otel-collector.yml":               collectorYAMLForProfile(profile),
		"tempo.yml":                        tempoYAML,
		"loki.yml":                         lokiYAML,
		"prometheus.yml":                   prometheusYAMLForProfile(resolved),
		"prometheus-alerts.yml":            prometheusAlertsYAML,
		"extent.yaml":                      contractOrDefault(plan, opts.Contract),
		".env.observability":               envObservability,
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
			authorized := opts.Overwrite || rel == "extent.yaml" && opts.ContractOverwrite
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
	b.WriteString("Default local credentials depend on Grafana image defaults or your configured environment.\n\n")
	b.WriteString("## Rollback\n\n")
	b.WriteString("Delete the generated files or discard the observability branch.\n")
	return b.String()
}

const composeYAML = `services:
  otel-collector:
    image: otel/opentelemetry-collector-contrib:0.102.1
    command: ["--config=/etc/otel-collector.yml"]
    volumes:
      - ./otel-collector.yml:/etc/otel-collector.yml:ro
    ports:
      - "4317:4317"
      - "4318:4318"
    depends_on:
      - tempo
      - loki
      - prometheus
      - cadvisor
      - node-exporter
    healthcheck:
      test: ["CMD", "/otelcol-contrib", "validate", "--config=/etc/otel-collector.yml"]
      interval: 10s
      timeout: 5s
      retries: 6

  grafana:
    image: grafana/grafana:11.0.0
    ports:
      - "3000:3000"
    volumes:
      - ./grafana/provisioning:/etc/grafana/provisioning:ro
      - ./grafana/dashboards:/var/lib/grafana/dashboards:ro
    depends_on:
      - prometheus
      - loki
      - tempo
    healthcheck:
      test: ["CMD-SHELL", "wget -q -O- http://localhost:3000/api/health >/dev/null"]
      interval: 10s
      timeout: 5s
      retries: 12

  tempo:
    image: grafana/tempo:2.5.0
    user: "0:0"
    command: ["-config.file=/etc/tempo.yaml"]
    volumes:
      - ./tempo.yml:/etc/tempo.yaml:ro
    ports:
      - "3200:3200"
    healthcheck:
      test: ["CMD-SHELL", "wget -q -O- http://localhost:3200/ready >/dev/null"]
      interval: 10s
      timeout: 5s
      retries: 12

  loki:
    image: grafana/loki:3.0.0
    command: ["-config.file=/etc/loki/local-config.yaml"]
    volumes:
      - ./loki.yml:/etc/loki/local-config.yaml:ro
    ports:
      - "3100:3100"
    healthcheck:
      test: ["CMD-SHELL", "wget -q -O- http://localhost:3100/ready >/dev/null"]
      interval: 10s
      timeout: 5s
      retries: 12

  prometheus:
    image: prom/prometheus:v2.52.0
    ports:
      - "9090:9090"
    volumes:
      - ./prometheus.yml:/etc/prometheus/prometheus.yml:ro
      - ./prometheus-alerts.yml:/etc/prometheus/prometheus-alerts.yml:ro
    command:
      - "--config.file=/etc/prometheus/prometheus.yml"
      - "--web.enable-remote-write-receiver"
    healthcheck:
      test: ["CMD-SHELL", "wget -q -O- http://localhost:9090/-/ready >/dev/null"]
      interval: 10s
      timeout: 5s
      retries: 12

  cadvisor:
    image: gcr.io/cadvisor/cadvisor:v0.49.1
    privileged: true
    ports:
      - "8088:8080"
    volumes:
      - /:/rootfs:ro
      - /var/run:/var/run:ro
      - /sys:/sys:ro
      - /var/lib/docker/:/var/lib/docker:ro

  node-exporter:
    image: prom/node-exporter:v1.8.1
    ports:
      - "9100:9100"
    command:
      - "--path.rootfs=/host"
    volumes:
      - /:/host:ro,rslave
`

func composeYAMLForProfile(profile stack.ProfileConfig) string {
	base := composeYAML
	if !profile.HostMetrics {
		base = base[:strings.Index(base, "  cadvisor:\n")]
	}
	for original, loopback := range map[string]string{
		`"4317:4317"`: `"127.0.0.1:4317:4317"`,
		`"4318:4318"`: `"127.0.0.1:4318:4318"`,
		`"3000:3000"`: `"127.0.0.1:3000:3000"`,
		`"3200:3200"`: `"127.0.0.1:3200:3200"`,
		`"3100:3100"`: `"127.0.0.1:3100:3100"`,
		`"9090:9090"`: `"127.0.0.1:9090:9090"`,
		`"8088:8080"`: `"127.0.0.1:8088:8080"`,
		`"9100:9100"`: `"127.0.0.1:9100:9100"`,
	} {
		base = strings.ReplaceAll(base, original, loopback)
	}
	if profile.Persistent {
		base = strings.ReplaceAll(base, "      - ./grafana/dashboards:/var/lib/grafana/dashboards:ro", "      - ./grafana/dashboards:/var/lib/grafana/dashboards:ro\n      - grafana-data:/var/lib/grafana")
		base = strings.ReplaceAll(base, "      - ./tempo.yml:/etc/tempo.yaml:ro", "      - ./tempo.yml:/etc/tempo.yaml:ro\n      - tempo-data:/tmp/tempo")
		base = strings.ReplaceAll(base, "      - ./loki.yml:/etc/loki/local-config.yaml:ro", "      - ./loki.yml:/etc/loki/local-config.yaml:ro\n      - loki-data:/loki")
		base = strings.ReplaceAll(base, "      - ./prometheus-alerts.yml:/etc/prometheus/prometheus-alerts.yml:ro", "      - ./prometheus-alerts.yml:/etc/prometheus/prometheus-alerts.yml:ro\n      - prometheus-data:/prometheus")
		base += "\nvolumes:\n  grafana-data:\n  tempo-data:\n  loki-data:\n  prometheus-data:\n"
	}
	if !profile.HostMetrics {
		base = strings.ReplaceAll(base, "      - cadvisor\n", "")
		base = strings.ReplaceAll(base, "      - node-exporter\n", "")
	}
	return base
}

const collectorYAML = `receivers:
  otlp:
    protocols:
      grpc:
        endpoint: 0.0.0.0:4317
      http:
        endpoint: 0.0.0.0:4318

processors:
  memory_limiter:
    check_interval: 1s
    limit_mib: 512
    spike_limit_mib: 128
  resource:
    attributes:
      - key: deployment.environment
        value: development
        action: upsert
      - key: telemetry.sdk.managed_by
        value: extent
        action: upsert
  attributes/redact:
    actions:
      - key: http.request.header.authorization
        action: delete
      - key: http.request.header.cookie
        action: delete
  tail_sampling:
    decision_wait: 5s
    num_traces: 10000
    expected_new_traces_per_sec: 100
    policies:
      - name: keep-errors
        type: status_code
        status_code:
          status_codes: [ERROR]
      - name: keep-slow
        type: latency
        latency:
          threshold_ms: 500
      - name: keep-all
        type: always_sample
  batch:

exporters:
  otlp/tempo:
    endpoint: tempo:4317
    tls:
      insecure: true
  otlphttp/loki:
    endpoint: http://loki:3100/otlp
  prometheus:
    endpoint: 0.0.0.0:9464
    resource_to_telemetry_conversion:
      enabled: true

service:
  pipelines:
    traces:
      receivers: [otlp]
      processors: [memory_limiter, resource, attributes/redact, tail_sampling, batch]
      exporters: [otlp/tempo]
    metrics:
      receivers: [otlp]
      processors: [memory_limiter, resource, attributes/redact, batch]
      exporters: [prometheus]
    logs:
      receivers: [otlp]
      processors: [memory_limiter, resource, attributes/redact, batch]
      exporters: [otlphttp/loki]
`

const collectorYAMLMinimal = `receivers:
  otlp:
    protocols:
      grpc:
        endpoint: 0.0.0.0:4317
      http:
        endpoint: 0.0.0.0:4318

processors:
  memory_limiter:
    check_interval: 1s
    limit_mib: 256
    spike_limit_mib: 64
  resource:
    attributes:
      - key: deployment.environment
        value: development
        action: upsert
      - key: telemetry.sdk.managed_by
        value: extent
        action: upsert
  batch:

exporters:
  otlp/tempo:
    endpoint: tempo:4317
    tls:
      insecure: true
  prometheus:
    endpoint: 0.0.0.0:9464
    resource_to_telemetry_conversion:
      enabled: true

service:
  pipelines:
    traces:
      receivers: [otlp]
      processors: [memory_limiter, resource, batch]
      exporters: [otlp/tempo]
    metrics:
      receivers: [otlp]
      processors: [memory_limiter, resource, batch]
      exporters: [prometheus]
`

func collectorYAMLForProfile(profile string) string {
	switch strings.ToLower(strings.TrimSpace(profile)) {
	case "minimal":
		return collectorYAMLMinimal
	case "low-resource":
		out := strings.ReplaceAll(collectorYAML, "limit_mib: 512", "limit_mib: 256")
		out = strings.ReplaceAll(out, "spike_limit_mib: 128", "spike_limit_mib: 64")
		out = strings.ReplaceAll(out, "num_traces: 10000", "num_traces: 2000")
		out = strings.ReplaceAll(out, "expected_new_traces_per_sec: 100", "expected_new_traces_per_sec: 25")
		return out
	case "high-cardinality-safe":
		return strings.Replace(collectorYAML, "      - key: http.request.header.cookie\n        action: delete\n", "      - key: http.request.header.cookie\n        action: delete\n      - key: db.statement\n        action: delete\n      - key: enduser.id\n        action: delete\n", 1)
	case "report-heavy":
		return strings.ReplaceAll(collectorYAML, "threshold_ms: 500", "threshold_ms: 250")
	default:
		return collectorYAML
	}
}

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

const prometheusYAML = `global:
  scrape_interval: 5s
rule_files:
  - /etc/prometheus/prometheus-alerts.yml

scrape_configs:
  - job_name: otel-collector-internal
    static_configs:
      - targets: ["otel-collector:8888"]
  - job_name: otlp-metrics
    static_configs:
      - targets: ["otel-collector:9464"]
  - job_name: cadvisor
    static_configs:
      - targets: ["cadvisor:8080"]
  - job_name: node-exporter
    static_configs:
      - targets: ["node-exporter:9100"]
`

func prometheusYAMLForProfile(profile stack.ProfileConfig) string {
	if profile.HostMetrics {
		return prometheusYAML
	}
	return prometheusYAML[:strings.Index(prometheusYAML, "  - job_name: cadvisor\n")]
}

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
        expr: sum(rate(http_server_errors_total[5m])) / clamp_min(sum(rate(http_server_requests_total[5m])), 1) > 0.01
        for: 5m
        labels:
          severity: warning
        annotations:
          summary: High HTTP error rate
      - alert: HighP95Latency
        expr: histogram_quantile(0.95, sum(rate(http_server_duration_milliseconds_bucket[5m])) by (le, route)) > 300
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
          "expr": "histogram_quantile(0.95, sum(rate(http_server_duration_milliseconds_bucket[5m])) by (le, route))"
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
