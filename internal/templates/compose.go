package templates

import (
	"bytes"

	"github.com/Anubisx404/Extent/internal/stack"
	"gopkg.in/yaml.v3"
)

// composeDocument is the typed model of docker-compose.observability.yml.
// Profile toggles are applied to these structs in buildCompose; the YAML is
// produced by yaml.v3, whose map keys are sorted, so output is byte-stable.
type composeDocument struct {
	Services composeServices      `yaml:"services"`
	Volumes  map[string]yaml.Node `yaml:"volumes,omitempty"`
}

// composeServices lists services in declaration order. Optional host-metrics
// services are pointers so they are omitted entirely when the profile disables them.
type composeServices struct {
	OTelCollector composeService  `yaml:"otel-collector"`
	Grafana       composeService  `yaml:"grafana"`
	Tempo         composeService  `yaml:"tempo"`
	Loki          composeService  `yaml:"loki"`
	Prometheus    composeService  `yaml:"prometheus"`
	CAdvisor      *composeService `yaml:"cadvisor,omitempty"`
	NodeExporter  *composeService `yaml:"node-exporter,omitempty"`
}

type composeService struct {
	Image       string         `yaml:"image"`
	User        string         `yaml:"user,omitempty"`
	Privileged  bool           `yaml:"privileged,omitempty"`
	Command     []string       `yaml:"command,omitempty"`
	Environment []string       `yaml:"environment,omitempty"`
	Ports       []string       `yaml:"ports,omitempty"`
	Volumes     []string       `yaml:"volumes,omitempty"`
	DependsOn   []string       `yaml:"depends_on,omitempty"`
	Healthcheck *composeHealth `yaml:"healthcheck,omitempty"`
}

type composeHealth struct {
	Test     []string `yaml:"test"`
	Interval string   `yaml:"interval"`
	Timeout  string   `yaml:"timeout"`
	Retries  int      `yaml:"retries"`
}

// loopback publishes a port on 127.0.0.1 only. Every published port goes through here.
func loopback(port string) string {
	return "127.0.0.1:" + port
}

// emptyVolume is the YAML null used for a named volume declaration (`name:`).
func emptyVolume() yaml.Node {
	return yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null", Value: ""}
}

// Tempo and Loki images ship neither a shell nor wget, so they have no
// HTTP healthcheck; `stack up --wait` only waits for them to be running. Their
// readiness is verified by `extent smoke` / `extent verify`.
func httpHealth(url string) *composeHealth {
	return &composeHealth{
		Test:     []string{"CMD-SHELL", "wget -q -O- " + url + " >/dev/null"},
		Interval: "10s",
		Timeout:  "5s",
		Retries:  12,
	}
}

