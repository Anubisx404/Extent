package instrumenter

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDryRunAndInvalidModesNeverMutate(t *testing.T) {
	root := t.TempDir()
	packageJSON := []byte(`{"dependencies":{"express":"4.21.2"}}`)
	server := []byte("console.log('start')\n")
	mustWrite(t, filepath.Join(root, "package.json"), string(packageJSON))
	mustWrite(t, filepath.Join(root, "server.js"), string(server))

	preview, err := Instrument(root, Options{Mode: "bootstrap", DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.ChangedFiles) == 0 || preview.Diff == "" {
		t.Fatalf("missing deterministic preview: %#v", preview)
	}
	assertExactFile(t, filepath.Join(root, "package.json"), packageJSON)
	assertExactFile(t, filepath.Join(root, "server.js"), server)
	if _, err := os.Stat(filepath.Join(root, ".extent")); !os.IsNotExist(err) {
		t.Fatalf("preview created transaction state: %v", err)
	}

	if _, err := Instrument(root, Options{Mode: "invalid"}); err == nil {
		t.Fatal("invalid mode accepted")
	}
	assertExactFile(t, filepath.Join(root, "package.json"), packageJSON)
}

func TestNodeTransactionPinsDependenciesAndUndoRestoresEverything(t *testing.T) {
	root := t.TempDir()
	packageJSON := []byte(`{"dependencies":{"express":"4.21.2","@opentelemetry/api":"1.9.1"}}`)
	server := []byte("console.log('start')\n")
	mustWrite(t, filepath.Join(root, "package.json"), string(packageJSON))
	mustWrite(t, filepath.Join(root, "server.js"), string(server))

	if _, err := Instrument(root, Options{Mode: "bootstrap"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Dependencies map[string]string `json:"dependencies"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"@opentelemetry/api":                        "1.9.1",
		"@opentelemetry/sdk-node":                   "0.220.0",
		"@opentelemetry/auto-instrumentations-node": "0.78.0",
		"@opentelemetry/exporter-trace-otlp-http":   "0.220.0",
		"@opentelemetry/exporter-metrics-otlp-http": "0.220.0",
		"@opentelemetry/exporter-logs-otlp-http":    "0.220.0",
		"@opentelemetry/sdk-logs":                   "0.220.0",
		"@opentelemetry/sdk-metrics":                "2.9.0",
		"@opentelemetry/resources":                  "2.9.0",
	}
	for name, version := range want {
		if manifest.Dependencies[name] != version {
			t.Fatalf("%s = %q, want %q", name, manifest.Dependencies[name], version)
		}
	}
	bootstrap, err := os.ReadFile(filepath.Join(root, "extent.instrumentation.js"))
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"exportIntervalMillis: metricExportIntervalMillis", `logs.getLogger("extent.http")`, `http.request.header.x_request_id`} {
		if !bytes.Contains(bootstrap, []byte(required)) {
			t.Fatalf("generated Node bootstrap missing %q", required)
		}
	}
	if _, err := Instrument(root, Options{Mode: "bootstrap", Undo: true}); err != nil {
		t.Fatal(err)
	}
	assertExactFile(t, filepath.Join(root, "package.json"), packageJSON)
	assertExactFile(t, filepath.Join(root, "server.js"), server)
	if _, err := os.Stat(filepath.Join(root, "extent.instrumentation.js")); !os.IsNotExist(err) {
		t.Fatalf("generated bootstrap survived undo: %v", err)
	}
}

func TestNodeIncompatibleExistingPinFailsBeforeMutation(t *testing.T) {
	root := t.TempDir()
	original := []byte(`{"dependencies":{"express":"4.21.2","@opentelemetry/sdk-node":"0.57.0"}}`)
	mustWrite(t, filepath.Join(root, "package.json"), string(original))
	mustWrite(t, filepath.Join(root, "server.js"), "console.log('start')\n")
	if _, err := Instrument(root, Options{Mode: "bootstrap"}); err == nil {
		t.Fatal("incompatible existing OpenTelemetry pin accepted")
	}
	assertExactFile(t, filepath.Join(root, "package.json"), original)
	if _, err := os.Stat(filepath.Join(root, ".extent")); !os.IsNotExist(err) {
		t.Fatalf("failed plan mutated project: %v", err)
	}
}

func TestNodeCompatibleRangesAndDevDependenciesArePreserved(t *testing.T) {
	root := t.TempDir()
	original := `{"dependencies":{"@opentelemetry/api":"^1.9.0"},"devDependencies":{"@opentelemetry/sdk-node":"^0.220.0","@opentelemetry/resources":"~2.9.0"}}`
	mustWrite(t, filepath.Join(root, "package.json"), original)
	mustWrite(t, filepath.Join(root, "server.js"), "console.log('start')\n")
	if _, err := Instrument(root, Options{Mode: "bootstrap"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Dependencies["@opentelemetry/api"] != "^1.9.0" ||
		manifest.DevDependencies["@opentelemetry/sdk-node"] != "^0.220.0" ||
		manifest.DevDependencies["@opentelemetry/resources"] != "~2.9.0" {
		t.Fatalf("compatible constraints were replaced: %#v %#v", manifest.Dependencies, manifest.DevDependencies)
	}
	if _, duplicated := manifest.Dependencies["@opentelemetry/sdk-node"]; duplicated {
		t.Fatal("devDependency was duplicated into dependencies")
	}
}

func TestNodeInjectionPreservesShebang(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "package.json"), `{"dependencies":{}}`)
	mustWrite(t, filepath.Join(root, "server.js"), "#!/usr/bin/env node\nconsole.log('start')\n")
	if _, err := Instrument(root, Options{Mode: "bootstrap"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "server.js"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(data), "#!/usr/bin/env node\nrequire(\"./extent.instrumentation\"); // extent:otel\n") {
		t.Fatalf("Node shebang order broken:\n%s", data)
	}
}

func TestPythonPinsDependenciesAndPreservesPreamble(t *testing.T) {
	root := t.TempDir()
	requirements := []byte("fastapi==0.115.0\n")
	entrypoint := []byte("#!/usr/bin/env python3\n# -*- coding: utf-8 -*-\nfrom __future__ import annotations\nprint('start')\n")
	mustWrite(t, filepath.Join(root, "requirements.txt"), string(requirements))
	mustWrite(t, filepath.Join(root, "main.py"), string(entrypoint))
	if _, err := Instrument(root, Options{Mode: "bootstrap"}); err != nil {
		t.Fatal(err)
	}
	assertFileContains(t, filepath.Join(root, "requirements.txt"), "opentelemetry-api==1.43.0")
	assertFileContains(t, filepath.Join(root, "requirements.txt"), "opentelemetry-instrumentation-fastapi==0.64b0")
	for _, required := range []string{"FastAPIInstrumentor().instrument()", "FlaskInstrumentor().instrument()", "LoggingHandler(level=logging.NOTSET", "export_interval_millis=metric_export_interval", "OTEL_INSTRUMENTATION_HTTP_CAPTURE_HEADERS_SERVER_REQUEST", "atexit.register(trace_provider.shutdown)"} {
		assertFileContains(t, filepath.Join(root, "extent_instrumentation.py"), required)
	}
	data, err := os.ReadFile(filepath.Join(root, "main.py"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	future := strings.Index(text, "from __future__ import annotations")
	injected := strings.Index(text, "import extent_instrumentation")
	if !strings.HasPrefix(text, "#!/usr/bin/env python3\n# -*- coding: utf-8 -*-\n") || future < 0 || injected < future {
		t.Fatalf("Python preamble/import order broken:\n%s", text)
	}
	if _, err := Instrument(root, Options{Mode: "bootstrap", Undo: true}); err != nil {
		t.Fatal(err)
	}
	assertExactFile(t, filepath.Join(root, "requirements.txt"), requirements)
	assertExactFile(t, filepath.Join(root, "main.py"), entrypoint)
}

func TestPythonIncompatibleOrUnpinnedOpenTelemetryRequirementFailsBeforeMutation(t *testing.T) {
	for name, requirement := range map[string]string{
		"incompatible": "opentelemetry-api>=2.0.0\n",
		"unpinned":     "opentelemetry-sdk\n",
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			original := []byte(requirement)
			mustWrite(t, filepath.Join(root, "requirements.txt"), requirement)
			mustWrite(t, filepath.Join(root, "main.py"), "print('start')\n")
			if _, err := Instrument(root, Options{Mode: "bootstrap"}); err == nil {
				t.Fatal("unsafe Python OpenTelemetry requirement accepted")
			}
			assertExactFile(t, filepath.Join(root, "requirements.txt"), original)
			if _, err := os.Stat(filepath.Join(root, ".extent")); !os.IsNotExist(err) {
				t.Fatalf("failed plan mutated project: %v", err)
			}
		})
	}
}

func TestGoEntrypointWithoutExistingImportsAndAmbiguity(t *testing.T) {
	root := t.TempDir()
	originalMod := []byte("module example.com/service\n\ngo 1.22\n")
	originalMain := []byte("package main\n\nfunc main() {}\n")
	mustWrite(t, filepath.Join(root, "go.mod"), string(originalMod))
	mustWrite(t, filepath.Join(root, "main.go"), string(originalMain))
	if _, err := Instrument(root, Options{Mode: "bootstrap"}); err != nil {
		t.Fatal(err)
	}
	assertFileContains(t, filepath.Join(root, "main.go"), `extentotel "example.com/service/internal/observability"`)
	assertFileContains(t, filepath.Join(root, "main.go"), "defer extentotel.Shutdown()")
	assertFileContains(t, filepath.Join(root, "internal/observability/otel.go"), "func Shutdown()")
	assertFileContains(t, filepath.Join(root, "internal/observability/otel.go"), "otel.SetTextMapPropagator")
	for _, pin := range []string{
		"go.opentelemetry.io/otel v1.35.0",
		"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp v1.35.0",
		"go.opentelemetry.io/otel/sdk v1.35.0",
	} {
		assertFileContains(t, filepath.Join(root, "go.mod"), pin)
	}
	if _, err := Instrument(root, Options{Mode: "bootstrap", Undo: true}); err != nil {
		t.Fatal(err)
	}
	assertExactFile(t, filepath.Join(root, "go.mod"), originalMod)
	assertExactFile(t, filepath.Join(root, "main.go"), originalMain)
	if _, err := os.Stat(filepath.Join(root, "internal", "observability", "otel.go")); !os.IsNotExist(err) {
		t.Fatalf("undo retained Go bootstrap: %v", err)
	}

	ambiguous := t.TempDir()
	mustWrite(t, filepath.Join(ambiguous, "go.mod"), "module example.com/multi\n\ngo 1.22\n")
	mustWrite(t, filepath.Join(ambiguous, "cmd", "a", "main.go"), "package main\nfunc main(){}\n")
	mustWrite(t, filepath.Join(ambiguous, "cmd", "b", "main.go"), "package main\nfunc main(){}\n")
	if _, err := Instrument(ambiguous, Options{Mode: "bootstrap"}); err == nil {
		t.Fatal("ambiguous Go entrypoints accepted")
	}
	if _, err := os.Stat(filepath.Join(ambiguous, ".extent")); !os.IsNotExist(err) {
		t.Fatalf("ambiguous plan mutated project: %v", err)
	}
}

func TestGoIncompatibleExistingPinFailsBeforeMutation(t *testing.T) {
	root := t.TempDir()
	original := []byte("module example.com/service\n\ngo 1.22\n\nrequire go.opentelemetry.io/otel v1.30.0\n")
	mustWrite(t, filepath.Join(root, "go.mod"), string(original))
	mustWrite(t, filepath.Join(root, "main.go"), "package main\nfunc main(){}\n")
	if _, err := Instrument(root, Options{Mode: "bootstrap"}); err == nil {
		t.Fatal("incompatible Go OpenTelemetry pin accepted")
	}
	assertExactFile(t, filepath.Join(root, "go.mod"), original)
	if _, err := os.Stat(filepath.Join(root, ".extent")); !os.IsNotExist(err) {
		t.Fatalf("failed plan mutated project: %v", err)
	}
}

func TestUnsupportedZeroCodeAndDeepExecutionFailBeforeMutation(t *testing.T) {
	root := t.TempDir()
	if _, err := Instrument(root, Options{Mode: "zero-code"}); err == nil {
		t.Fatal("zero-code accepted unsupported project")
	}
	if _, err := Instrument(root, Options{Mode: "deep", RunDeepCodemods: true}); err == nil {
		t.Fatal("unsafe deep execution accepted")
	}
	if _, err := os.Stat(filepath.Join(root, ".extent")); !os.IsNotExist(err) {
		t.Fatalf("unsupported operation mutated project: %v", err)
	}
}

func assertExactFile(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("%s bytes changed:\nwant %q\n got %q", path, want, got)
	}
}
