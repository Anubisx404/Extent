package instrumenter

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/Anubisx404/Extent/internal/fileops"
	"golang.org/x/mod/semver"
)

var nodeDependencyPins = pins.Node.Dependencies
var nodeDatabasePins = pins.Node.Database

var nodeDependencies = []string{
	"@opentelemetry/api",
	"@opentelemetry/sdk-node",
	"@opentelemetry/auto-instrumentations-node",
	"@opentelemetry/exporter-trace-otlp-http",
	"@opentelemetry/exporter-metrics-otlp-http",
	"@opentelemetry/exporter-logs-otlp-http",
	"@opentelemetry/sdk-logs",
	"@opentelemetry/sdk-metrics",
	"@opentelemetry/resources",
}

// nodeESMDependencyPins and nodeESMDependencies are added only for ESM targets.
// The ESM loader hook (hook.mjs) ships with @opentelemetry/instrumentation.
var nodeESMDependencyPins = pins.Node.ESM

var nodeESMDependencies = []string{"@opentelemetry/instrumentation"}

// nodeEntrypoints are the candidate Node entrypoints, in lookup order.
var nodeEntrypoints = []string{"server.js", "index.js", "src/server.js", "src/index.js", "server.mjs", "index.mjs", "src/server.mjs", "src/index.mjs"}

// nodeESMLoaderFlag loads the generated ESM bootstrap before the application.
const nodeESMLoaderFlag = "--import ./extent.instrumentation.mjs"

var nodeDatabaseInstrumentation = map[string][]string{
	"pg":       {"@opentelemetry/instrumentation-pg"},
	"mysql":    {"@opentelemetry/instrumentation-mysql"},
	"mysql2":   {"@opentelemetry/instrumentation-mysql2"},
	"mongodb":  {"@opentelemetry/instrumentation-mongodb"},
	"mongoose": {"@opentelemetry/instrumentation-mongodb"},
	"redis":    {"@opentelemetry/instrumentation-redis"},
	"ioredis":  {"@opentelemetry/instrumentation-ioredis"},
}

func nodePlan(root string, opts Options) ([]fileops.Step, error) {
	path := filepath.Join(root, "package.json")
	data, e := os.ReadFile(path)
	if e != nil {
		return nil, e
	}
	pkg, e := parseJSONObject(data)
	if e != nil {
		return nil, e
	}
	ep, e := selectEntrypoint(root, opts, nodeEntrypoints)
	if e != nil {
		return nil, e
	}
	esm := isESMTarget(pkg, ep)
	if err := validateNodePins(pkg, esm); err != nil {
		return nil, err
	}
	pkgChanged := addNodeDependenciesPinned(pkg, esm)

	var source []byte
	sourceInstrumented := false
	if ep != "" {
		source, _ = os.ReadFile(filepath.Join(root, ep))
		sourceInstrumented = strings.Contains(string(source), marker)
	}
	// ESM entrypoints are loaded with `node --import`, which registers the OTel
	// loader hook before application modules are imported. A source import would
	// be hoisted and run too late for framework instrumentation.
	loadedByFlag := false
	if esm && ep != "" && !sourceInstrumented {
		rewritten, handled := rewriteNodeStartScript(pkg, ep)
		pkgChanged = pkgChanged || rewritten
		loadedByFlag = handled
	}

	steps := []fileops.Step{}
	if pkgChanged {
		out, e := marshalPackageJSON(pkg, data)
		if e != nil {
			return nil, e
		}
		steps = append(steps, fileops.Step{Path: "package.json", Action: fileops.Update, Data: out, Mode: 0644})
	}
	name, boot, inj := "extent.instrumentation.js", nodeCJSBootstrap, `require("./extent.instrumentation"); // extent:otel`
	if esm {
		name, boot, inj = "extent.instrumentation.mjs", nodeESMBootstrap, `import "./extent.instrumentation.mjs"; // extent:otel`
	}
	if old, e := os.ReadFile(filepath.Join(root, name)); e != nil || string(old) != boot {
		a := fileops.Create
		if e == nil {
			a = fileops.Update
		}
		steps = append(steps, fileops.Step{Path: name, Action: a, Data: []byte(boot), Mode: 0644})
	}
	if ep != "" && !sourceInstrumented && !loadedByFlag {
		steps = append(steps, fileops.Step{Path: ep, Action: fileops.Update, Data: injectNodePreamble(source, inj), Mode: 0644})
	}
	return steps, nil
}

