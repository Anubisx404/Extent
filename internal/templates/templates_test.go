package templates

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Anubisx404/Extent/internal/fileops"
	"github.com/Anubisx404/Extent/internal/planner"
	"github.com/Anubisx404/Extent/internal/state"
	"gopkg.in/yaml.v3"
)

func TestWriteLGTMWritesV1FileSet(t *testing.T) {
	root := t.TempDir()
	plan := planner.Plan{Root: root, Detected: []string{"runtime:node"}}

	written, err := WriteLGTM(root, plan, WriteOptions{})
	if err != nil {
		t.Fatal(err)
	}

	want := []string{
		"docker-compose.observability.yml",
		"otel-collector.yml",
		"tempo.yml",
		"loki.yml",
		"prometheus.yml",
		"prometheus-alerts.yml",
		"extent.yaml",
		".env.observability",
		"grafana/provisioning/datasources/datasources.yml",
		"grafana/provisioning/dashboards/dashboards.yml",
		"grafana/dashboards/service-overview.json",
		".extent/report.md",
		".extent/plan.json",
	}
	for _, rel := range want {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err != nil {
			t.Fatalf("expected %s to exist: %v", rel, err)
		}
	}
	if len(written) != len(want) {
		t.Fatalf("expected %d written files, got %d: %#v", len(want), len(written), written)
	}
}

func TestWriteLGTMDefaultIsPortablePersistentAndLoopbackOnly(t *testing.T) {
	root := t.TempDir()
	_, err := WriteLGTM(root, planner.Plan{Root: root}, WriteOptions{})
	if err != nil {
		t.Fatal(err)
	}

	compose := mustRead(t, filepath.Join(root, "docker-compose.observability.yml"))
	for _, forbidden := range []string{"cadvisor:", "node-exporter:", "privileged: true", `- "/:`} {
		if strings.Contains(compose, forbidden) {
			t.Fatalf("portable default contains %q:\n%s", forbidden, compose)
		}
	}
	for _, required := range []string{"127.0.0.1:4317:4317", "127.0.0.1:3000:3000", "grafana-data:/var/lib/grafana", "./grafana/dashboards:/var/lib/grafana/dashboards:ro", "healthcheck:", `user: "0:0"`} {
		if !strings.Contains(compose, required) {
			t.Fatalf("portable default missing %q:\n%s", required, compose)
		}
	}
	prometheus := mustRead(t, filepath.Join(root, "prometheus.yml"))
	if strings.Contains(prometheus, "cadvisor:8080") || strings.Contains(prometheus, "node-exporter:9100") {
		t.Fatalf("portable Prometheus config contains host targets:\n%s", prometheus)
	}
	for _, required := range []string{"- otel-collector:8888", "- otel-collector:9464"} {
		if !strings.Contains(prometheus, required) {
			t.Fatalf("Prometheus config missing %q:\n%s", required, prometheus)
		}
	}
	assertFileContains(t, filepath.Join(root, "grafana/dashboards/service-overview.json"), "Container CPU")
	assertFileContains(t, filepath.Join(root, "grafana/dashboards/service-overview.json"), "Host CPU")
}

