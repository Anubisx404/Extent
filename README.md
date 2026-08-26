# Extent

[![Go](https://img.shields.io/badge/Go-1.22+-00ADD8?logo=go&logoColor=white)](https://go.dev/)
[![Docker](https://img.shields.io/badge/Docker-Compose-2496ED?logo=docker&logoColor=white)](https://docs.docker.com/compose/)
[![OpenTelemetry](https://img.shields.io/badge/OpenTelemetry-OTLP-425CC7?logo=opentelemetry&logoColor=white)](https://opentelemetry.io/)
[![Grafana LGTM](https://img.shields.io/badge/Grafana-LGTM-F46800?logo=grafana&logoColor=white)](https://grafana.com/oss/)
[![Status](https://img.shields.io/badge/status-V1_CLI_foundation-brightgreen)](#v1-status)

Extent is a CLI-first observability bootstrapper for local projects. It acts
like a repo-aware observability engineer: it scans the project, maps runtime
and framework signals, generates an OpenTelemetry observability contract,
provisions a Docker LGTM stack, injects safe bootstrap instrumentation, verifies
telemetry flow, scores signal quality, and generates bottleneck reports from
Prometheus evidence.

The product direction is simple: point Extent at a project, let it create an
observability branch, review the diff, run the local stack, and inspect your
system through Grafana.

## Why Extent Exists

Adding observability to an existing project usually means stitching together
SDK setup, environment variables, Docker networking, collectors, dashboards,
and verification by hand. Extent turns that into a guided workflow:

```text
scan -> analyze -> plan -> apply -> instrument -> stack up -> verify -> report -> score
```

Extent treats OpenTelemetry APIs, SDKs, semantic attributes, resources,
instrumentation scopes, and the Collector pipeline as the observability
contract. Generated files are meant to be reviewed, diffed, and undone rather
than hidden behind one-shot scripts.

## Extent vs. Manual Implementation

While a senior engineer can manually instrument a project, Extent provides a 
systematic approach that reduces the "Observability Cold Start" from days to 
minutes.

| Feature | Extent Automated | Manual Implementation |
| :--- | :--- | :--- |
| **Structural Integrity** | Generates bounded OpenTelemetry bootstrap and local LGTM configuration with pinned dependencies for supported targets. | Requires manual version selection and configuration. |
| **Cross-Domain Sync** | Uses reviewable, transactional changes with ownership checks, exact backups, idempotent apply, and conflict-safe undo. | Requires manual coordination of SDKs and configuration files. |
| **Source Changes** | Uses syntax-aware Go import/lifecycle edits; Node and Python bootstrap edits are deliberately constrained. Deep codemods remain experimental. | Requires manual source editing. |
| **Operational Safety** | Includes built-in cardinality safety checks and telemetry quality scoring (`extent score`). | Risk of "Cardinality Explosion" or broken trace context propagation. |
| **Feedback Loop** | Service-scoped smoke checks correlate synthetic traffic with Prometheus metrics, Tempo traces, and Loki logs. | Requires custom traffic generation and manual validation. |

## V1 Status

Implemented:

- Repository scanner for Node, Python, Go, .NET, and Java signals.
- Framework detection for Express, NestJS, Next.js, Fastify, SvelteKit,
  FastAPI, Django, Flask, ASP.NET Core, and Spring Boot.
- Package-manager detection for npm, pnpm, yarn, Go modules, Poetry, uv,
  NuGet, Maven, and Gradle.
- Database library detection for PostgreSQL, MySQL, MongoDB, Redis, SQLAlchemy,
  Prisma/ORM hints, and related client packages.
- Docker Compose detection.
- Deep analyzer detection for entrypoints, REST routes, external HTTP clients,
  loggers, queues/jobs, Docker ports/env files/networks, and test commands.
- Plan generation with proposed changes and next commands.
- Safe apply flow with optional branch creation.
- Generated `extent.yaml` observability contract.
- Apply profiles: `minimal`, `full`, `high-cardinality-safe`, `low-resource`,
  and `report-heavy`.
- Generated OpenTelemetry Collector, Tempo, Loki, Prometheus, and Grafana
  provisioning files.
- Collector processors for resource enrichment, redaction, memory limiting,
  batching, and tail sampling.
- Generated `.env.observability` file for app telemetry settings.
- Stable `zero-code` and constrained `bootstrap` instrumentation modes for
  Node.js, Python, and Go. Bootstrap dependencies are pinned and incompatible
  existing constraints fail before mutation.
- Node CommonJS/ESM lifecycle, HTTP tracing, metrics, logs, request correlation,
  and supported database auto-instrumentation.
- Python Flask/FastAPI lifecycle, traces, metrics, logs, request correlation,
  and optional supported database instrumentation.
- Go provider setup, AST-based import/lifecycle injection, dependency
  reconciliation, and shutdown flushing. Application spans still require Go
  OpenTelemetry API or framework instrumentation in the target application.
- `deep` mode is experimental and requires `--experimental`; generated
  TypeScript, LibCST, Roslyn, OpenRewrite, and JavaParser codemods are not part
  of the stable V1 support contract.
- Optional dependency install command execution.
- Telemetry smoke verification through app traffic plus service-specific
  Prometheus, Tempo, and Loki correlation. Collector counters are reported only
  as supporting global evidence.
- Portable core stack by default. Linux host/container exporters are available
  only through the explicit host-metrics profile.
- Starter Grafana dashboard panels for app latency, DB latency, container CPU,
  container memory, and host CPU.
- Grafana datasource provisioning for Tempo trace-to-log, trace-to-metric, and
  service-map links.
- Prometheus alert rules for high errors, high latency, slow DB queries, queue
  backlog, memory growth, and missing telemetry.
- Prometheus-backed bottleneck reports in text, Markdown, HTML, and JSON.
- Tempo and Loki evidence query clients for trace examples and log anomalies.
- Before/after baseline storage and report comparison.
- Telemetry quality scoring through `extent score`.
- Cardinality safety analysis through `extent cardinality`.
- Optional end-to-end `verify --url` flow that sends synthetic app traffic and
  queries Prometheus, Loki, and Tempo readiness.
- Framework recipe files under `recipes/` for Node, Python, .NET, Java, and Go.
- Unit tests for scanner, planner, template, instrumenter, dependency, and smoke packages.

Not yet implemented:

- Stable bootstrap or zero-code mutation for .NET and Java; these runtimes are
  detection-only outside experimental deep codemod generation.
- Stable deep instrumentation for any language.
- Package-manager publishing or self-update.
- Svelte local UI.
- Tauri desktop package.
- Browser-level Grafana UI click automation. Extent validates generated config,
  backing APIs, and live Grafana datasource JSON through `verify --grafana`.

## Install And Run

Prerequisite: Go 1.22+.

From this repo:

```powershell
go test ./...
go run ./cmd/extent version
```

Scan a target project:

```powershell
go run ./cmd/extent scan C:\path\to\repo
```

Run the deeper analyzer:

```powershell
go run ./cmd/extent analyze C:\path\to\repo
```

Generate a plan:

```powershell
go run ./cmd/extent plan C:\path\to\repo
```

Apply the V1 observability files on a branch:

```powershell
go run ./cmd/extent apply --branch observability/otel-lgtm --profile full C:\path\to\repo
```

Inject OpenTelemetry bootstrap code and dependency manifest updates:

```powershell
go run ./cmd/extent instrument --mode bootstrap C:\path\to\repo
go run ./cmd/extent instrument --mode bootstrap --apply C:\path\to\repo
go run ./cmd/extent instrument --mode zero-code --dry-run C:\path\to\repo
go run ./cmd/extent instrument --mode deep --experimental --show-diff C:\path\to\repo
go run ./cmd/extent instrument --mode deep --experimental --apply C:\path\to\repo
```

Experimental deep mode writes `extent.codemods/`, which contains reviewable patchers for
HTTP routes, DB calls, queues, outbound HTTP, business functions, and log
statements. With `--apply`, Extent runs the detected language codemod commands;
missing toolchains are reported as command failures instead of being ignored.

Preview or run the dependency install command:

```powershell
go run ./cmd/extent deps C:\path\to\repo
go run ./cmd/extent deps --install C:\path\to\repo
```

Start the generated stack from the target repo:

```powershell
go run ./cmd/extent stack up --wait --timeout 2m C:\path\to\repo
```

Verify generated files and local tools:

```powershell
go run ./cmd/extent verify C:\path\to\repo
```

Run end-to-end verification against a live app and LGTM stack:

```powershell
go run ./cmd/extent verify --url http://localhost:8080/health --prometheus http://localhost:9090 --grafana http://localhost:3000 C:\path\to\repo
```

Send traffic and prove telemetry reached the Collector:

```powershell
go run ./cmd/extent smoke --url http://localhost:8080/health --service checkout --prometheus http://localhost:9090 --tempo http://localhost:3200 --loki http://localhost:3100
```

Generate a bottleneck report from Prometheus:

```powershell
go run ./cmd/extent report --prometheus http://localhost:9090
go run ./cmd/extent report --last 30m --format markdown --prometheus http://localhost:9090 --loki http://localhost:3100 --tempo http://localhost:3200 --include-data C:\path\to\repo
go run ./cmd/extent report --last 1h --format html --prometheus http://localhost:9090 --compare baseline --include-data C:\path\to\repo
```

Capture and compare a baseline:

```powershell
go run ./cmd/extent baseline --prometheus http://localhost:9090 --loki http://localhost:3100 --tempo http://localhost:3200 C:\path\to\repo
go run ./cmd/extent report --compare baseline --format markdown
```

Check Loki label safety:

```powershell
go run ./cmd/extent cardinality C:\path\to\repo
```

Score telemetry quality:

```powershell
go run ./cmd/extent score --prometheus http://localhost:9090
```

Open Grafana:

```text
http://localhost:3000
```

## CLI Commands

```text
extent scan [--json] [repo]
extent analyze [--json] [repo]
extent plan [--json] [repo]
extent apply [--branch name] [--force] [--profile name] [repo]
extent instrument [--mode zero-code|bootstrap|deep] [--experimental] [--entrypoint path] [--dry-run|--apply|--undo] [--show-diff] [--json] [repo]
extent deps [--install] [repo]
extent smoke --url app-url --service service-name [--prometheus url] [--tempo url] [--loki url] [--requests n] [--json]
extent report [--last 30m] [--format text|markdown|html|json] [--prometheus url] [--loki url] [--tempo url] [--compare baseline] [--include-data] [--json] [repo]
extent baseline [--prometheus url] [--loki url] [--tempo url] [--json] [repo]
extent cardinality [--json] [repo]
extent score [--prometheus url] [--json]
extent stack up [--wait=true] [--timeout 2m] [--project-name name] [repo]
extent stack down [repo]
extent stack status [--json] [repo]
extent verify [--url app-url] [--prometheus url] [--loki url] [--tempo url] [--grafana url] [--requests n] [--json] [repo]
extent doctor [--json] [repo]
extent version
```

`doctor` performs static project/tool checks. `verify` performs generated-file
and endpoint readiness checks, with optional live application requests.

## Generated Files

`extent apply` writes these files into the target repo:

```text
docker-compose.observability.yml
otel-collector.yml
tempo.yml
loki.yml
prometheus.yml
prometheus-alerts.yml
extent.yaml
.env.observability
grafana/
  provisioning/
    datasources/datasources.yml
    dashboards/dashboards.yml
  dashboards/service-overview.json
.extent/
  plan.json
  report.md
```

The generated stack contains:

- OpenTelemetry Collector for OTLP ingest.
- Tempo for traces.
- Loki for logs.
- Prometheus for metrics.
- Optional cAdvisor and node_exporter services when the generated host-metrics
  profile is explicitly enabled on a compatible Linux Docker host.
- Grafana with provisioned datasources and a starter dashboard.

## Architecture

```text
cmd/extent              CLI entrypoint
internal/scanner        Repo detection and signals
internal/analyzer       Deep repository analysis and recommendations
internal/contract       extent.yaml observability contract rendering
internal/planner        Proposed changes and next steps
internal/gitops         Branch creation and git helpers
internal/instrumenter   Node, Python, and Go OpenTelemetry source injection
internal/deps           Dependency install command planning/execution
internal/smoke          App traffic and Prometheus telemetry checks
internal/evidence       Tempo, Loki, and Prometheus evidence query clients
internal/reporter       Prometheus-backed bottleneck report synthesis
internal/recommendations Evidence-backed remediation synthesis
internal/baseline       Before/after snapshot persistence and comparison
internal/cardinality    Loki label cardinality safety analysis
internal/codemods       Generated deep AST/codemod patcher bundle
internal/scorer         Telemetry quality scoring
internal/templates      Generated LGTM/OpenTelemetry files
internal/stack          Docker Compose wrapper
internal/verifier       Local verification checks
recipes/                Framework-specific instrumentation recipes
plans/                  Delivery plans and phase notes
docs/                   Roadmap and implementation alternatives
```