// isESMTarget reports whether the project should get the ESM bootstrap: either
// package.json declares "type": "module" or the entrypoint is a .mjs file.
func isESMTarget(pkg *jsonObject, ep string) bool {
	typ, _ := pkg.get("type")
	if s, ok := typ.(string); ok && s == "module" {
		return true
	}
	return strings.HasSuffix(ep, ".mjs")
}

// rewriteNodeStartScript changes `node <ep> ...` in scripts.start to load the
// ESM bootstrap with --import. It reports whether it changed package.json and
// whether the start script now loads the bootstrap (already or newly).
func rewriteNodeStartScript(pkg *jsonObject, ep string) (changed bool, handled bool) {
	scripts := pkg.object("scripts")
	if scripts == nil {
		return false, false
	}
	value, _ := scripts.get("start")
	start, ok := value.(string)
	if !ok {
		return false, false
	}
	if strings.Contains(start, nodeESMLoaderFlag) {
		return false, true
	}
	fields := strings.Fields(start)
	if len(fields) < 2 || fields[0] != "node" || strings.HasPrefix(fields[1], "-") {
		return false, false
	}
	if filepath.ToSlash(filepath.Clean(fields[1])) != ep {
		return false, false
	}
	idx := strings.Index(start, "node")
	updated := start[:idx] + "node " + nodeESMLoaderFlag + start[idx+len("node"):]
	scripts.set("start", updated)
	return true, true
}

func injectNodePreamble(source []byte, injection string) []byte {
	text := string(source)
	if strings.HasPrefix(text, "#!") {
		if newline := strings.IndexByte(text, '\n'); newline >= 0 {
			return []byte(text[:newline+1] + injection + "\n" + text[newline+1:])
		}
		return []byte(text + "\n" + injection + "\n")
	}
	return []byte(injection + "\n" + text)
}

func addNodeDependenciesPinned(pkg *jsonObject, esm bool) bool {
	changed := false
	deps := pkg.object("dependencies")
	if deps == nil {
		deps = newJSONObject()
		pkg.set("dependencies", deps)
	}
	devDeps := pkg.object("devDependencies")
	required := nodeDependencies
	if esm {
		required = append(append([]string{}, nodeDependencies...), nodeESMDependencies...)
	}
	for _, d := range required {
		if _, exists := dependencyConstraint(deps, devDeps, d); !exists {
			deps.set(d, nodePinFor(d))
			changed = true
		}
	}
	apps := make([]string, 0, len(nodeDatabaseInstrumentation))
	for app := range nodeDatabaseInstrumentation {
		apps = append(apps, app)
	}
	sort.Strings(apps)
	for _, app := range apps {
		if _, ok := dependencyConstraint(deps, devDeps, app); ok {
			for _, d := range nodeDatabaseInstrumentation[app] {
				if _, ok := dependencyConstraint(deps, devDeps, d); !ok {
					deps.set(d, nodeDatabasePins[d])
					changed = true
				}
			}
		}
	}
	return changed
}

// nodePinFor returns the pinned version for a dependency Extent adds.
func nodePinFor(name string) string {
	if version, ok := nodeDependencyPins[name]; ok {
		return version
	}
	return nodeESMDependencyPins[name]
}

func validateNodePins(pkg *jsonObject, esm bool) error {
	deps := pkg.object("dependencies")
	devDeps := pkg.object("devDependencies")
	pins := make(map[string]string, len(nodeDependencyPins)+len(nodeDatabasePins)+len(nodeESMDependencyPins))
	for name, version := range nodeDependencyPins {
		pins[name] = version
	}
	for name, version := range nodeDatabasePins {
		pins[name] = version
	}
	if esm {
		for name, version := range nodeESMDependencyPins {
			pins[name] = version
		}
	}
	for name, expected := range pins {
		if constraint, ok := dependencyConstraint(deps, devDeps, name); ok && !nodeConstraintAllows(constraint, expected) {
			return fmt.Errorf("incompatible OpenTelemetry dependency %s=%s; expected a constraint containing %s", name, constraint, expected)
		}
	}
	return nil
}

func dependencyConstraint(deps, devDeps *jsonObject, name string) (string, bool) {
	for _, collection := range []*jsonObject{deps, devDeps} {
		if value, ok := collection.get(name); ok {
			constraint, valid := value.(string)
			return constraint, valid
		}
	}
	return "", false
}

