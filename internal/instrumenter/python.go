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

var pythonFrameworkInstrumentation = map[string][]string{
	"fastapi": {"opentelemetry-instrumentation-fastapi"},
	"flask":   {"opentelemetry-instrumentation-flask"},
	"django":  {"opentelemetry-instrumentation-django"},
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
	pyproject := filepath.Join(root, "pyproject.toml")
	packages := append(append([]string{}, pythonDependencies...), pythonFrameworkInstrumentationFor(root)...)
	packages = append(packages, pythonDBInstrumentationFor(root)...)
	if old, e := os.ReadFile(req); e == nil {
		if err := validatePythonPins(old); err != nil {
			return nil, err
		}
		lines := mergeRequirements(string(old), packages)
		if lines != string(old) {
			steps = append(steps, fileops.Step{Path: "requirements.txt", Action: fileops.Update, Data: []byte(lines), Mode: 0644})
		}
	} else if old, e := os.ReadFile(pyproject); e == nil {
		updated, err := mergePyprojectDependencies(string(old), packages)
		if err != nil {
			return nil, err
		}
		if updated != string(old) {
			steps = append(steps, fileops.Step{Path: "pyproject.toml", Action: fileops.Update, Data: []byte(updated), Mode: 0644})
		}
	}
	ep, e := selectPythonEntrypoint(root, opts)
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

func selectPythonEntrypoint(root string, opts Options) (string, error) {
	if opts.Entrypoint != "" {
		if filepath.IsAbs(opts.Entrypoint) || filepath.VolumeName(opts.Entrypoint) != "" {
			return "", fmt.Errorf("invalid entrypoint %q", opts.Entrypoint)
		}
		cleaned := filepath.ToSlash(filepath.Clean(opts.Entrypoint))
		if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") || strings.HasPrefix(cleaned, "/") || !strings.HasSuffix(cleaned, ".py") {
			return "", fmt.Errorf("invalid entrypoint %q", opts.Entrypoint)
		}
		if exists(filepath.Join(root, filepath.FromSlash(cleaned))) {
			return cleaned, nil
		}
		return "", fmt.Errorf("invalid entrypoint %q", opts.Entrypoint)
	}
	ep, err := selectEntrypoint(root, opts, []string{"main.py", "app.py"})
	if err != nil || ep != "" {
		return ep, err
	}
	if exists(filepath.Join(root, "manage.py")) {
		return "manage.py", nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return "", nil
	}
	var found []string
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") || entry.Name() == "venv" {
			continue
		}
		for _, sub := range []string{"wsgi.py", "asgi.py"} {
			if exists(filepath.Join(root, entry.Name(), sub)) {
				found = append(found, entry.Name()+"/"+sub)
			}
		}
	}
	sort.Strings(found)
	if len(found) > 1 {
		return "", fmt.Errorf("ambiguous entrypoints: %s", strings.Join(found, ", "))
	}
	if len(found) == 1 {
		return found[0], nil
	}
	return "", nil
}

func readPythonInspectionText(root string) string {
	var text strings.Builder
	for _, rel := range []string{"requirements.txt", "pyproject.toml", "main.py", "app.py", "manage.py"} {
		if data, err := os.ReadFile(filepath.Join(root, rel)); err == nil {
			text.WriteString(strings.ToLower(string(data)) + "\n")
		}
	}
	return text.String()
}

func pythonFrameworkInstrumentationFor(root string) []string {
	text := readPythonInspectionText(root)
	var packages []string
	for framework, instrumentation := range pythonFrameworkInstrumentation {
		if strings.Contains(text, framework) {
			packages = append(packages, instrumentation...)
		}
	}
	sort.Strings(packages)
	return packages
}

