# Support matrix

> Generated from `recipes/*/*.yaml` support metadata and the scanner's
> detection-only list (`internal/scanner/recipe_parity_test.go`), then
> checked by hand. For now this is maintained by hand. Update it whenever a
> recipe's `support` block or the scanner's framework list changes.

## Status

- **stable**: part of the supported contract. Covered by tests and the release.
- **experimental**: works, but needs `--experimental` and may change. Not in the stable contract.
- **detection-only**: Extent reports the runtime or framework, but does not inject anything in this mode.

Modes:

- **zero-code**: `extent instrument --mode zero-code` writes an env file (`extent.zero-code.env`) with the run settings for the auto-instrumentation agent. It does not change source.
- **bootstrap**: `extent instrument --mode bootstrap --apply` injects OpenTelemetry setup code and dependency manifest changes.
- **deep**: `extent instrument --mode deep --experimental --apply` generates reviewable codemods in `extent.codemods/`.

## Matrix

| Runtime | Framework (recipe) | zero-code | bootstrap | deep | Notes |
| --- | --- | --- | --- | --- | --- |
| Node.js | Express (`express`) | stable | stable | experimental | CommonJS and ESM. See Node notes. |
| Node.js | NestJS (`nestjs`) | stable | stable | experimental | Same Node rules as Express. |
| Node.js | Next.js (`nextjs`) | stable | stable | experimental | |
| Node.js | Fastify (`fastify`) | stable | stable | experimental | |
| Python | Flask (`flask`) | stable | stable | experimental | Import added to `app.py` or `main.py`. |
| Python | FastAPI (`fastapi`) | stable | stable | experimental | Install dependencies into a virtualenv first. |
| Python | Django (`django`) | stable | stable | experimental | |
| Go | net/http (`nethttp`) | stable | stable | detection-only | See Go notes. |
| Go | Gin (`gin`) | stable | stable | detection-only | See Go notes. |
| Go | Fiber (`fiber`) | stable | stable | detection-only | See Go notes. |
| Go | chi | detection-only | detection-only | detection-only | Scanner reports it. No recipe. |
| .NET | ASP.NET Core (`aspnetcore`) | detection-only | stable | experimental | Zero-code writes a guidance comment only. |
| .NET | Entity Framework Core (`efcore`) | detection-only | stable | experimental | Database add-on for ASP.NET Core. Not a separate framework row. |
| .NET | HotChocolate (`hotchocolate`) | detection-only | detection-only | experimental | Recipe exists. The scanner does not emit it, so Extent does not detect it. |
| Java | Spring Boot (`springboot`) | detection-only | detection-only | detection-only | Java-only projects get "no supported runtime detected" in every mode. |
| Svelte | Svelte (`svelte`) | detection-only | detection-only | detection-only | Scanner reports it. No recipe. |
| Svelte | SvelteKit (`sveltekit`) | detection-only | detection-only | detection-only | Scanner reports it. No recipe. |

Deep mode in the Spring Boot row: the recipe lists `experimental`, but deep
mode only generates codemods when the repo also has a supported runtime (Node,
Python, Go, or .NET). A Java-only project gets no codemods, so the row is
detection-only in practice.

## Runtime notes

### Node.js

- **CommonJS** projects get a `require` of the generated bootstrap at the top of the entrypoint.
- **ESM** projects (`"type": "module"`): Extent rewrites the `start` script to
  `node --import ./extent.instrumentation.mjs ...`. Run the project with
  `npm start`, not by calling the entrypoint directly.
- Traces, metrics, logs, and request correlation are set up for HTTP. Supported
  database clients are auto-instrumented.

### Python

- Flask and FastAPI get a generated `extent_instrumentation` import at the top of the entrypoint.
- Install dependencies into a virtualenv before running `instrument --apply`.

### Go

- Bootstrap sets up the trace and metric providers and flushes them on SIGINT and SIGTERM.
- HTTP server spans need OpenTelemetry `otelhttp` wrapping or framework
  instrumentation in your application. The bootstrap does not wrap your handlers.
- `log.Fatal` exits without the shutdown flush. Return errors from `main` if you want the last spans exported.

### .NET

- Bootstrap adds `ExtentObservabilityExtensions.cs`, OpenTelemetry package references, and a registration in `Program.cs` or `Startup.cs`.
- Zero-code writes only a comment pointing to OpenTelemetry auto-instrumentation.

### Deep mode (experimental, all runtimes)

- Requires `--experimental`.
- Writes `extent.codemods/` with reviewable patchers for HTTP routes, DB calls, queues, outbound HTTP, business functions, and log statements.
- With `--apply`, Extent runs the detected language codemod commands. Missing toolchains are reported as command failures.
- Generated TypeScript, LibCST, Roslyn, OpenRewrite, and JavaParser codemods are not part of the stable contract.

## Not yet implemented

- Stable deep instrumentation for any language.
- Bootstrap or zero-code for Java (Spring Boot).
- Bootstrap recipes for Svelte and SvelteKit (detection only).
- Recipe for chi (detection only).
- Package-manager publishing or self-update.
- A Svelte local UI and a Tauri desktop package.
- Browser-level Grafana UI automation. `verify --grafana` checks the generated
  config, the backing APIs, and live datasource JSON only.
