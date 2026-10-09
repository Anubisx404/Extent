package instrumenter

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Anubisx404/Extent/internal/fileops"
)

var pythonDependencyPins = pins.Python.Dependencies

var pythonDependencies = []string{
	"opentelemetry-api",
	"opentelemetry-sdk",
	"opentelemetry-exporter-otlp",
	"opentelemetry-instrumentation",
	"opentelemetry-instrumentation-logging",
	"opentelemetry-instrumentation-requests",
}

// pythonFrameworkInstrumentation maps a web framework to its OpenTelemetry
// instrumentation package. Only frameworks the project uses get installed.
var pythonFrameworkInstrumentation = map[string][]string{
	"fastapi": {"opentelemetry-instrumentation-fastapi"},
	"flask":   {"opentelemetry-instrumentation-flask"},
}

var pythonDatabaseInstrumentation = map[string][]string{
	"sqlalchemy":  {"opentelemetry-instrumentation-sqlalchemy"},
	"psycopg2":    {"opentelemetry-instrumentation-psycopg2"},
	"psycopg":     {"opentelemetry-instrumentation-psycopg"},
	"asyncpg":     {"opentelemetry-instrumentation-asyncpg"},
	"pymongo":     {"opentelemetry-instrumentation-pymongo"},
	"redis":       {"opentelemetry-instrumentation-redis"},
	"mysqlclient": {"opentelemetry-instrumentation-mysql"},
	"pymysql":     {"opentelemetry-instrumentation-pymysql"},
}

func pythonPlan(root string, opts Options) ([]fileops.Step, error) {
	steps := []fileops.Step{}
	add := func(rel string, data []byte) error {
		old, e := os.ReadFile(filepath.Join(root, rel))
		if e == nil && bytes.Equal(old, data) {
			return nil
		}
		a := fileops.Create
		if e == nil {
			a = fileops.Update
		}
		steps = append(steps, fileops.Step{Path: rel, Action: a, Data: data, Mode: 0644})
		return nil
	}
	if e := add("extent_instrumentation.py", []byte(pythonBootstrap)); e != nil {
		return nil, e
	}
	req := filepath.Join(root, "requirements.txt")
	old, e := os.ReadFile(req)
	if e == nil {
		if err := validatePythonPins(old); err != nil {
			return nil, err
		}
		packages := append(append([]string{}, pythonDependencies...), pythonFrameworkInstrumentationFor(root)...)
		lines := mergeRequirements(string(old), append(packages, pythonDBInstrumentationFor(req)...))
		if lines != string(old) {
			steps = append(steps, fileops.Step{Path: "requirements.txt", Action: fileops.Update, Data: []byte(lines), Mode: 0644})
		}
	}
	ep, e := selectEntrypoint(root, opts, []string{"main.py", "app.py"})
	if e != nil {
		return nil, e
	}
	if ep != "" {
		old, _ := os.ReadFile(filepath.Join(root, ep))
		if !strings.Contains(string(old), marker) {
			text := string(old)
			lines := strings.Split(text, "\n")
			idx := 0
			for idx < len(lines) && (strings.HasPrefix(lines[idx], "#!") || strings.HasPrefix(lines[idx], "# -*-") || strings.HasPrefix(strings.TrimSpace(lines[idx]), "from __future__")) {
				idx++
			}
			lines = append(lines[:idx], append([]string{"import extent_instrumentation  # extent:otel"}, lines[idx:]...)...)
			old = []byte(strings.Join(lines, "\n"))
			steps = append(steps, fileops.Step{Path: ep, Action: fileops.Update, Data: old, Mode: 0644})
		}
	}
	return steps, nil
}

func validatePythonPins(data []byte) error {
	for _, line := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
		trimmed := strings.TrimSpace(strings.SplitN(line, "#", 2)[0])
		if trimmed == "" {
			continue
		}
		withoutMarker := strings.TrimSpace(strings.SplitN(trimmed, ";", 2)[0])
		lower := strings.ToLower(withoutMarker)
		for name, want := range pythonDependencyPins {
			if !strings.HasPrefix(lower, name) {
				continue
			}
			rest := strings.TrimSpace(withoutMarker[len(name):])
			if rest == "" {
				return fmt.Errorf("unpinned OpenTelemetry requirement %s; expected %s==%s", line, name, want)
			}
			if !strings.Contains("=<>!~[", rest[:1]) {
				continue
			}
			if rest != "=="+want {
				return fmt.Errorf("incompatible OpenTelemetry requirement %s; expected %s==%s", line, name, want)
			}
		}
	}
	return nil
}
func mergeRequirements(text string, packages []string) string {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	seen := map[string]bool{}
	for i, l := range lines {
		s := strings.TrimSpace(l)
		if s == "" || strings.HasPrefix(s, "#") {
			continue
		}
		name := strings.ToLower(strings.FieldsFunc(s, func(r rune) bool { return r == '=' || r == '<' || r == '>' || r == '!' || r == '~' || r == ' ' })[0])
		seen[name] = true
		if want, ok := pythonDependencyPins[name]; ok {
			lines[i] = name + "==" + want
		}
	}
	for _, p := range packages {
		if !seen[strings.ToLower(p)] {
			lines = append(lines, p+"=="+pythonDependencyPins[strings.ToLower(p)])
			seen[strings.ToLower(p)] = true
		}
	}
	return strings.TrimRight(strings.Join(lines, "\n"), "\n") + "\n"
}