func nodeConstraintAllows(constraint, expected string) bool {
	constraint = strings.TrimSpace(constraint)
	want := "v" + strings.TrimPrefix(expected, "v")
	if !semver.IsValid(want) {
		return false
	}
	if constraint == expected || constraint == "v"+expected || constraint == "="+expected {
		return true
	}
	if strings.HasPrefix(constraint, "^") || strings.HasPrefix(constraint, "~") {
		kind := constraint[0]
		lower := "v" + strings.TrimPrefix(strings.TrimSpace(constraint[1:]), "v")
		if !semver.IsValid(lower) || semver.Compare(want, lower) < 0 {
			return false
		}
		wantParts := semverParts(want)
		lowerParts := semverParts(lower)
		if kind == '~' {
			return wantParts[0] == lowerParts[0] && wantParts[1] == lowerParts[1]
		}
		if lowerParts[0] > 0 {
			return wantParts[0] == lowerParts[0]
		}
		if lowerParts[1] > 0 {
			return wantParts[0] == 0 && wantParts[1] == lowerParts[1]
		}
		return wantParts == lowerParts
	}
	return false
}

func semverParts(version string) [3]int {
	core := strings.TrimPrefix(version, "v")
	if cutoff := strings.IndexAny(core, "-+"); cutoff >= 0 {
		core = core[:cutoff]
	}
	fields := strings.Split(core, ".")
	parts := [3]int{}
	for i := 0; i < len(fields) && i < len(parts); i++ {
		parts[i], _ = strconv.Atoi(fields[i])
	}
	return parts
}

const nodeCJSBootstrap = `// Generated by Extent. Keep this file imported before application code.
const { NodeSDK } = require("@opentelemetry/sdk-node");
const { BatchLogRecordProcessor } = require("@opentelemetry/sdk-logs");
const { logs } = require("@opentelemetry/api-logs");
const { PeriodicExportingMetricReader } = require("@opentelemetry/sdk-metrics");
const { resourceFromAttributes } = require("@opentelemetry/resources");
const { getNodeAutoInstrumentations } = require("@opentelemetry/auto-instrumentations-node");
const { OTLPTraceExporter } = require("@opentelemetry/exporter-trace-otlp-http");
const { OTLPMetricExporter } = require("@opentelemetry/exporter-metrics-otlp-http");
const { OTLPLogExporter } = require("@opentelemetry/exporter-logs-otlp-http");
const { IncomingMessage } = require("node:http");

const resource = resourceFromAttributes({
  "service.name": process.env.OTEL_SERVICE_NAME || "service",
  "service.namespace": process.env.OTEL_SERVICE_NAMESPACE || "default",
  "deployment.environment.name": process.env.OTEL_DEPLOYMENT_ENVIRONMENT || "development",
});
const configuredMetricInterval = Number(process.env.OTEL_METRIC_EXPORT_INTERVAL || 5000);
const metricExportIntervalMillis = Number.isFinite(configuredMetricInterval) && configuredMetricInterval >= 1000
  ? configuredMetricInterval
  : 5000;

const sdk = new NodeSDK({
  resource,
  traceExporter: new OTLPTraceExporter(),
  metricReaders: [new PeriodicExportingMetricReader({ exporter: new OTLPMetricExporter(), exportIntervalMillis: metricExportIntervalMillis })],
  logRecordProcessors: [new BatchLogRecordProcessor({ exporter: new OTLPLogExporter() })],
  instrumentations: [getNodeAutoInstrumentations({
    "@opentelemetry/instrumentation-pg": {
      enhancedDatabaseReporting: true,
      requireParentSpan: true,
    },
    "@opentelemetry/instrumentation-mysql": { enhancedDatabaseReporting: true },
    "@opentelemetry/instrumentation-mongodb": { enhancedDatabaseReporting: true },
    "@opentelemetry/instrumentation-redis": { requireParentSpan: true },
    "@opentelemetry/instrumentation-http": {
      requestHook: (_span, request) => {
        // Log incoming server requests only; outgoing client requests are skipped.
        if (!(request instanceof IncomingMessage)) return;
        logs.getLogger("extent.http").emit({
          body: "HTTP request",
          attributes: { "http.request.header.x_request_id": request.headers?.["x-request-id"] || "" },
        });
      },
      headersToSpanAttributes: {
        client: { requestHeaders: ["x-request-id"], responseHeaders: ["x-request-id"] },
        server: { requestHeaders: ["x-request-id"], responseHeaders: ["x-request-id"] },
      },
    },
  })],
});

let stopping = false;
async function shutdown(signal) {
  if (stopping) return;
  stopping = true;
  try {
    await sdk.shutdown();
  } catch (error) {
    console.error("extent: OpenTelemetry shutdown failed", error);
    process.exitCode = 1;
  }
  if (signal) process.exit();
}

process.once("SIGTERM", () => void shutdown("SIGTERM"));
process.once("SIGINT", () => void shutdown("SIGINT"));
sdk.start();

module.exports = { sdk, shutdown };
`

