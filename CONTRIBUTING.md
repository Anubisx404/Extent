# Contributing to Extent

Thank you for contributing to Extent. This document outlines the development workflow, validation requirements, and guidelines for proposing changes.

## Prerequisites

- **Go**: 1.22 or higher.
- **Docker & Docker Compose**: Optional for development; required only when running live LGTM stack verification or smoke checks.

## Local Validation

Before submitting changes, ensure your code builds and passes all tests and lint checks locally:

```powershell
go test ./...
go vet ./...
go build ./cmd/extent
```

### Optional Release Build Validation

If you make changes that affect build tags, ldflags, or packaging assets, you can run a GoReleaser snapshot build:

```powershell
goreleaser release --snapshot --clean
```

## Continuous Integration

Extent runs automated acceptance checks on pull requests and pushes to `main` via the `generated-fixtures` job to verify cross-platform generated-artifact transactions across Node, Python, and Go targets. The `live-lgtm` job is a manual dispatch workflow that verifies generated Compose syntax and stack lifecycle in a temporary workspace; full service-scoped telemetry assertions require a running instrumented fixture.

## Target Repository Mutations & Generated Artifacts

Extent inspects and mutates external target repositories. To preserve user trust and repository safety, adhere to the following principles when implementing features that touch target code or configurations:

1. **Safety & Previewability**: Any command that mutates target code or writes new configurations must provide safe preview and dry-run options (e.g., `--dry-run`, `--show-diff`) and respect undo capabilities where applicable.
2. **Deterministic Contracts**: Generated files (such as `extent.yaml`, OpenTelemetry Collector pipelines, LGTM stack configurations, and Grafana provisioning artifacts) must strictly follow repository contract schemas and templates.
3. **Idempotency & Non-Destruction**: Applying changes multiple times must produce consistent, idempotent results without unintentionally overwriting unrelated user files.

## Secrets and Fixture Hygiene

- Never commit real credentials, API tokens, sensitive endpoint paths, or private data into test fixtures, baseline test samples, mock data, or report templates.
- Always use sanitized mock hostnames, placeholder secrets, and synthetic payloads in test suites.

## Commit Message Convention

Extent follows the [Conventional Commits](https://www.conventionalcommits.org/) specification:

- `feat:` New features or capabilities
- `fix:` Bug fixes
- `docs:` Documentation-only changes
- `chore:` Tooling, dependency, or repository maintenance
- `test:` Adding or updating tests
- `refactor:` Code refactoring without behavioral changes

## Submitting Pull Requests

1. Create a descriptive branch for your work.
2. Ensure all local tests and vet checks pass.
3. Open a Pull Request referencing relevant context, describing the scope of changes, local verification steps, and any telemetry or generated artifact impact.