func pythonDBInstrumentationFor(root string) []string {
	text := readPythonInspectionText(root)
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

func mergePyprojectDependencies(text string, packages []string) (string, error) {
	normalized := strings.ReplaceAll(text, "\r\n", "\n")
	lines := strings.Split(normalized, "\n")

	projStart, poetryStart := -1, -1
	for i, line := range lines {
		hdr, ok := isTOMLTableHeader(line)
		if !ok {
			continue
		}
		if hdr == "project" && projStart == -1 {
			projStart = i
		} else if hdr == "tool.poetry.dependencies" && poetryStart == -1 {
			poetryStart = i
		}
	}

	if projStart >= 0 {
		projEnd := findTOMLSectionEnd(lines, projStart)
		depStart := findTOMLKeyLine(lines, projStart+1, projEnd, "dependencies")
		if depStart == -1 {
			insertAt := trimTrailingBlankLineIndex(lines, projStart+1, projEnd)
			seen := map[string]bool{}
			block := []string{"dependencies = ["}
			for _, p := range packages {
				lower := strings.ToLower(p)
				if seen[lower] {
					continue
				}
				seen[lower] = true
				block = append(block, fmt.Sprintf(`  "%s==%s",`, p, pythonDependencyPins[lower]))
			}
			block = append(block, "]")
			updated := append(append([]string{}, lines[:insertAt]...), append(block, lines[insertAt:]...)...)
			return strings.TrimRight(strings.Join(updated, "\n"), "\n") + "\n", nil
		}
		depEnd, err := findTOMLArrayEnd(lines, depStart, projEnd)
		if err != nil {
			return "", err
		}
		seen := map[string]bool{}
		existingSpecs := extractTOMLArrayStrings(lines, depStart, depEnd)
		for _, spec := range existingSpecs {
			name := pythonPackageName(spec)
			if name == "" {
				continue
			}
			if want, isOTel := pythonDependencyPins[name]; isOTel {
				if err := validatePythonSpec(spec, name, want); err != nil {
					return "", err
				}
			}
			seen[name] = true
		}
		var missing []string
		for _, p := range packages {
			lower := strings.ToLower(p)
			if !seen[lower] {
				missing = append(missing, p)
				seen[lower] = true
			}
		}
		if len(missing) == 0 {
			return text, nil
		}
		if depStart == depEnd {
			indent := leadingWhitespace(lines[depStart])
			itemIndent := indent + "  "
			var replacement []string
			replacement = append(replacement, indent+"dependencies = [")
			for _, spec := range existingSpecs {
				replacement = append(replacement, fmt.Sprintf(`%s"%s",`, itemIndent, spec))
			}
			for _, p := range missing {
				lower := strings.ToLower(p)
				replacement = append(replacement, fmt.Sprintf(`%s"%s==%s",`, itemIndent, p, pythonDependencyPins[lower]))
			}
			replacement = append(replacement, indent+"]")
			updated := append(append([]string{}, lines[:depStart]...), append(replacement, lines[depStart+1:]...)...)
			return strings.TrimRight(strings.Join(updated, "\n"), "\n") + "\n", nil
		}
		itemIndent := detectArrayItemIndent(lines, depStart+1, depEnd)
		ensureTrailingCommaBefore(lines, depStart+1, depEnd)
		var additions []string
		for _, p := range missing {
			lower := strings.ToLower(p)
			additions = append(additions, fmt.Sprintf(`%s"%s==%s",`, itemIndent, p, pythonDependencyPins[lower]))
		}
		updated := append(append([]string{}, lines[:depEnd]...), append(additions, lines[depEnd:]...)...)
		return strings.TrimRight(strings.Join(updated, "\n"), "\n") + "\n", nil
	}

	if poetryStart >= 0 {
		poetryEnd := findTOMLSectionEnd(lines, poetryStart)
		seen := map[string]bool{}
		for i := poetryStart + 1; i < poetryEnd; i++ {
			trimmed := strings.TrimSpace(lines[i])
			if trimmed == "" || strings.HasPrefix(trimmed, "#") {
				continue
			}
			eq := strings.IndexByte(trimmed, '=')
			if eq < 0 {
				continue
			}
			rawKey := strings.TrimSpace(trimmed[:eq])
			name := strings.ToLower(strings.Trim(rawKey, `"'`))
			val := stripTOMLComment(strings.TrimSpace(trimmed[eq+1:]))
			if want, isOTel := pythonDependencyPins[name]; isOTel {
				unquoted := strings.TrimSpace(strings.Trim(val, `"'`))
				if unquoted != "=="+want && unquoted != want {
					return "", fmt.Errorf("incompatible OpenTelemetry requirement %s = %s; expected %s = \"==%s\"", name, val, name, want)
				}
			}
			if name != "" {
				seen[name] = true
			}
		}
		var additions []string
		for _, p := range packages {
			lower := strings.ToLower(p)
			if !seen[lower] {
				additions = append(additions, fmt.Sprintf(`%s = "==%s"`, p, pythonDependencyPins[lower]))
				seen[lower] = true
			}
		}
		if len(additions) == 0 {
			return text, nil
		}
		insertAt := trimTrailingBlankLineIndex(lines, poetryStart+1, poetryEnd)
		updated := append(append([]string{}, lines[:insertAt]...), append(additions, lines[insertAt:]...)...)
		return strings.TrimRight(strings.Join(updated, "\n"), "\n") + "\n", nil
	}

	seen := map[string]bool{}
	var block []string
	block = append(block, "[project]", "dependencies = [")
	for _, p := range packages {
		lower := strings.ToLower(p)
		if seen[lower] {
			continue
		}
		seen[lower] = true
		block = append(block, fmt.Sprintf(`  "%s==%s",`, p, pythonDependencyPins[lower]))
	}
	block = append(block, "]")
	base := strings.TrimRight(normalized, "\n")
	if base == "" {
		return strings.Join(block, "\n") + "\n", nil
	}
	return base + "\n\n" + strings.Join(block, "\n") + "\n", nil
}

func isTOMLTableHeader(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "[") || strings.HasPrefix(trimmed, "[[") {
		return "", false
	}
	end := strings.IndexByte(trimmed, ']')
	if end < 0 {
		return "", false
	}
	after := strings.TrimSpace(trimmed[end+1:])
	if after != "" && !strings.HasPrefix(after, "#") {
		return "", false
	}
	return strings.ToLower(strings.TrimSpace(trimmed[1:end])), true
}

