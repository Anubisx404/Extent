package templates

import "strings"

// collectorTuning holds the values that differ between full-style Collector profiles.
type collectorTuning struct {
	MemoryLimitMiB  int
	SpikeLimitMiB   int
	NumTraces       int
	TracesPerSecond int
	SlowThresholdMs int
	// ExtraRedact lists attribute keys deleted from traces and logs in addition to auth headers.
	ExtraRedact []string
}

func defaultCollectorTuning() collectorTuning {
	return collectorTuning{
		MemoryLimitMiB:  512,
		SpikeLimitMiB:   128,
		NumTraces:       50000,
		TracesPerSecond: 100,
		SlowThresholdMs: 500,
	}
}

// cardinalityDeletes are process/host resource attributes that
// resource_to_telemetry_conversion would turn into Prometheus labels.
var cardinalityDeletes = []string{
	"process.pid",
	"process.command_args",
	"process.command",
	"process.command_line",
	"process.executable.path",
	"process.executable.name",
	"process.owner",
	"process.runtime.description",
	"host.id",
	"host.name",
	"service.instance.id",
}

func attr(key, value, action string) map[string]string {
	m := map[string]string{"key": key, "action": action}
	if value != "" {
		m["value"] = value
	}
	return m
}

func deleteAttr(key string) map[string]string { return attr(key, "", "delete") }

func receiversBlock() map[string]any {
	return map[string]any{
		"otlp": map[string]any{
			"protocols": map[string]any{
				"grpc": map[string]any{"endpoint": "0.0.0.0:4317"},
				"http": map[string]any{"endpoint": "0.0.0.0:4318"},
			},
		},
	}
}

func cardinalityProcessor() map[string]any {
	attrs := make([]map[string]string, 0, len(cardinalityDeletes))
	for _, key := range cardinalityDeletes {
		attrs = append(attrs, deleteAttr(key))
	}
	return map[string]any{"attributes": attrs}
}

func resourceProcessor() map[string]any {
	return map[string]any{"attributes": []map[string]string{
		// insert: keep values the application already set (e.g. deployment.environment).
		attr("deployment.environment", "development", "insert"),
		attr("telemetry.sdk.managed_by", "extent", "upsert"),
	}}
}

func promExporter() map[string]any {
	return map[string]any{
		"endpoint":                         "0.0.0.0:9464",
		"resource_to_telemetry_conversion": map[string]any{"enabled": true},
	}
}

func tempoExporter() map[string]any {
	return map[string]any{
		"endpoint": "tempo:4317",
		"tls":      map[string]any{"insecure": true},
	}
}

// buildFullCollector is the default Collector configuration: traces to Tempo
// with tail sampling, metrics to Prometheus, logs to Loki.
func buildFullCollector(t collectorTuning) map[string]any {
	redact := []map[string]string{
		deleteAttr("http.request.header.authorization"),
		deleteAttr("http.request.header.cookie"),
	}
	for _, key := range t.ExtraRedact {
		redact = append(redact, deleteAttr(key))
	}
	return map[string]any{
		"receivers": receiversBlock(),
		"processors": map[string]any{
			"memory_limiter": map[string]any{
				"check_interval":  "1s",
				"limit_mib":       t.MemoryLimitMiB,
				"spike_limit_mib": t.SpikeLimitMiB,
			},
			"resource":                     resourceProcessor(),
			"resource/metrics_cardinality": cardinalityProcessor(),
			"attributes/redact":            map[string]any{"actions": redact},
			"tail_sampling": map[string]any{
				"decision_wait":               "5s",
				"num_traces":                  t.NumTraces,
				"expected_new_traces_per_sec": t.TracesPerSecond,
				"policies": []map[string]any{
					// Keep every trace carrying the correlation header so smoke checks can find it.
					// OTTL matches the attribute whether the SDK records it as a string or an array.
					{
						"name": "keep-correlation-request",
						"type": "ottl_condition",
						"ottl_condition": map[string]any{
							"error_mode": "ignore",
							"span":       []string{`attributes["http.request.header.x_request_id"] != nil`},
						},
					},
					{
						"name":        "keep-errors",
						"type":        "status_code",
						"status_code": map[string]any{"status_codes": []string{"ERROR"}},
					},
					{
						"name":    "keep-slow",
						"type":    "latency",
						"latency": map[string]any{"threshold_ms": t.SlowThresholdMs},
					},
					// Baseline sample of everything else.
					{
						"name":          "baseline-10pct",
						"type":          "probabilistic",
						"probabilistic": map[string]any{"sampling_percentage": 10},
					},
				},
			},
			"batch": nil,
		},
		"exporters": map[string]any{
			"otlp/tempo":    tempoExporter(),
			"otlphttp/loki": map[string]any{"endpoint": "http://loki:3100/otlp"},
			"prometheus":    promExporter(),
		},
		"service": map[string]any{
			"telemetry": telemetryBlock(),
			"pipelines": map[string]any{
				// Metrics only: the cardinality processor is never applied to traces or logs.
				"traces":  pipeline([]string{"otlp"}, []string{"memory_limiter", "resource", "attributes/redact", "tail_sampling", "batch"}, []string{"otlp/tempo"}),
				"metrics": pipeline([]string{"otlp"}, []string{"memory_limiter", "resource", "resource/metrics_cardinality", "attributes/redact", "batch"}, []string{"prometheus"}),
				"logs":    pipeline([]string{"otlp"}, []string{"memory_limiter", "resource", "attributes/redact", "batch"}, []string{"otlphttp/loki"}),
			},
		},
	}
}