// buildCompose returns the compose model for a profile. The portable default
// is loopback-only with no privileged containers; each profile flag adds or
// adjusts only the parts it names.
func buildCompose(profile stack.ProfileConfig) composeDocument {
	doc := composeDocument{
		Services: composeServices{
			OTelCollector: composeService{
				Image:     stack.ImageOTelCollector.Ref(),
				Command:   []string{"--config=/etc/otel-collector.yml"},
				Volumes:   []string{"./otel-collector.yml:/etc/otel-collector.yml:ro"},
				Ports:     []string{loopback("4317:4317"), loopback("4318:4318")},
				DependsOn: []string{"tempo", "loki", "prometheus"},
				Healthcheck: &composeHealth{
					Test:     []string{"CMD", "/otelcol-contrib", "validate", "--config=/etc/otel-collector.yml"},
					Interval: "10s",
					Timeout:  "5s",
					Retries:  6,
				},
			},
			Grafana: composeService{
				Image: stack.ImageGrafana.Ref(),
				Environment: []string{
					"GF_SECURITY_ADMIN_USER=${GRAFANA_ADMIN_USER:-admin}",
					// The password comes from .env.observability (written by extent apply). Compose
					// fails fast with this message when it is missing.
					"GF_SECURITY_ADMIN_PASSWORD=${GRAFANA_ADMIN_PASSWORD:?run extent apply}",
				},
				Ports:       []string{loopback("3000:3000")},
				Volumes:     []string{"./grafana/provisioning:/etc/grafana/provisioning:ro", "./grafana/dashboards:/var/lib/grafana/dashboards:ro"},
				DependsOn:   []string{"prometheus", "loki", "tempo"},
				Healthcheck: httpHealth("http://localhost:3000/api/health"),
			},
			Tempo: composeService{
				Image:   stack.ImageTempo.Ref(),
				User:    "0:0",
				Command: []string{"-config.file=/etc/tempo.yaml"},
				Volumes: []string{"./tempo.yml:/etc/tempo.yaml:ro"},
				Ports:   []string{loopback("3200:3200")},
			},
			Loki: composeService{
				Image:   stack.ImageLoki.Ref(),
				Command: []string{"-config.file=/etc/loki/local-config.yaml"},
				Volumes: []string{"./loki.yml:/etc/loki/local-config.yaml:ro"},
				Ports:   []string{loopback("3100:3100")},
			},
			Prometheus: composeService{
				Image:       stack.ImagePrometheus.Ref(),
				Ports:       []string{loopback("9090:9090")},
				Volumes:     []string{"./prometheus.yml:/etc/prometheus/prometheus.yml:ro", "./prometheus-alerts.yml:/etc/prometheus/prometheus-alerts.yml:ro"},
				Command:     []string{"--config.file=/etc/prometheus/prometheus.yml"},
				Healthcheck: httpHealth("http://localhost:9090/-/ready"),
			},
		},
		Volumes: map[string]yaml.Node{},
	}

	if profile.HostMetrics {
		// cAdvisor needs privileged access to the host Docker daemon and cgroups.
		doc.Services.CAdvisor = &composeService{
			Image:      stack.ImageCAdvisor.Ref(),
			Privileged: true,
			Ports:      []string{loopback("8088:8080")},
			Volumes: []string{
				"/:/rootfs:ro",
				"/var/run:/var/run:ro",
				"/sys:/sys:ro",
				"/var/lib/docker/:/var/lib/docker:ro",
			},
		}
		doc.Services.NodeExporter = &composeService{
			Image:   stack.ImageNodeExporter.Ref(),
			Ports:   []string{loopback("9100:9100")},
			Command: []string{"--path.rootfs=/host"},
			Volumes: []string{"/:/host:ro,rslave"},
		}
		doc.Services.OTelCollector.DependsOn = append(doc.Services.OTelCollector.DependsOn, "cadvisor", "node-exporter")
	}

	if profile.Persistent {
		// Named volumes keep Grafana, Tempo, Loki and Prometheus state across restarts.
		doc.Services.Grafana.Volumes = append(doc.Services.Grafana.Volumes, "grafana-data:/var/lib/grafana")
		doc.Services.Tempo.Volumes = append(doc.Services.Tempo.Volumes, "tempo-data:/tmp/tempo")
		doc.Services.Loki.Volumes = append(doc.Services.Loki.Volumes, "loki-data:/loki")
		doc.Services.Prometheus.Volumes = append(doc.Services.Prometheus.Volumes, "prometheus-data:/prometheus")
		for _, name := range []string{"grafana-data", "tempo-data", "loki-data", "prometheus-data"} {
			doc.Volumes[name] = emptyVolume()
		}
	}
	return doc
}

// composeYAMLForProfile renders the compose file for a profile.
func composeYAMLForProfile(profile stack.ProfileConfig) string {
	return marshalYAML(buildCompose(profile))
}

// marshalYAML encodes a generated document with two-space indentation. The
// inputs are fixed Go values, so encoding failures are programming errors.
func marshalYAML(v any) string {
	var b bytes.Buffer
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	if err := enc.Encode(v); err != nil {
		panic("marshal generated YAML: " + err.Error())
	}
	if err := enc.Close(); err != nil {
		panic("marshal generated YAML: " + err.Error())
	}
	return b.String()
}