func findTOMLSectionEnd(lines []string, start int) int {
	for i := start + 1; i < len(lines); i++ {
		if _, ok := isTOMLTableHeader(lines[i]); ok {
			return i
		}
	}
	return len(lines)
}

func findTOMLKeyLine(lines []string, start, end int, key string) int {
	for i := start; i < end; i++ {
		trimmed := strings.TrimSpace(lines[i])
		if strings.HasPrefix(trimmed, key) {
			rest := strings.TrimSpace(trimmed[len(key):])
			if strings.HasPrefix(rest, "=") {
				return i
			}
		}
	}
	return -1
}

func findTOMLArrayEnd(lines []string, start, sectionEnd int) (int, error) {
	eq := strings.IndexByte(lines[start], '=')
	if eq < 0 {
		return -1, fmt.Errorf("invalid dependencies entry in pyproject.toml")
	}
	depth := 0
	started := false
	for i := start; i < sectionEnd; i++ {
		segment := lines[i]
		if i == start {
			segment = segment[eq+1:]
		}
		inQuote := byte(0)
		escaped := false
		for j := 0; j < len(segment); j++ {
			ch := segment[j]
			if inQuote != 0 {
				if escaped {
					escaped = false
					continue
				}
				if ch == '\\' && inQuote == '"' {
					escaped = true
					continue
				}
				if ch == inQuote {
					inQuote = 0
				}
				continue
			}
			if ch == '#' {
				break
			}
			if ch == '"' || ch == '\'' {
				inQuote = ch
				continue
			}
			if ch == '[' {
				depth++
				started = true
			} else if ch == ']' {
				depth--
				if started && depth == 0 {
					return i, nil
				}
			}
		}
	}
	return -1, fmt.Errorf("unterminated dependencies array in pyproject.toml")
}