func TestWriteLGTMRefusesOverwrite(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "otel-collector.yml")
	if err := os.WriteFile(path, []byte("existing"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := WriteLGTM(root, planner.Plan{Root: root}, WriteOptions{})
	if err == nil {
		t.Fatal("expected overwrite refusal")
	}
	if _, statErr := os.Stat(filepath.Join(root, ".extent", "plan.json")); !os.IsNotExist(statErr) {
		t.Fatalf("expected preflight to avoid partial writes, got stat error %v", statErr)
	}
}

func TestPlanLGTMValidatesProfilesWithoutMutation(t *testing.T) {
	root := t.TempDir()
	if _, err := PlanLGTM(root, planner.Plan{Root: root}, WriteOptions{Profile: "full"}); err != nil {
		t.Fatalf("full profile rejected: %v", err)
	}
	if _, err := PlanLGTM(root, planner.Plan{Root: root}, WriteOptions{Profile: "invalid"}); err == nil {
		t.Fatal("invalid profile accepted")
	}
	if _, err := os.Stat(filepath.Join(root, ".extent")); !os.IsNotExist(err) {
		t.Fatalf("planning mutated project: %v", err)
	}
}

func TestWriteLGTMIsIdempotentAndUndoRestoresExactBytes(t *testing.T) {
	root := t.TempDir()
	original := []byte("user collector config\n")
	collector := filepath.Join(root, "otel-collector.yml")
	if err := os.WriteFile(collector, original, 0o640); err != nil {
		t.Fatal(err)
	}
	plan := planner.Plan{Root: root, Detected: []string{"runtime:go"}}
	options := WriteOptions{Profile: "full", Overwrite: true}
	if _, err := WriteLGTM(root, plan, options); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteLGTM(root, plan, options); err != nil {
		t.Fatal(err)
	}
	manifest, err := state.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Operations) != 1 {
		t.Fatalf("idempotent apply recorded %d operations", len(manifest.Operations))
	}
	if err := fileops.UndoKind(root, "stack-config"); err != nil {
		t.Fatal(err)
	}
	restored, err := os.ReadFile(collector)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(restored, original) {
		t.Fatalf("restored bytes = %q", restored)
	}
	if _, err := os.Stat(filepath.Join(root, "docker-compose.observability.yml")); !os.IsNotExist(err) {
		t.Fatalf("created file survived undo: %v", err)
	}
}