// buildMinimalCollector is the smallest configuration: traces and metrics only, no sampling or log export.
func buildMinimalCollector() map[string]any {
	return map[string]any{
		"receivers": receiversBlock(),
		"processors": map[string]any{
			"memory_limiter": map[string]any{
				"check_interval":  "1s",
				"limit_mib":       256,
				"spike_limit_mib": 64,
			},
			"resource":                     resourceProcessor(),
			"resource/metrics_cardinality": cardinalityProcessor(),
			"batch":                        nil,
		},
		"exporters": map[string]any{
			"otlp/tempo": tempoExporter(),
			"prometheus": promExporter(),
		},
		"service": map[string]any{
			"telemetry": telemetryBlock(),
			"pipelines": map[string]any{
				"traces":  pipeline([]string{"otlp"}, []string{"memory_limiter", "resource", "batch"}, []string{"otlp/tempo"}),
				"metrics": pipeline([]string{"otlp"}, []string{"memory_limiter", "resource", "resource/metrics_cardinality", "batch"}, []string{"prometheus"}),
			},
		},
	}
}

// telemetryBlock exposes the Collector's own metrics on 0.0.0.0:8888 so the
// Prometheus job "otel-collector-internal" can scrape them. Newer Collector
// releases bind internal telemetry to localhost unless configured here.
func telemetryBlock() map[string]any {
	return map[string]any{
		"metrics": map[string]any{
			"readers": []map[string]any{{
				"pull": map[string]any{
					"exporter": map[string]any{
						"prometheus": map[string]any{"host": "0.0.0.0", "port": 8888},
					},
				},
			}},
		},
	}
}

func pipeline(receivers, processors, exporters []string) map[string]any {
	return map[string]any{"receivers": receivers, "processors": processors, "exporters": exporters}
}

// collectorConfigForProfile returns the Collector model for a profile name.
func collectorConfigForProfile(profile string) map[string]any {
	switch strings.ToLower(strings.TrimSpace(profile)) {
	case "minimal":
		return buildMinimalCollector()
	case "low-resource":
		t := defaultCollectorTuning()
		t.MemoryLimitMiB = 256
		t.SpikeLimitMiB = 64
		t.NumTraces = 2000
		t.TracesPerSecond = 25
		return buildFullCollector(t)
	case "high-cardinality-safe":
		t := defaultCollectorTuning()
		t.ExtraRedact = []string{"db.statement", "enduser.id"}
		return buildFullCollector(t)
	case "report-heavy":
		t := defaultCollectorTuning()
		t.SlowThresholdMs = 250
		return buildFullCollector(t)
	default:
		return buildFullCollector(defaultCollectorTuning())
	}
}

func collectorYAMLForProfile(profile string) string {
	return marshalYAML(collectorConfigForProfile(profile))
}