// pythonFrameworkInstrumentationFor returns the instrumentation packages for the
// web frameworks detected in requirements.txt or the Python entrypoints.
func pythonFrameworkInstrumentationFor(root string) []string {
	var text strings.Builder
	for _, rel := range []string{"requirements.txt", "main.py", "app.py"} {
		if data, err := os.ReadFile(filepath.Join(root, rel)); err == nil {
			text.WriteString(strings.ToLower(string(data)) + "\n")
		}
	}
	var packages []string
	for framework, instrumentation := range pythonFrameworkInstrumentation {
		if strings.Contains(text.String(), framework) {
			packages = append(packages, instrumentation...)
		}
	}
	sort.Strings(packages)
	return packages
}

func pythonDBInstrumentationFor(path string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	text := strings.ToLower(string(data))
	var packages []string
	seen := map[string]bool{}
	for appDep, instrumentationDeps := range pythonDatabaseInstrumentation {
		if !strings.Contains(text, strings.ToLower(appDep)) {
			continue
		}
		for _, dep := range instrumentationDeps {
			if !seen[dep] {
				packages = append(packages, dep)
				seen[dep] = true
			}
		}
	}
	sort.Strings(packages)
	return packages
}

const pythonBootstrap = `"""Generated by Extent. Import before application code."""

import atexit
import logging
import os

os.environ.setdefault("OTEL_INSTRUMENTATION_HTTP_CAPTURE_HEADERS_SERVER_REQUEST", "X-Request-ID")

from opentelemetry import trace
from opentelemetry._logs import set_logger_provider
from opentelemetry.instrumentation.logging import LoggingInstrumentor
from opentelemetry.instrumentation.requests import RequestsInstrumentor
from opentelemetry.metrics import set_meter_provider
from opentelemetry.exporter.otlp.proto.http.metric_exporter import OTLPMetricExporter
from opentelemetry.exporter.otlp.proto.http.trace_exporter import OTLPSpanExporter
from opentelemetry.exporter.otlp.proto.http._log_exporter import OTLPLogExporter
from opentelemetry.sdk._logs import LoggerProvider, LoggingHandler
from opentelemetry.sdk._logs.export import BatchLogRecordProcessor
from opentelemetry.sdk.metrics import MeterProvider
from opentelemetry.sdk.metrics.export import PeriodicExportingMetricReader
from opentelemetry.sdk.resources import Resource
from opentelemetry.sdk.trace import TracerProvider
from opentelemetry.sdk.trace.export import BatchSpanProcessor

try:
    from opentelemetry.instrumentation.fastapi import FastAPIInstrumentor
except Exception:
    FastAPIInstrumentor = None

try:
    from opentelemetry.instrumentation.flask import FlaskInstrumentor
except Exception:
    FlaskInstrumentor = None

try:
    from opentelemetry.instrumentation.sqlalchemy import SQLAlchemyInstrumentor
except Exception:
    SQLAlchemyInstrumentor = None

try:
    from opentelemetry.instrumentation.psycopg2 import Psycopg2Instrumentor
except Exception:
    Psycopg2Instrumentor = None

resource = Resource.create({
    "service.name": os.getenv("OTEL_SERVICE_NAME", "service"),
    "service.namespace": os.getenv("OTEL_SERVICE_NAMESPACE", "default"),
    "deployment.environment.name": os.getenv("OTEL_DEPLOYMENT_ENVIRONMENT", "development"),
})
trace_provider = TracerProvider(resource=resource)
trace_provider.add_span_processor(BatchSpanProcessor(OTLPSpanExporter()))
trace.set_tracer_provider(trace_provider)

try:
    metric_export_interval = max(1000, int(os.getenv("OTEL_METRIC_EXPORT_INTERVAL", "5000")))
except ValueError:
    metric_export_interval = 5000
metric_reader = PeriodicExportingMetricReader(OTLPMetricExporter(), export_interval_millis=metric_export_interval)
meter_provider = MeterProvider(resource=resource, metric_readers=[metric_reader])
set_meter_provider(meter_provider)

logger_provider = LoggerProvider(resource=resource)
logger_provider.add_log_record_processor(BatchLogRecordProcessor(OTLPLogExporter()))
set_logger_provider(logger_provider)
LoggingInstrumentor().instrument(set_logging_format=True)
logging.getLogger().addHandler(LoggingHandler(level=logging.NOTSET, logger_provider=logger_provider))
RequestsInstrumentor().instrument()
if FastAPIInstrumentor is not None:
    FastAPIInstrumentor().instrument()
if FlaskInstrumentor is not None:
    FlaskInstrumentor().instrument()
if SQLAlchemyInstrumentor is not None:
    SQLAlchemyInstrumentor().instrument(enable_commenter=True, commenter_options={})
if Psycopg2Instrumentor is not None:
    Psycopg2Instrumentor().instrument(enable_commenter=True)

atexit.register(logger_provider.shutdown)
atexit.register(meter_provider.shutdown)
atexit.register(trace_provider.shutdown)
`
