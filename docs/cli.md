# Extent CLI reference

This page lists every command, its flags and defaults, what it does, and the
stable exit codes. Flags and defaults come from `extent <command> -h`. Run
`extent help` for the one-screen summary.

Conventions:

- `[repo]` is an optional project path as the last positional argument. It
  defaults to `.`. `score` and `version` take no repo argument.
- Go-style flags are accepted with one or two dashes (`-json` and `--json`).
- Examples use `extent`. On Windows, use `.\extent.exe` (see the README quickstart).
- Commands with `--json` print one JSON object whose first member is
  `"schema"`. See [JSON output](#json-output).

## Command summary

| Command | Purpose | Writes files? |
| --- | --- | --- |
| `version` | Print build metadata | No |
| `scan` | Detect runtimes, frameworks, package managers, database libraries, Compose files, entrypoints | No |
| `analyze` | Deep repo analysis: routes, HTTP clients, loggers, queues, Docker, test commands | No |
| `plan` | Show proposed changes and next commands | No |
| `apply` | Write the generated LGTM stack files and `extent.yaml` | Yes |
| `instrument` | Preview, apply, or undo OpenTelemetry bootstrap code | Yes (with `--apply` or `--undo`) |
| `deps` | Show or run the dependency install command | Only with `--install` |
| `smoke` | Send synthetic traffic and check telemetry arrived | No |
| `report` | Bottleneck report from Prometheus evidence (optional soak) | No (except `--compare` input) |
| `baseline` | Capture a before snapshot to `.extent/baseline.json` | Yes |
| `cardinality` | Loki label cardinality safety analysis | No |
| `score` | Telemetry quality score for one service | No |
| `stack up` / `down` / `status` | Start, stop, and inspect the Docker Compose stack | Compose state only |
| `verify` | Generated-file and endpoint readiness checks | No |
| `doctor` | Same checks as `verify`, reported under the `doctor` name | No |

## version

```text
extent version [--json]
```

- `--json`: print JSON build metadata (default: off).

## scan

```text
extent scan [--json] [repo]
```

Detects runtimes, frameworks, package managers, database libraries, Compose
files, and entrypoints. Read-only.

- `--json`: print JSON output (default: off).

## analyze

```text
extent analyze [--json] [--dynamic] [repo]
```

Deep analysis of entrypoints, REST routes, external HTTP clients, loggers,
queues and jobs, Docker ports, env files and networks, and test commands.

- `--json`: print JSON output (default: off).
- `--dynamic`: attempt runtime reflection for registered framework routes
  (default: off). For Node projects this runs the entrypoints with `node`. Use it
  only on trusted code. See [SECURITY.md](../SECURITY.md).

## plan

```text
extent plan [--json] [repo]
```

Prints detected signals, the proposed file changes, and the next commands. It
does not write files.

- `--json`: print JSON output (default: off).

## apply

```text
extent apply [--branch name] [--force] [--profile name] [repo]
```

Writes the generated observability files and `extent.yaml` into the repo. See
[generated-files.md](generated-files.md) for the file list.

- `--branch`: create or switch to a git branch before writing files (default: none).
  The working tree must be clean, or Extent refuses.
- `--force`: overwrite generated observability files (default: off). Extent-owned
  files you have edited are still refused. See [troubleshooting](troubleshooting.md).
- `--profile`: override the `extent.yaml` profile. One of `minimal`, `full`,
  `high-cardinality-safe`, `low-resource`, `report-heavy`.

`apply` has no `--json` flag.

## instrument

```text
extent instrument [--mode zero-code|bootstrap|deep] [--experimental] [--entrypoint path] [--force] [--dry-run|--apply|--undo] [--show-diff] [--json] [repo]
```

Without `--apply`, `instrument` previews the change and writes nothing.

- `--mode`: `zero-code`, `bootstrap`, or `deep` (default: `bootstrap`).
  `deep` also requires `--experimental`. Support per runtime is in
  [support-matrix.md](support-matrix.md).
- `--experimental`: acknowledge the experimental deep-mode limitations (default: off).
  Required with `--mode deep`, except with `--undo`.
- `--entrypoint`: project-relative entrypoint when detection is ambiguous (default: detected).
- `--force`: replace conflicting Extent instrumentation outputs, keeping an exact
  backup (default: off). Needed to re-run deep mode when its target files exist.
- `--dry-run`: show intended changes without writing (default: on unless `--apply`).
- `--apply`: write the changes (default: off).
- `--undo`: remove the Extent instrumentation files recorded in `.extent/state.json`.
  It does not need `--apply`, and Extent refuses (exit 1) if a file it created or
  changed has been modified since. `--undo` cannot be combined with `--apply` or `--dry-run`.
- `--show-diff`: print a simple diff of generated files (default: off). Cannot be
  combined with `--apply` or `--undo`.
- `--json`: print JSON output (default: off).

## deps

```text
extent deps [--install] [repo]
```

Prints the detected package-manager install command. With `--install` it runs
that command in the repo.

- `--install`: run the detected install command (default: off).

`deps` has no `--json` flag.

## smoke

```text
extent smoke --url app-url --service service-name [--prometheus url] [--tempo url] [--loki url] [--requests n] [--duration d] [--concurrency n] [--rate rps] [--json]
```

Sends synthetic requests to your app, then checks that the service's telemetry
reached Prometheus, Tempo, and Loki. Required: `--url` and `--service`.

- `--url`: application URL to request (required).
- `--service`: target `service.name` for correlated trace verification (required).
- `--prometheus`: Prometheus base URL (default `http://localhost:9090`).
- `--tempo`: Tempo base URL (default `http://localhost:3200`).
- `--loki`: Loki base URL (default `http://localhost:3100`).
- `--requests`: number of requests, 1 to 1000 (default `3`). Ignored with `--duration`.
- `--duration`: sustained soak duration instead of a fixed request count (default: off).
- `--concurrency`: concurrent synthetic request workers (default `1`).
- `--rate`: requests per second; `0` means unlimited. Unset defaults to 50 with
  `--duration` and unlimited otherwise.
- `--json`: print JSON output (default: off).

## report

```text
extent report --service service-name [--soak d --url app-url] [--concurrency n] [--rate rps] [--last 30m] [--format text|markdown|html|json] [--prometheus url] [--loki url] [--tempo url] [--compare baseline|path] [--include-data] [--json] [repo]
```

Builds a bottleneck report from Prometheus, with optional Loki and Tempo evidence.
Required: `--service`. `--soak` also requires `--url`.

- `--service`: target `service.name` for scoped evidence (required).
- `--last`: lookback window label (default `30m`).
- `--format`: `text`, `markdown`, `html`, or `json` (default `text`).
- `--prometheus`: Prometheus base URL (default `http://localhost:9090`).
- `--loki`: Loki base URL for log evidence (default: none).
- `--tempo`: Tempo base URL for trace evidence (default: none).
- `--compare`: compare against a baseline file, or the word `baseline` for `.extent/baseline.json`.
- `--include-data`: append the raw gathered data (default: off).
- `--soak`: sustained soak duration (default: off). Requires `--url`.
- `--url`: target application URL for soak testing.
- `--concurrency`: concurrent soak workers (default `1`).
- `--rate`: soak rate limit in requests per second; `0` means unlimited. Unset
  defaults to 50 with `--soak`.
- `--json`: print JSON output (default: off).

A report with warnings exits 5.

## baseline

```text
extent baseline --service service-name [--last 30m] [--prometheus url] [--loki url] [--tempo url] [--json] [repo]
```

Captures the current evidence as a snapshot at `.extent/baseline.json`, for later
`report --compare baseline`. Required: `--service`.

- `--service`: target `service.name` for scoped evidence (required).
- `--last`: bounded evidence lookback (default `30m`).
- `--prometheus`: default `http://localhost:9090`.
- `--loki`: default `http://localhost:3100`.
- `--tempo`: default `http://localhost:3200`.
- `--json`: print the snapshot as JSON (default: off).

If evidence collection fails, the command exits 5 and saves nothing.

## cardinality

```text
extent cardinality [--prometheus url] [--json] [repo]
```

Scores Loki label cardinality risk from the repo's logging code. With
`--prometheus`, it also analyses the live label cardinality of
`http_server_duration_milliseconds_count`.

- `--prometheus`: Prometheus base URL for live analysis (default: none, static only).
- `--json`: print JSON output (default: off).

## score

```text
extent score [--service service-name] [--prometheus url] [--tempo url] [--loki url] [--json]
```

Rates telemetry quality for one service. Takes no repo argument.

- `--service`: target `service.name`. Enables the service identity and
  log/trace correlation checks (default: none).
- `--prometheus`: default `http://localhost:9090`.
- `--tempo`: Tempo base URL. Resolves a correlated trace ID for the correlation check (default: none).
- `--loki`: Loki base URL. Required to measure log/trace correlation with `--service` (default: none).
- `--json`: print JSON output (default: off).

## stack

### stack up

```text
extent stack up [--wait=true|false] [--timeout 2m] [--project-name name] [repo]
```

Starts the generated Compose stack (`docker-compose.observability.yml`). Before
starting it checks, in order:

1. The Compose file is a regular file.
2. `docker` is installed.
3. `docker compose version` works. Compose v2 is required. Podman and nerdctl are not supported.
4. `docker info` succeeds (the daemon is running).
5. Each published host port is free on `127.0.0.1`.

- `--wait`: wait for health checks (default `true`). Grafana and Prometheus use
  Compose healthchecks. Loki and Tempo are polled on their `/ready` endpoints.
- `--timeout`: bounded startup and readiness timeout (default `2m`, max `30m`).
- `--project-name`: explicit, deterministic Compose project name (default: derived from the repo).

On success it prints the Grafana address and login hint.

### stack down

```text
extent stack down [repo]
```

Stops the stack. No flags. Named volumes are kept. See
[troubleshooting](troubleshooting.md) for resetting Grafana's password.

### stack status

```text
extent stack status [--json] [repo]
```

Lists each component's state and health.

- `--json`: print structured component state and health (default: off).

## verify

```text
extent verify [--url app-url --service service-name] [--prometheus url] [--loki url] [--tempo url] [--grafana url] [--grafana-user user] [--grafana-password pass] [--grafana-token token] [--requests n] [--json] [repo]
```

Checks the generated files and, when given, live endpoints. Without `--url` it
checks files and tool readiness only. With `--url` it sends synthetic requests
and checks correlated telemetry. `--url` requires `--service`.

- `--url`: application URL to request for end-to-end checks (default: none).
- `--service`: target `service.name` for correlated checks (default: none; required with `--url`).
- `--prometheus`: default `http://localhost:9090`.
- `--loki`: default `http://localhost:3100`.
- `--tempo`: default `http://localhost:3200`.
- `--grafana`: Grafana base URL for live datasource correlation checks (default: none).
- `--grafana-user`: username for basic-auth API checks (default: none).
- `--grafana-password`: password for basic-auth API checks (default: none).
- `--grafana-token`: service account token for Bearer API checks. Takes precedence over basic auth (default: none).
- `--requests`: synthetic requests when `--url` is set, 1 to 1000 (default `3`).
- `--json`: print JSON output (default: off).

When the Grafana flags are not given, credentials are read from the environment
variables `EXTENT_GRAFANA_TOKEN`, `EXTENT_GRAFANA_USER`, and
`EXTENT_GRAFANA_PASSWORD`. Then they fall back to the Grafana login that
`extent apply` recorded in `.env.observability` (user `admin` by default).

A failed check exits 5. With `--json`, the JSON is printed first.

## doctor

```text
extent doctor [--url app-url --service service-name] [--prometheus url] [--loki url] [--tempo url] [--grafana url] [--grafana-user user] [--grafana-password pass] [--grafana-token token] [--requests n] [--json] [repo]
```

Accepts the same flags as `verify` and runs the same checks. The difference is
the name: output and the JSON `schema` member are `doctor`.

## Exit codes

These codes are a stable contract. Scripts and CI depend on them, so existing
numbers will not change. New categories get new numbers.

| Code | Meaning |
| --- | --- |
| `0` | Success. |
| `1` | Internal or unclassified failure. This includes filesystem conflicts (for example, a generated file you edited), refused overwrites, and "no supported runtime detected". |
| `2` | Usage error: bad or unknown flags, unknown command or action, invalid enum or range, missing required flag, extra positional arguments, invalid `extent.yaml`. |
| `3` | Safety refusal: conflicting mutation flags (`--dry-run`, `--apply`, `--undo`; `--show-diff` with `--apply` or `--undo`), or an unsafe path (symlink escape, `..`, absolute or reserved paths). |
| `4` | Unavailable: a required local dependency or service (Docker, Compose daemon, stack lifecycle) could not be used. |
| `5` | Verification failed: telemetry smoke checks, `verify`/`doctor` checks, report warnings, or baseline evidence collection. With `--json`, the JSON is written before exit. |

## JSON output

Every `--json` payload has a top-level `"schema"` member that names its
contract. The contract for each is a JSON Schema file in
[`schemas/`](../schemas/):

| Command | `schema` value | Schema file |
| --- | --- | --- |
| `version` | `extent.version/v1` | `schemas/version.v1.json` |
| `scan` | `extent.scan/v1` | `schemas/scan.v1.json` |
| `analyze` | `extent.analyze/v1` | `schemas/analyze.v1.json` |
| `plan` | `extent.plan/v1` | `schemas/plan.v1.json` |
| `instrument` | `extent.instrument/v1` | `schemas/instrument.v1.json` |
| `smoke` | `extent.smoke/v1` | `schemas/smoke.v1.json` |
| `report` | `extent.report/v1` | `schemas/report.v1.json` |
| `baseline` | `extent.baseline/v1` | `schemas/baseline.v1.json` |
| `cardinality` | `extent.cardinality/v1` | `schemas/cardinality.v1.json` |
| `score` | `extent.score/v1` | `schemas/score.v1.json` |
| `stack status` | `extent.stack-status/v1` | `schemas/stack-status.v1.json` |
| `verify` | `extent.verify/v1` | `schemas/verify.v1.json` |
| `doctor` | `extent.doctor/v1` | `schemas/doctor.v1.json` |

Consumers should check `schema` before reading the payload.
