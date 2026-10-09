# Troubleshooting

Start with the check that matches your symptom. `extent doctor` runs static
checks on the repo and tools. `extent stack status` shows each container's
state. `extent verify --url ... --service ...` runs end-to-end checks.
Failed checks exit with code 5, which is described in [cli.md](cli.md#exit-codes).

## Ports already in use

`extent stack up` checks the host ports before it starts anything. It stops
with `host port <port> is unavailable` if one of these is taken:

| Port | Service |
| --- | --- |
| 3000 | Grafana |
| 3100 | Loki |
| 3200 | Tempo |
| 4317 | OTLP gRPC (Collector) |
| 4318 | OTLP HTTP (Collector) |
| 9090 | Prometheus |

All ports bind to `127.0.0.1` only, so only processes on your machine can
conflict. Find and stop the process using the port:

```bash
lsof -iTCP:3000 -sTCP:LISTEN          # macOS / Linux
```

```powershell
Get-NetTCPConnection -LocalPort 3000 -State Listen   # Windows PowerShell
```

Then run `extent stack up` again. Changing a port means editing the generated
Compose file. Re-running `apply` then refuses the edit, so free the port instead.

## Docker is not running (Docker Desktop on Windows and macOS)

`stack up` checks `docker --version`, `docker compose version`, and `docker info`.
If the daemon is down, it fails with `docker daemon unavailable`. On Windows
and macOS, start Docker Desktop and wait until it reports that it is running.
Then check with:

```bash
docker info
```

```powershell
docker info
```

Exit code 4 means a required service such as Docker was unavailable.

## Compose v2 is required

Extent needs the `docker compose` plugin (Compose v2). The old standalone
`docker-compose` command is not enough, and the error is
`docker compose v2 is unavailable`. Podman and nerdctl are not supported.

```bash
docker compose version
```

```powershell
docker compose version
```

Install or update Docker Desktop, or install the Compose v2 plugin on Linux.

## No container and host metrics

The default generated stack does not include cAdvisor or node-exporter. The
`host-metrics-linux` profile that adds them is not reachable from `apply` or
`extent.yaml` in this release. As a result, the dashboard's **Container CPU**,
**Container Memory**, and **Host CPU** panels stay empty. Expected behaviour,
not a fault.

## Loki and Tempo take a while to start

Grafana and Prometheus have health checks. Loki and Tempo do not, so
`extent stack up --wait` polls their `/ready` endpoints until they answer. On a
first start this can take a minute. `stack up` gives up after `--timeout`
(default `2m`). If it times out, check the logs.

Extent names the Compose project `extent-<repo-folder>` (or the value of
`--project-name`), so pass the same project name to `docker compose`. Use the
folder name in lowercase, with characters other than letters, digits, `-`, and
`_` replaced by `-`:

```bash
cd /path/to/repo
docker compose -f docker-compose.observability.yml --env-file .env.observability -p extent-<repo-folder> logs loki tempo
```

```powershell
cd C:\path\to\repo
docker compose -f docker-compose.observability.yml --env-file .env.observability -p extent-<repo-folder> logs loki tempo
```

## No traces show up

Work through this list in order.

1. **Endpoint.** The app must point at the right host:
   - App in Docker on the same Compose network: `OTEL_EXPORTER_OTLP_ENDPOINT=http://otel-collector:4318`
   - App running on your host machine: `OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318`

   `.env.observability` contains the Compose form and a commented host form.
2. **Protocol.** The generated settings use `OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf`,
   which matches port 4318. Port 4317 is gRPC. Do not mix them.
3. **Service name.** `OTEL_SERVICE_NAME` is `your-service-name` in the generated
   file. Set it to your real service name. `smoke`, `verify`, and `report` look up
   the service by this name through `--service`.
4. **Sampling.** Tail sampling keeps every trace with an error, every trace at or
   above the slow threshold (500 ms; 250 ms in `report-heavy`), and every trace
   carrying the `x_request_id` header. It keeps 10% of other traces. A few fast,
   successful requests may not appear. Send a request with the header, or use
   `extent smoke`, which sends correlated traffic.
5. **Collector logs.** Look for export errors:

   ```bash
   docker compose -f docker-compose.observability.yml --env-file .env.observability -p extent-<repo-folder> logs otel-collector
   ```

   ```powershell
   docker compose -f docker-compose.observability.yml --env-file .env.observability -p extent-<repo-folder> logs otel-collector
   ```

6. **Prove the path.** Run `extent smoke --url <app-url> --service <name> --prometheus http://localhost:9090 --tempo http://localhost:3200 --loki http://localhost:3100`.
   Each stage reports whether telemetry reached it.

## Grafana password changed but login fails

Grafana reads `GF_SECURITY_ADMIN_PASSWORD` only when it first creates its
database. The `grafana-data` volume keeps the password from that first start.
Editing `GRAFANA_ADMIN_PASSWORD` in `.env.observability` afterwards does not change it.

To reset, remove the Grafana volume and start again. This also deletes stored
Tempo, Loki, and Prometheus data, because the stack's volumes are removed together.

```bash
cd /path/to/repo
extent stack down .
docker compose -f docker-compose.observability.yml --env-file .env.observability -p extent-<repo-folder> down -v
extent stack up .
```

```powershell
cd C:\path\to\repo
extent stack down .
docker compose -f docker-compose.observability.yml --env-file .env.observability -p extent-<repo-folder> down -v
extent stack up .
```

`extent stack down` alone keeps the volumes, which is why the password does not change.
`docker volume ls` lists the project's volumes (`extent-<repo-folder>_grafana-data`
and the others). If you used `--project-name`, use that name in place of `extent-<repo-folder>`.

## Undo refuses: a generated file was edited

`extent instrument --undo` removes the files Extent recorded in
`.extent/state.json`. If one of them changed since Extent wrote it, undo refuses
with exit code 1:

```text
error: filesystem conflict: created target <path> was modified or removed
```

`--undo` does not need `--apply`. It runs directly. Options:

- **Revert your edit** to the file, then run undo again. With git:
  `git restore -- <path>` (this discards your uncommitted changes to that file).
- **Keep your edit.** Move the change out of the Extent-owned file by hand first,
  then undo.

Do not edit `.extent/state.json` to get past the conflict.

## Re-running `apply` needs `--force`

`extent apply` skips files whose content is already what it would write. Re-running it
with no changes exits 0. It refuses to overwrite a file whose content would change:

```text
error: refusing to overwrite existing file: otel-collector.yml
```

This happens when you change the profile, for example with
`extent apply --profile low-resource`. Re-run with `--force`:

```bash
extent apply --profile low-resource --force /path/to/repo
```

`--force` only replaces files that still match what Extent last wrote. If you
edited a generated file, `--force` also refuses:

```text
error: filesystem conflict: owned target prometheus.yml was modified
```

Revert your edit to that file, then run `apply --force` again.

After any change to the generated files, restart the stack with
`extent stack down` and `extent stack up` so the new settings take effect.
