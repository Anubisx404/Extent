# Extent

[![CI](https://github.com/Anubisx404/Extent/actions/workflows/ci.yml/badge.svg)](https://github.com/Anubisx404/Extent/actions/workflows/ci.yml)
[![GitHub Release](https://img.shields.io/github/v/release/Anubisx404/Extent?logo=github)](https://github.com/Anubisx404/Extent/releases)
[![Go](https://img.shields.io/badge/Go-1.25+-00ADD8?logo=go&logoColor=white)](https://go.dev/)

Extent is a CLI-first observability bootstrapper for local projects. It acts
like a repo-aware observability engineer: it scans the project, maps runtime
and framework signals, generates an OpenTelemetry observability contract,
provisions a Docker LGTM stack, injects safe bootstrap instrumentation, verifies
telemetry flow, scores signal quality, and generates bottleneck reports from
Prometheus evidence. How it fits together is in [docs/architecture.md](docs/architecture.md).

## Install

Building Extent needs **Go 1.25+**. Target projects can use any Go version. The
local stack needs **Docker with Compose v2**.

**Release binaries.** Download the archive for your OS and architecture from
[GitHub Releases](https://github.com/Anubisx404/Extent/releases). Each release
ships `checksums.txt`, a cosign signature, and provenance attestations. To verify
them, follow [RELEASING.md](RELEASING.md#6-verify-the-published-artifacts).

**With Go:**

```sh
go install github.com/Anubisx404/Extent/cmd/extent@latest
extent version
```

`go install` writes to `$(go env GOPATH)/bin` (`%USERPROFILE%\go\bin` on Windows).

## Quickstart (about 5 minutes)

This uses a Node, Python, or Go app in a git repo with a clean working tree.
Commands are shown for bash (macOS and Linux) and PowerShell (Windows). If
`extent` is not on your `PATH`, use `./extent` or `.\extent.exe`.

```bash
REPO=~/src/checkout
```

```powershell
$REPO = "C:\src\checkout"
```

**1. Scan** (read-only):

```bash
extent scan "$REPO"
```

```powershell
extent scan $REPO
```

**2. Generate the observability files on a new branch.** This writes `extent.yaml`,
the Collector, Prometheus, Loki, Tempo, Grafana, and Compose files, and
`.env.observability`. `--branch` needs a clean git tree. See
[docs/generated-files.md](docs/generated-files.md).

```bash
extent apply --branch observability/otel-lgtm --profile full "$REPO"
```

```powershell
extent apply --branch observability/otel-lgtm --profile full $REPO
```

**3. Preview, then apply, the bootstrap instrumentation.** Review the diff first.

```bash
extent instrument --mode bootstrap --dry-run "$REPO"
extent instrument --mode bootstrap --apply "$REPO"
```

```powershell
extent instrument --mode bootstrap --dry-run $REPO
extent instrument --mode bootstrap --apply $REPO
```

**4. Install SDK dependencies:**

```bash
extent deps --install "$REPO"
```

```powershell
extent deps --install $REPO
```

**5. Start the LGTM stack** (Docker must be running):

```bash
extent stack up --wait --timeout 2m "$REPO"
```

```powershell
extent stack up --wait --timeout 2m $REPO
```

**6. Run the app with telemetry settings** in the same shell. From the host, the
endpoint is `localhost:4318`.

```bash
export OTEL_SERVICE_NAME=checkout
export OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318
npm start   # or however you normally start the app
```

```powershell
$env:OTEL_SERVICE_NAME = "checkout"
$env:OTEL_EXPORTER_OTLP_ENDPOINT = "http://localhost:4318"
npm start   # or however you normally start the app
```

**7. Send traffic and check that telemetry arrived:**

```bash
extent smoke --url http://localhost:8080/health --service checkout --requests 10
```

```powershell
extent smoke --url http://localhost:8080/health --service checkout --requests 10
```

**8. Verify the stack end to end.** Grafana credentials come from the values `apply` recorded.

```bash
extent verify --url http://localhost:8080/health --service checkout --grafana http://localhost:3000 "$REPO"
```

```powershell
extent verify --url http://localhost:8080/health --service checkout --grafana http://localhost:3000 $REPO
```

**9. Open Grafana** at http://localhost:3000. Log in as user `admin`, with the
password from `GRAFANA_ADMIN_PASSWORD` in `.env.observability`:

```bash
grep GRAFANA_ADMIN_PASSWORD "$REPO/.env.observability"
```

```powershell
Select-String GRAFANA_ADMIN_PASSWORD "$REPO\.env.observability"
```

**10. Stop, or undo the instrumentation.** `stack down` keeps data. `instrument --undo`
refuses if you edited the injected files.

```bash
extent stack down "$REPO"
extent instrument --undo "$REPO"
```

```powershell
extent stack down $REPO
extent instrument --undo $REPO
```

If a step fails, see [docs/troubleshooting.md](docs/troubleshooting.md).

## Support

A summary. The full table, with per-mode status and runtime notes, is in
[docs/support-matrix.md](docs/support-matrix.md).

| Runtime | Frameworks | Bootstrap | Deep (codemods) |
| --- | --- | --- | --- |
| Node.js | Express, NestJS, Next.js, Fastify | stable | experimental |
| Python | Flask, FastAPI, Django | stable | experimental |
| Go | net/http, Gin, Fiber | stable (HTTP spans need `otelhttp` or framework instrumentation) | detection-only |
| .NET | ASP.NET Core (with EF Core) | stable | experimental |
| Java | Spring Boot | detection-only | detection-only |
| Svelte | Svelte, SvelteKit | detection-only | detection-only |

Deep mode needs `--experimental` and is outside the stable support contract.

## Documentation

- [CLI reference](docs/cli.md): commands, flags and defaults, exit codes, JSON schemas
- [Generated files and the LGTM stack](docs/generated-files.md): what `apply` writes, profiles, ports, Grafana login
- [Architecture](docs/architecture.md): workflow, packages, diagrams
- [Support matrix](docs/support-matrix.md): runtime, framework, and mode status
- [Troubleshooting](docs/troubleshooting.md): ports, Docker, traces, Grafana login, undo, re-running `apply`
- [Upgrading](docs/upgrading.md): moving from v1.0.x
- [Security policy](SECURITY.md): read before `analyze --dynamic` or deep mode, which run target code
- [Contributing](CONTRIBUTING.md) and [Releasing](RELEASING.md)
- [License](LICENSE)
