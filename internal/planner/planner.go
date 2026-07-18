package planner

import (
	"fmt"
	"strings"

	"github.com/Anubisx404/Extent/internal/scanner"
)

type Plan struct {
	Root         string   `json:"root"`
	Detected     []string `json:"detected"`
	Changes      []Change `json:"changes"`
	Warnings     []string `json:"warnings"`
	NextCommands []string `json:"nextCommands"`
}

type Change struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

func Build(result scanner.Result) Plan {
	plan := Plan{Root: result.Root}
	plan.Detected = append(plan.Detected, prefixValues("runtime", result.Runtimes)...)
	plan.Detected = append(plan.Detected, prefixValues("framework", result.Frameworks)...)
	plan.Detected = append(plan.Detected, prefixValues("package-manager", result.PackageManagers)...)
	plan.Detected = append(plan.Detected, prefixValues("database", result.DatabaseLibraries)...)
	if len(result.ComposeFiles) > 0 {
		plan.Detected = append(plan.Detected, fmt.Sprintf("docker-compose:%s", strings.Join(result.ComposeFiles, ",")))
	}

	plan.Changes = []Change{
		{Path: "docker-compose.observability.yml", Reason: "adds OpenTelemetry Collector, Grafana, Tempo, Loki, and Prometheus services"},
		{Path: "otel-collector.yml", Reason: "routes OTLP traces, metrics, and logs to local LGTM services"},
		{Path: "tempo.yml", Reason: "enables Tempo OTLP trace ingestion"},
		{Path: "loki.yml", Reason: "configures local Loki log storage and OTLP-compatible metadata"},
		{Path: "prometheus.yml", Reason: "scrapes OpenTelemetry Collector metrics"},
		{Path: ".env.observability", Reason: "documents OTLP environment variables for app containers and local runs"},
		{Path: "grafana/provisioning/datasources/datasources.yml", Reason: "provisions Prometheus, Loki, and Tempo datasources"},
		{Path: "grafana/provisioning/dashboards/dashboards.yml", Reason: "registers generated dashboards"},
		{Path: "grafana/dashboards/service-overview.json", Reason: "creates a starter service overview dashboard"},
		{Path: ".extent/plan.json", Reason: "records the exact plan applied by the CLI"},
		{Path: ".extent/report.md", Reason: "records usage and rollback guidance"},
	}

	if len(result.Runtimes) == 0 {
		plan.Warnings = append(plan.Warnings, "no supported runtime detected yet; V1 will only write observability stack files")
	}
	if len(result.ComposeFiles) == 0 {
		plan.Warnings = append(plan.Warnings, "no Docker Compose file detected; use the generated observability compose file alongside your app stack")
	}
	if len(result.Frameworks) == 0 {
		plan.Warnings = append(plan.Warnings, "no supported framework detected; runtime-specific code instrumentation should be added after review")
	}

	plan.NextCommands = []string{
		"extent apply --branch observability/otel-lgtm <repo>",
		"docker compose -f docker-compose.observability.yml up -d",
		"extent verify <repo>",
		"open Grafana at http://localhost:3000",
	}
	return plan
}

func prefixValues(prefix string, values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, prefix+":"+value)
	}
	return out
}
