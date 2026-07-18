package config

import (
	"bytes"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Version         int             `yaml:"version"`
	Profile         Profile         `yaml:"profile"`
	Protocol        Protocol        `yaml:"protocol"`
	Service         Service         `yaml:"service"`
	OTLP            OTLP            `yaml:"otlp"`
	Signals         Signals         `yaml:"signals"`
	Instrumentation Instrumentation `yaml:"instrumentation"`
	SLO             SLO             `yaml:"slo"`
	Sampling        Sampling        `yaml:"sampling"`
	Redaction       Redaction       `yaml:"redaction"`
}
type Profile struct {
	Name    string   `yaml:"name"`
	Recipes []string `yaml:"recipes,omitempty"`
}
type Protocol struct {
	HTTP bool `yaml:"http"`
	GRPC bool `yaml:"grpc"`
}
type Service struct {
	Name        string `yaml:"name"`
	Namespace   string `yaml:"namespace"`
	Version     string `yaml:"version"`
	Environment string `yaml:"environment"`
	Port        int    `yaml:"port"`
}
type OTLP struct {
	Endpoint string `yaml:"endpoint"`
	Protocol string `yaml:"protocol"`
	Insecure bool   `yaml:"insecure"`
}
type Signals struct {
	Traces  bool `yaml:"traces"`
	Metrics bool `yaml:"metrics"`
	Logs    bool `yaml:"logs"`
}
type Instrumentation struct {
	HTTP            bool `yaml:"http"`
	Database        bool `yaml:"database"`
	Redis           bool `yaml:"redis"`
	Queues          bool `yaml:"queues"`
	ExternalHTTP    bool `yaml:"external_http"`
	LogsCorrelation bool `yaml:"logs_correlation"`
}
type SLO struct {
	HTTPLatencyP95MS    int     `yaml:"http_latency_p95_ms"`
	ErrorRatePercent    float64 `yaml:"error_rate_percent"`
	AvailabilityPercent float64 `yaml:"availability_percent"`
}
type Sampling struct {
	Local     string `yaml:"local"`
	HeavyLoad string `yaml:"heavy_load"`
}
type Redaction struct {
	Headers     []string `yaml:"headers"`
	DBStatement string   `yaml:"db_statement"`
	RequestBody string   `yaml:"request_body"`
}

func Defaults() Config {
	return Config{Version: 1, Profile: Profile{Name: "full"}, Protocol: Protocol{HTTP: true}, Service: Service{Name: "service", Namespace: "default", Version: "1.0.0", Environment: "development", Port: 8080}, OTLP: OTLP{Endpoint: "http://localhost:4317", Protocol: "grpc", Insecure: true}, Signals: Signals{Traces: true, Metrics: true, Logs: true}, Instrumentation: Instrumentation{HTTP: true, LogsCorrelation: true}, SLO: SLO{HTTPLatencyP95MS: 300, ErrorRatePercent: 1, AvailabilityPercent: 99.5}, Sampling: Sampling{Local: "always_on", HeavyLoad: "tail_sampling"}, Redaction: Redaction{Headers: []string{"authorization", "cookie"}, DBStatement: "sanitize", RequestBody: "disabled"}}
}

func Parse(data []byte) (Config, error) {
	var root yaml.Node
	d := yaml.NewDecoder(bytes.NewReader(data))
	if err := d.Decode(&root); err != nil {
		return Config{}, err
	}
	if root.Kind == 0 {
		return Config{}, fmt.Errorf("empty YAML document")
	}
	if err := duplicates(root.Content[0], ""); err != nil {
		return Config{}, err
	}
	c := Defaults()
	d = yaml.NewDecoder(bytes.NewReader(data))
	d.KnownFields(true)
	if err := d.Decode(&c); err != nil {
		return Config{}, err
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		if err == nil {
			return Config{}, fmt.Errorf("multiple YAML documents are not supported")
		}
		return Config{}, err
	}
	if err := c.Validate(); err != nil {
		return Config{}, err
	}
	return c, nil
}
func duplicates(n *yaml.Node, path string) error {
	if n.Kind == yaml.MappingNode {
		seen := map[string]bool{}
		for i := 0; i < len(n.Content); i += 2 {
			k := n.Content[i].Value
			if seen[k] {
				return fmt.Errorf("duplicate YAML field %q", k)
			}
			seen[k] = true
			if err := duplicates(n.Content[i+1], path+"."+k); err != nil {
				return err
			}
		}
	} else {
		for _, x := range n.Content {
			if err := duplicates(x, path); err != nil {
				return err
			}
		}
	}
	return nil
}
func Load(path string) (Config, error) {
	b, e := os.ReadFile(path)
	if e != nil {
		return Config{}, e
	}
	return Parse(b)
}

func Marshal(c Config) ([]byte, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return yaml.Marshal(c)
}

func (c Config) Validate() error {
	if c.Version != 1 {
		return fmt.Errorf("unsupported config version %d", c.Version)
	}
	allowed := map[string]bool{"minimal": true, "full": true, "high-cardinality-safe": true, "low-resource": true, "report-heavy": true}
	if strings.TrimSpace(c.Profile.Name) == "" || !allowed[strings.TrimSpace(c.Profile.Name)] {
		return fmt.Errorf("unsupported or missing profile.name")
	}
	for _, r := range c.Profile.Recipes {
		if strings.TrimSpace(r) == "" {
			return fmt.Errorf("profile.recipes cannot contain empty names")
		}
	}
	if !c.Protocol.HTTP && !c.Protocol.GRPC {
		return fmt.Errorf("at least one protocol is required")
	}
	if strings.TrimSpace(c.Service.Name) == "" {
		return fmt.Errorf("service.name is required")
	}
	if c.Service.Port < 1 || c.Service.Port > 65535 {
		return fmt.Errorf("service.port must be 1..65535")
	}
	if c.OTLP.Protocol != "grpc" && c.OTLP.Protocol != "http/protobuf" {
		return fmt.Errorf("otlp.protocol must be grpc or http/protobuf")
	}
	u, e := url.Parse(c.OTLP.Endpoint)
	if e != nil || u.Scheme == "" || u.Host == "" || u.User != nil {
		return fmt.Errorf("otlp.endpoint must be a safe absolute URL")
	}
	if c.SLO.HTTPLatencyP95MS < 1 || c.SLO.ErrorRatePercent < 0 || c.SLO.ErrorRatePercent > 100 || c.SLO.AvailabilityPercent < 0 || c.SLO.AvailabilityPercent > 100 {
		return fmt.Errorf("invalid SLO range")
	}
	if !map[string]bool{"always_on": true, "probabilistic": true, "tail_sampling": true}[c.Sampling.Local] || !map[string]bool{"always_on": true, "probabilistic": true, "tail_sampling": true}[c.Sampling.HeavyLoad] {
		return fmt.Errorf("invalid sampling mode")
	}
	if !map[string]bool{"sanitize": true, "disabled": true}[c.Redaction.DBStatement] || !map[string]bool{"sanitize": true, "disabled": true}[c.Redaction.RequestBody] {
		return fmt.Errorf("invalid redaction mode")
	}
	return nil
}