const nodeESMBootstrap = `// Generated by Extent. Keep this file imported before application code.
// Run the application with: node --import ./extent.instrumentation.mjs <entrypoint>
import { register } from "node:module";
register("@opentelemetry/instrumentation/hook.mjs", import.meta.url);

import { NodeSDK } from "@opentelemetry/sdk-node";
import { IncomingMessage } from "node:http";
import { BatchLogRecordProcessor } from "@opentelemetry/sdk-logs";
import { logs } from "@opentelemetry/api-logs";
import { PeriodicExportingMetricReader } from "@opentelemetry/sdk-metrics";
import { resourceFromAttributes } from "@opentelemetry/resources";
import { getNodeAutoInstrumentations } from "@opentelemetry/auto-instrumentations-node";
import { OTLPTraceExporter } from "@opentelemetry/exporter-trace-otlp-http";
import { OTLPMetricExporter } from "@opentelemetry/exporter-metrics-otlp-http";
import { OTLPLogExporter } from "@opentelemetry/exporter-logs-otlp-http";

const resource = resourceFromAttributes({
  "service.name": process.env.OTEL_SERVICE_NAME || "service",
  "service.namespace": process.env.OTEL_SERVICE_NAMESPACE || "default",
  "deployment.environment.name": process.env.OTEL_DEPLOYMENT_ENVIRONMENT || "development",
});
const configuredMetricInterval = Number(process.env.OTEL_METRIC_EXPORT_INTERVAL || 5000);
const metricExportIntervalMillis = Number.isFinite(configuredMetricInterval) && configuredMetricInterval >= 1000
  ? configuredMetricInterval
  : 5000;

const sdk = new NodeSDK({
  resource,
  traceExporter: new OTLPTraceExporter(),
  metricReaders: [new PeriodicExportingMetricReader({ exporter: new OTLPMetricExporter(), exportIntervalMillis: metricExportIntervalMillis })],
  logRecordProcessors: [new BatchLogRecordProcessor({ exporter: new OTLPLogExporter() })],
  instrumentations: [getNodeAutoInstrumentations({
    "@opentelemetry/instrumentation-pg": {
      enhancedDatabaseReporting: true,
      requireParentSpan: true,
    },
    "@opentelemetry/instrumentation-mysql": { enhancedDatabaseReporting: true },
    "@opentelemetry/instrumentation-mongodb": { enhancedDatabaseReporting: true },
    "@opentelemetry/instrumentation-redis": { requireParentSpan: true },
    "@opentelemetry/instrumentation-http": {
      requestHook: (_span, request) => {
        // Log incoming server requests only; outgoing client requests are skipped.
        if (!(request instanceof IncomingMessage)) return;
        logs.getLogger("extent.http").emit({
          body: "HTTP request",
          attributes: { "http.request.header.x_request_id": request.headers?.["x-request-id"] || "" },
        });
      },
      headersToSpanAttributes: {
        client: { requestHeaders: ["x-request-id"], responseHeaders: ["x-request-id"] },
        server: { requestHeaders: ["x-request-id"], responseHeaders: ["x-request-id"] },
      },
    },
  })],
});

let stopping = false;
async function shutdown(signal) {
  if (stopping) return;
  stopping = true;
  try {
    await sdk.shutdown();
  } catch (error) {
    console.error("extent: OpenTelemetry shutdown failed", error);
    process.exitCode = 1;
  }
  if (signal) process.exit();
}

process.once("SIGTERM", () => void shutdown("SIGTERM"));
process.once("SIGINT", () => void shutdown("SIGINT"));
sdk.start();

export { sdk, shutdown };
`
