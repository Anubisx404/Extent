package templates

import (
	"github.com/Anubisx404/Extent/internal/stack"
)

// prometheusConfig is the typed model of prometheus.yml.
type prometheusConfig struct {
	Global        prometheusGlobal   `yaml:"global"`
	RuleFiles     []string           `yaml:"rule_files"`
	ScrapeConfigs []prometheusScrape `yaml:"scrape_configs"`
}

type prometheusGlobal struct {
	ScrapeInterval string `yaml:"scrape_interval"`
}

type prometheusScrape struct {
	JobName       string         `yaml:"job_name"`
	StaticConfigs []staticTarget `yaml:"static_configs"`
}

type staticTarget struct {
	Targets []string `yaml:"targets"`
}

func scrape(job string, target string) prometheusScrape {
	return prometheusScrape{JobName: job, StaticConfigs: []staticTarget{{Targets: []string{target}}}}
}

// buildPrometheus scrapes the Collector always; host targets only with HostMetrics.
func buildPrometheus(profile stack.ProfileConfig) prometheusConfig {
	cfg := prometheusConfig{
		Global:    prometheusGlobal{ScrapeInterval: "5s"},
		RuleFiles: []string{"/etc/prometheus/prometheus-alerts.yml"},
		ScrapeConfigs: []prometheusScrape{
			scrape("otel-collector-internal", "otel-collector:8888"),
			scrape("otlp-metrics", "otel-collector:9464"),
		},
	}
	if profile.HostMetrics {
		cfg.ScrapeConfigs = append(cfg.ScrapeConfigs,
			scrape("cadvisor", "cadvisor:8080"),
			scrape("node-exporter", "node-exporter:9100"),
		)
	}
	return cfg
}

func prometheusYAMLForProfile(profile stack.ProfileConfig) string {
	return marshalYAML(buildPrometheus(profile))
}