func extractTOMLArrayStrings(lines []string, start, end int) []string {
	var specs []string
	eq := strings.IndexByte(lines[start], '=')
	for i := start; i <= end; i++ {
		segment := lines[i]
		if i == start && eq >= 0 {
			segment = segment[eq+1:]
		}
		inQuote := byte(0)
		escaped := false
		tokenStart := -1
		for j := 0; j < len(segment); j++ {
			ch := segment[j]
			if inQuote != 0 {
				if escaped {
					escaped = false
					continue
				}
				if ch == '\\' && inQuote == '"' {
					escaped = true
					continue
				}
				if ch == inQuote {
					specs = append(specs, segment[tokenStart:j])
					inQuote = 0
					tokenStart = -1
				}
				continue
			}
			if ch == '#' {
				break
			}
			if ch == '"' || ch == '\'' {
				inQuote = ch
				tokenStart = j + 1
			}
		}
	}
	return specs
}

func pythonPackageName(spec string) string {
	s := strings.TrimSpace(spec)
	if s == "" {
		return ""
	}
	fields := strings.FieldsFunc(s, func(r rune) bool {
		return r == '=' || r == '<' || r == '>' || r == '!' || r == '~' || r == '[' || r == ';' || r == ' '
	})
	if len(fields) == 0 {
		return ""
	}
	return strings.ToLower(fields[0])
}

func validatePythonSpec(spec, name, want string) error {
	withoutMarker := strings.TrimSpace(strings.SplitN(spec, ";", 2)[0])
	lower := strings.ToLower(withoutMarker)
	if !strings.HasPrefix(lower, name) {
		return nil
	}
	rest := strings.TrimSpace(withoutMarker[len(name):])
	if rest == "" {
		return fmt.Errorf("unpinned OpenTelemetry requirement %s; expected %s==%s", spec, name, want)
	}
	if !strings.Contains("=<>!~[", rest[:1]) {
		return nil
	}
	if rest != "=="+want {
		return fmt.Errorf("incompatible OpenTelemetry requirement %s; expected %s==%s", spec, name, want)
	}
	return nil
}

func stripTOMLComment(s string) string {
	inQuote := byte(0)
	escaped := false
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if inQuote != 0 {
			if escaped {
				escaped = false
				continue
			}
			if ch == '\\' && inQuote == '"' {
				escaped = true
				continue
			}
			if ch == inQuote {
				inQuote = 0
			}
			continue
		}
		if ch == '"' || ch == '\'' {
			inQuote = ch
			continue
		}
		if ch == '#' {
			return strings.TrimSpace(s[:i])
		}
	}
	return strings.TrimSpace(s)
}

func trimTrailingBlankLineIndex(lines []string, start, end int) int {
	idx := end
	for idx > start && strings.TrimSpace(lines[idx-1]) == "" {
		idx--
	}
	return idx
}

func leadingWhitespace(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] != ' ' && s[i] != '\t' {
			return s[:i]
		}
	}
	return s
}

func detectArrayItemIndent(lines []string, start, end int) string {
	for i := start; i < end; i++ {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			return leadingWhitespace(lines[i])
		}
	}
	return "  "
}

func ensureTrailingCommaBefore(lines []string, start, end int) {
	for i := end - 1; i >= start; i-- {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		code := stripTOMLComment(lines[i])
		if strings.HasSuffix(code, ",") {
			return
		}
		idx := strings.LastIndex(lines[i], code)
		if idx >= 0 {
			pos := idx + len(code)
			lines[i] = lines[i][:pos] + "," + lines[i][pos:]
		}
		return
	}
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
    from opentelemetry.instrumentation.django import DjangoInstrumentor
except Exception:
    DjangoInstrumentor = None

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
if DjangoInstrumentor is not None:
    DjangoInstrumentor().instrument()
if SQLAlchemyInstrumentor is not None:
    SQLAlchemyInstrumentor().instrument(enable_commenter=True, commenter_options={})
if Psycopg2Instrumentor is not None:
    Psycopg2Instrumentor().instrument(enable_commenter=True)

atexit.register(logger_provider.shutdown)
atexit.register(meter_provider.shutdown)
atexit.register(trace_provider.shutdown)
`
