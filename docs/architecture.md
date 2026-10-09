# Architecture

Extent is a single Go binary. It reads a target repo, writes reviewable files,
and talks to a local Docker Compose stack. Every mutation is planned first and
recorded so it can be previewed and undone.

## Workflow

Commands run in this order for a typical project:

```text
scan -> analyze -> plan -> apply -> instrument -> stack up -> verify -> smoke -> report -> score
```

```mermaid
flowchart TD
    subgraph Discovery ["1. Discovery & Planning"]
        A["Target Repository"] --> B["extent scan<br/>(Runtime & Framework Detection)"]
        B --> C["extent analyze<br/>(AST, Routes, DB, Queues)"]
        C --> D["extent plan<br/>(Rollout Strategy & Profiles)"]
    end

    subgraph Generation ["2. Provisioning & Instrumentation"]
        E["extent apply<br/>(extent.yaml & LGTM Configs)"]
        E --> F["extent instrument<br/>(OTel Bootstrap Injection)"]
        F --> G["extent deps<br/>(SDK Dependency Sync)"]
    end

    subgraph Runtime ["3. Stack & Verification"]
        H["extent stack up<br/>(Docker Compose LGTM Stack)"]
        H --> I["extent verify / doctor<br/>(Config & Endpoint Checks)"]
        I --> J["extent smoke<br/>(Synthetic Traffic & Correlation)"]
    end

    subgraph Governance ["4. Analysis & Quality"]
        K["extent cardinality<br/>(Loki Label Risk Score)"]
        L["extent score<br/>(Telemetry Quality Rating)"]
        M["extent report / baseline<br/>(Prometheus Bottlenecks & Diff)"]
    end

    D --> E
    G --> H
    J --> K
    J --> L
    J --> M
```

## Design goals

Adding observability to a project usually means stitching together SDK setup,
environment variables, Docker networking, collectors, dashboards, and
verification by hand. Extent turns that into a guided workflow. It compares as
follows:

| Feature | Extent | Manual |
| :--- | :--- | :--- |
| **Structural integrity** | Generates a bounded OpenTelemetry bootstrap and local LGTM configuration with pinned dependencies for supported targets. | Manual version selection and configuration. |
| **Cross-domain sync** | Transactional changes with ownership checks, exact backups, idempotent apply, and conflict-safe undo. | Manual coordination of SDKs and configuration files. |
| **Source changes** | Syntax-aware Go import and lifecycle edits. Node and Python bootstrap edits are deliberately constrained. Deep codemods are experimental. | Manual source editing. |
| **Operational safety** | Built-in cardinality checks and telemetry quality scoring (`extent score`). | Risk of cardinality explosion or broken trace context. |
| **Feedback loop** | Service-scoped smoke checks correlate synthetic traffic with Prometheus metrics, Tempo traces, and Loki logs. | Custom traffic generation and manual validation. |

Extent treats the OpenTelemetry APIs, SDKs, semantic attributes, resources,
instrumentation scopes, and the Collector pipeline as the observability
contract. Generated files are meant to be reviewed, diffed, and undone rather
than hidden behind one-shot scripts.

## Packages

```text
cmd/extent                CLI entrypoint and command handlers (cmd_*.go)
recipes/                  Framework recipe metadata (detect, inject, support level)
internal/buildinfo        Version and build metadata
internal/scanner          Repo detection: runtimes, frameworks, package managers, entrypoints
internal/analyzer         Deep repository analysis and recommendations inputs
internal/config           extent.yaml parsing and validation
internal/contract         extent.yaml observability contract rendering
internal/planner          Proposed changes and next steps
internal/templates        Generated LGTM and OpenTelemetry files (Compose, Collector, Prometheus, Loki, Tempo, Grafana)
internal/stack            Docker Compose wrapper: preflight, up, down, status, readiness
internal/instrumenter     Node, Python, Go, and .NET bootstrap source injection
internal/codemods         Generated experimental deep AST/codemod patcher bundle
internal/deps             Dependency install command planning and execution
internal/gitops           Branch creation and git helpers
internal/fileops          Transactional writes, backups, and undo for generated files
internal/state            .extent/state.json ownership manifest
internal/process          Bounded subprocess runner used by stack and deps
internal/observability    Bounded HTTP client and URL validation for query APIs
internal/evidence         Tempo, Loki, and Prometheus evidence query clients
internal/smoke            App traffic and telemetry correlation checks
internal/verifier         Generated-file and endpoint verification (verify, doctor)
internal/reporter         Prometheus-backed bottleneck report synthesis
internal/recommendations  Evidence-backed remediation synthesis
internal/baseline         Before/after snapshot persistence and comparison
internal/cardinality      Loki label cardinality safety analysis
internal/scorer           Telemetry quality scoring
testdata/e2e/             Tiny fixture apps used by the live LGTM acceptance workflow
```

## Component diagram

```mermaid
flowchart TD
    CLI["cmd/extent<br/>(CLI Entrypoint & Dispatcher)"]

    subgraph AnalysisLayer ["Analysis & Contract Engine"]
        SCAN["internal/scanner"]
        ANALYZER["internal/analyzer"]
        PLANNER["internal/planner"]
        CONTRACT["internal/contract"]
        CONF["internal/config"]
    end

    subgraph MutationLayer ["Instrumentation & GitOps"]
        INST["internal/instrumenter"]
        DEPS["internal/deps"]
        CODEMOD["internal/codemods"]
        GITOPS["internal/gitops"]
        RECIPES["recipes/"]
    end

    subgraph ProvisioningLayer ["Stack Provisioning & Control"]
        TEMPL["internal/templates"]
        STACK["internal/stack"]
    end

    subgraph VerificationLayer ["Verification & Evidence Engine"]
        VERIFY["internal/verifier"]
        SMOKE["internal/smoke"]
        EVID["internal/evidence"]
        REPORT["internal/reporter"]
        RECOM["internal/recommendations"]
        BASE["internal/baseline"]
        CARD["internal/cardinality"]
        SCORE["internal/scorer"]
    end

    subgraph TargetArtifacts ["Target System & LGTM Stack"]
        TARGET_CODE["Application Source & Dependencies"]
        COMPOSE["docker-compose.observability.yml"]
        COLLECTOR["OTel Collector (OTLP Ingest)"]
        BACKENDS["Prometheus / Tempo / Loki / Grafana"]
    end

    CLI --> SCAN
    CLI --> INST
    CLI --> TEMPL
    CLI --> VERIFY

    SCAN --> ANALYZER
    ANALYZER --> PLANNER
    PLANNER --> CONTRACT
    CONTRACT --> CONF
    CONF --> TEMPL

    INST --> TARGET_CODE
    DEPS --> TARGET_CODE
    CODEMOD --> TARGET_CODE

    TEMPL --> COMPOSE
    STACK --> COMPOSE
    COMPOSE --> COLLECTOR
    COLLECTOR --> BACKENDS

    VERIFY --> TARGET_CODE
    SMOKE --> COLLECTOR
    EVID --> BACKENDS
    REPORT --> BACKENDS
```

## Exit codes and JSON

The exit-code table and the JSON `schema` contract are in [cli.md](cli.md).
Schemas live in [`schemas/`](../schemas/).