func assertFileContains(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), want) {
		t.Fatalf("expected %s to contain %q, got:\n%s", path, want, string(data))
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

type collectorConfig struct {
	Processors map[string]collectorProcessor `yaml:"processors"`
	Service    struct {
		Pipelines map[string]struct {
			Processors []string `yaml:"processors"`
		} `yaml:"pipelines"`
	} `yaml:"service"`
}

// collectorProcessor is a superset of the processor shapes used by the template;
// fields absent from a given processor simply stay zero.
type collectorProcessor struct {
	NumTraces  int               `yaml:"num_traces"`
	Attributes []collectorAttr   `yaml:"attributes"`
	Policies   []collectorPolicy `yaml:"policies"`
}

type collectorAttr struct {
	Key    string `yaml:"key"`
	Value  string `yaml:"value"`
	Action string `yaml:"action"`
}

type collectorPolicy struct {
	Name          string `yaml:"name"`
	Type          string `yaml:"type"`
	OTTLCondition *struct {
		Span []string `yaml:"span"`
	} `yaml:"ottl_condition"`
	Probabilistic *struct {
		SamplingPercentage float64 `yaml:"sampling_percentage"`
	} `yaml:"probabilistic"`
}

func parseCollector(t *testing.T, profile string) collectorConfig {
	t.Helper()
	var cfg collectorConfig
	if err := yaml.Unmarshal([]byte(collectorYAMLForProfile(profile)), &cfg); err != nil {
		t.Fatalf("collector config for profile %q is not valid YAML: %v", profile, err)
	}
	return cfg
}

func TestCollectorTailSamplingHasNoAlwaysSampleAndKeepsCorrelatedTraces(t *testing.T) {
	cfg := parseCollector(t, "full")
	ts := cfg.Processors["tail_sampling"]
	if ts.NumTraces != 50000 {
		t.Fatalf("num_traces = %d, want 50000", ts.NumTraces)
	}
	var hasCorrelation, hasProbabilistic bool
	for _, p := range ts.Policies {
		if p.Type == "always_sample" {
			t.Fatalf("tail_sampling must not contain always_sample policy %q", p.Name)
		}
		if p.Type == "ottl_condition" && p.OTTLCondition != nil {
			for _, condition := range p.OTTLCondition.Span {
				if strings.Contains(condition, `attributes["http.request.header.x_request_id"]`) {
					hasCorrelation = true
				}
			}
		}
		if p.Type == "probabilistic" && p.Probabilistic != nil && p.Probabilistic.SamplingPercentage == 10 {
			hasProbabilistic = true
		}
	}
	if !hasCorrelation {
		t.Fatalf("missing x_request_id keep policy: %#v", ts.Policies)
	}
	if !hasProbabilistic {
		t.Fatalf("missing 10%% probabilistic baseline: %#v", ts.Policies)
	}
}

func TestCollectorCardinalityProcessorIsMetricsOnly(t *testing.T) {
	for _, profile := range []string{"full", "minimal", "low-resource", "report-heavy", "high-cardinality-safe"} {
		cfg := parseCollector(t, profile)
		card, ok := cfg.Processors["resource/metrics_cardinality"]
		if !ok {
			t.Fatalf("profile %q: resource/metrics_cardinality processor missing", profile)
		}
		deleted := map[string]bool{}
		for _, a := range card.Attributes {
			if a.Action != "delete" {
				t.Fatalf("profile %q: cardinality processor has non-delete action for %q", profile, a.Key)
			}
			deleted[a.Key] = true
		}
		for _, key := range []string{"process.pid", "process.command_args", "process.command", "process.command_line", "process.executable.path", "process.executable.name", "process.owner", "process.runtime.description", "host.id", "host.name", "service.instance.id"} {
			if !deleted[key] {
				t.Fatalf("profile %q: cardinality processor does not delete %q", profile, key)
			}
		}
		for _, key := range []string{"service.name", "service.namespace", "deployment.environment", "telemetry.sdk.language"} {
			if deleted[key] {
				t.Fatalf("profile %q: cardinality processor must not delete %q", profile, key)
			}
		}
		if p := cfg.Service.Pipelines["metrics"].Processors; !containsString(p, "resource/metrics_cardinality") {
			t.Fatalf("profile %q: metrics pipeline %v lacks cardinality processor", profile, p)
		}
		if p := cfg.Service.Pipelines["traces"].Processors; containsString(p, "resource/metrics_cardinality") {
			t.Fatalf("profile %q: traces pipeline %v must not use cardinality processor", profile, p)
		}
		if p := cfg.Service.Pipelines["logs"].Processors; containsString(p, "resource/metrics_cardinality") {
			t.Fatalf("profile %q: logs pipeline %v must not use cardinality processor", profile, p)
		}
	}
}

func TestCollectorDeploymentEnvironmentDoesNotOverwriteAppValue(t *testing.T) {
	cfg := parseCollector(t, "full")
	for _, a := range cfg.Processors["resource"].Attributes {
		if a.Key == "deployment.environment" && a.Action != "insert" {
			t.Fatalf("deployment.environment action = %q, want insert", a.Action)
		}
	}
}

func TestCollectorLowResourceKeepsSmallTraceBuffer(t *testing.T) {
	cfg := parseCollector(t, "low-resource")
	if got := cfg.Processors["tail_sampling"].NumTraces; got != 2000 {
		t.Fatalf("low-resource num_traces = %d, want 2000", got)
	}
}

func TestPrometheusDoesNotEnableUnusedRemoteWriteReceiver(t *testing.T) {
	root := t.TempDir()
	if _, err := WriteLGTM(root, planner.Plan{Root: root}, WriteOptions{}); err != nil {
		t.Fatal(err)
	}
	compose := mustRead(t, filepath.Join(root, "docker-compose.observability.yml"))
	if strings.Contains(compose, "enable-remote-write-receiver") {
		t.Fatalf("compose still enables remote write receiver:\n%s", compose)
	}
}

func TestGrafanaCredentialsComeFromEnvFileAndLoopbackBound(t *testing.T) {
	root := t.TempDir()
	if _, err := WriteLGTM(root, planner.Plan{Root: root}, WriteOptions{}); err != nil {
		t.Fatal(err)
	}
	compose := mustRead(t, filepath.Join(root, "docker-compose.observability.yml"))
	for _, required := range []string{
		"GF_SECURITY_ADMIN_USER=${GRAFANA_ADMIN_USER:-admin}",
		"GF_SECURITY_ADMIN_PASSWORD=${GRAFANA_ADMIN_PASSWORD:?run extent apply}",
		"127.0.0.1",
	} {
		if !strings.Contains(compose, required) {
			t.Fatalf("compose missing %q:\n%s", required, compose)
		}
	}
}

func containsString(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}
