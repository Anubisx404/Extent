package instrumenter

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readTestFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func assertFileNotContains(t *testing.T, path, unwanted string) {
	t.Helper()
	if strings.Contains(readTestFile(t, path), unwanted) {
		t.Fatalf("expected %s not to contain %q", path, unwanted)
	}
}

func TestNodeESMStartScriptLoadsBootstrapWithImportFlag(t *testing.T) {
	root := t.TempDir()
	packageJSON := []byte("{\n  \"name\": \"api\",\n  \"type\": \"module\",\n  \"scripts\": {\n    \"start\": \"node server.js\"\n  },\n  \"dependencies\": {\n    \"express\": \"^4.18.0\"\n  }\n}\n")
	server := []byte("import express from \"express\";\nconsole.log(express);\n")
	mustWrite(t, filepath.Join(root, "package.json"), string(packageJSON))
	mustWrite(t, filepath.Join(root, "server.js"), string(server))

	result, err := Instrument(root, Options{Mode: "bootstrap"})
	if err != nil {
		t.Fatal(err)
	}
	assertChanged(t, result, "package.json")
	assertChanged(t, result, "extent.instrumentation.mjs")
	assertExactFile(t, filepath.Join(root, "server.js"), server)
	assertFileContains(t, filepath.Join(root, "package.json"), `"start": "node --import ./extent.instrumentation.mjs server.js"`)
	assertFileContains(t, filepath.Join(root, "package.json"), `"@opentelemetry/instrumentation": "0.220.0"`)

	bootstrap := readTestFile(t, filepath.Join(root, "extent.instrumentation.mjs"))
	register := strings.Index(bootstrap, `register("@opentelemetry/instrumentation/hook.mjs", import.meta.url);`)
	sdk := strings.Index(bootstrap, "new NodeSDK(")
	if register < 0 || sdk < 0 || register > sdk {
		t.Fatalf("loader hook must be registered before the SDK is set up (register=%d sdk=%d)", register, sdk)
	}
	assertFileContains(t, filepath.Join(root, "extent.instrumentation.mjs"), `import { register } from "node:module";`)

	second, err := Instrument(root, Options{Mode: "bootstrap"})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.ChangedFiles) != 0 {
		t.Fatalf("expected idempotent second run, changed %#v", second.ChangedFiles)
	}

	if _, err := Instrument(root, Options{Mode: "bootstrap", Undo: true}); err != nil {
		t.Fatal(err)
	}
	assertExactFile(t, filepath.Join(root, "package.json"), packageJSON)
	assertExactFile(t, filepath.Join(root, "server.js"), server)
	if _, err := os.Stat(filepath.Join(root, "extent.instrumentation.mjs")); !os.IsNotExist(err) {
		t.Fatalf("generated ESM bootstrap survived undo: %v", err)
	}
}

func TestNodeESMWithoutStartScriptFallsBackToSourceImport(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "package.json"), `{"type":"module","dependencies":{"express":"^4.18.0"}}`)
	mustWrite(t, filepath.Join(root, "index.js"), "import express from \"express\";\nconsole.log(express);\n")

	if _, err := Instrument(root, Options{Mode: "bootstrap"}); err != nil {
		t.Fatal(err)
	}
	assertFileContains(t, filepath.Join(root, "index.js"), `import "./extent.instrumentation.mjs"; // extent:otel`)
	assertFileContains(t, filepath.Join(root, "extent.instrumentation.mjs"), "node --import ./extent.instrumentation.mjs <entrypoint>")
	assertFileNotContains(t, filepath.Join(root, "package.json"), "--import")
}

func TestNodeESMDetectedFromMjsEntrypoint(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "package.json"), `{"dependencies":{"express":"^4.18.0"}}`)
	mustWrite(t, filepath.Join(root, "index.mjs"), "import express from \"express\";\nconsole.log(express);\n")

	if _, err := Instrument(root, Options{Mode: "bootstrap"}); err != nil {
		t.Fatal(err)
	}
	assertFileContains(t, filepath.Join(root, "index.mjs"), `import "./extent.instrumentation.mjs"; // extent:otel`)
	assertFileContains(t, filepath.Join(root, "extent.instrumentation.mjs"), "@opentelemetry/sdk-node")
	assertFileContains(t, filepath.Join(root, "package.json"), `"@opentelemetry/instrumentation": "0.220.0"`)
}

func TestNodeCommonJSLeavesESMLoaderAndStartScriptAlone(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "package.json"), `{"scripts":{"start":"node server.js"},"dependencies":{"express":"^4.18.0"}}`)
	mustWrite(t, filepath.Join(root, "server.js"), "const express = require(\"express\");\n")

	if _, err := Instrument(root, Options{Mode: "bootstrap"}); err != nil {
		t.Fatal(err)
	}
	assertFileContains(t, filepath.Join(root, "server.js"), `require("./extent.instrumentation"); // extent:otel`)
	assertFileContains(t, filepath.Join(root, "package.json"), `"start": "node server.js"`)
	assertFileNotContains(t, filepath.Join(root, "package.json"), "@opentelemetry/instrumentation\"")
}

func TestNodeHTTPRequestHookLogsOnlyIncomingRequests(t *testing.T) {
	cjs := t.TempDir()
	mustWrite(t, filepath.Join(cjs, "package.json"), `{"dependencies":{}}`)
	mustWrite(t, filepath.Join(cjs, "server.js"), "console.log('start')\n")
	if _, err := Instrument(cjs, Options{Mode: "bootstrap"}); err != nil {
		t.Fatal(err)
	}
	assertFileContains(t, filepath.Join(cjs, "extent.instrumentation.js"), `const { IncomingMessage } = require("node:http");`)
	assertFileContains(t, filepath.Join(cjs, "extent.instrumentation.js"), "if (!(request instanceof IncomingMessage)) return;")

	esm := t.TempDir()
	mustWrite(t, filepath.Join(esm, "package.json"), `{"type":"module","dependencies":{}}`)
	mustWrite(t, filepath.Join(esm, "index.js"), "console.log('start')\n")
	if _, err := Instrument(esm, Options{Mode: "bootstrap"}); err != nil {
		t.Fatal(err)
	}
	assertFileContains(t, filepath.Join(esm, "extent.instrumentation.mjs"), `import { IncomingMessage } from "node:http";`)
	assertFileContains(t, filepath.Join(esm, "extent.instrumentation.mjs"), "if (!(request instanceof IncomingMessage)) return;")
}

func TestPackageJSONKeepsKeyOrderAndIndentation(t *testing.T) {
	cases := []struct {
		name   string
		input  string
		prefix string
	}{
		{
			name:   "four spaces",
			input:  "{\n    \"name\": \"demo\",\n    \"version\": \"1.0.0\",\n    \"scripts\": {\n        \"test\": \"echo ok\"\n    },\n    \"dependencies\": {\n        \"express\": \"^4.18.0\"\n    },\n    \"main\": \"server.js\"\n}\n",
			prefix: "{\n    \"name\": \"demo\",\n    \"version\": \"1.0.0\",\n    \"scripts\": {\n        \"test\": \"echo ok\"\n    },\n    \"dependencies\": {\n        \"express\": \"^4.18.0\",\n        \"@opentelemetry/api\": \"1.9.1\",",
		},
		{
			name:   "tab",
			input:  "{\n\t\"name\": \"demo\",\n\t\"dependencies\": {\n\t\t\"express\": \"^4.18.0\"\n\t},\n\t\"license\": \"MIT\",\n\t\"x\": 1.50\n}\n",
			prefix: "{\n\t\"name\": \"demo\",\n\t\"dependencies\": {\n\t\t\"express\": \"^4.18.0\",\n\t\t\"@opentelemetry/api\": \"1.9.1\",",
		},
		{
			name:   "minified",
			input:  `{"name":"demo","dependencies":{"express":"^4.18.0"},"license":"MIT"}`,
			prefix: "{\n  \"name\": \"demo\",\n  \"dependencies\": {\n    \"express\": \"^4.18.0\",\n    \"@opentelemetry/api\": \"1.9.1\",",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			mustWrite(t, filepath.Join(root, "package.json"), tc.input)
			mustWrite(t, filepath.Join(root, "server.js"), "console.log('start')\n")
			if _, err := Instrument(root, Options{Mode: "bootstrap"}); err != nil {
				t.Fatal(err)
			}
			got := readTestFile(t, filepath.Join(root, "package.json"))
			if !strings.HasPrefix(got, tc.prefix) {
				t.Fatalf("package.json layout changed.\nwant prefix:\n%s\ngot:\n%s", tc.prefix, got)
			}
			if !strings.HasSuffix(got, "}\n") {
				t.Fatalf("package.json missing closing brace and newline: %q", got)
			}
		})
	}
}

func TestPackageJSONPreservesNumbersAndOrderOfExistingKeys(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "package.json"), "{\n  \"version\": 1.50,\n  \"name\": \"demo\",\n  \"dependencies\": {\n    \"express\": \"^4.18.0\"\n  }\n}\n")
	mustWrite(t, filepath.Join(root, "server.js"), "console.log('start')\n")
	if _, err := Instrument(root, Options{Mode: "bootstrap"}); err != nil {
		t.Fatal(err)
	}
	got := readTestFile(t, filepath.Join(root, "package.json"))
	if !strings.HasPrefix(got, "{\n  \"version\": 1.50,\n  \"name\": \"demo\",\n") {
		t.Fatalf("existing key order or number literal changed:\n%s", got)
	}
}

func TestPackageJSONUntouchedWhenAlreadyInstrumented(t *testing.T) {
	root := t.TempDir()
	original := []byte(`{"name":"x","dependencies":{"@opentelemetry/api":"1.9.1","@opentelemetry/sdk-node":"0.220.0","@opentelemetry/auto-instrumentations-node":"0.78.0","@opentelemetry/exporter-trace-otlp-http":"0.220.0","@opentelemetry/exporter-metrics-otlp-http":"0.220.0","@opentelemetry/exporter-logs-otlp-http":"0.220.0","@opentelemetry/sdk-logs":"0.220.0","@opentelemetry/sdk-metrics":"2.9.0","@opentelemetry/resources":"2.9.0"}}`)
	mustWrite(t, filepath.Join(root, "package.json"), string(original))
	mustWrite(t, filepath.Join(root, "server.js"), "// extent:otel\nconsole.log('start')\n")
	result, err := Instrument(root, Options{Mode: "bootstrap"})
	if err != nil {
		t.Fatal(err)
	}
	for _, changed := range result.ChangedFiles {
		if changed == "package.json" {
			t.Fatalf("package.json rewritten although no dependency changed: %#v", result.ChangedFiles)
		}
	}
	assertExactFile(t, filepath.Join(root, "package.json"), original)
}

func TestPythonAddsOnlyDetectedFrameworkInstrumentation(t *testing.T) {
	flaskRoot := t.TempDir()
	mustWrite(t, filepath.Join(flaskRoot, "requirements.txt"), "flask==3.0.0\n")
	mustWrite(t, filepath.Join(flaskRoot, "app.py"), "from flask import Flask\napp = Flask(__name__)\n")
	if _, err := Instrument(flaskRoot, Options{Mode: "bootstrap"}); err != nil {
		t.Fatal(err)
	}
	assertFileContains(t, filepath.Join(flaskRoot, "requirements.txt"), "opentelemetry-instrumentation-flask==0.64b0")
	assertFileNotContains(t, filepath.Join(flaskRoot, "requirements.txt"), "opentelemetry-instrumentation-fastapi")
	assertFileContains(t, filepath.Join(flaskRoot, "extent_instrumentation.py"), "FlaskInstrumentor")

	fastapiRoot := t.TempDir()
	mustWrite(t, filepath.Join(fastapiRoot, "requirements.txt"), "fastapi==0.115.0\n")
	mustWrite(t, filepath.Join(fastapiRoot, "main.py"), "from fastapi import FastAPI\napp = FastAPI()\n")
	if _, err := Instrument(fastapiRoot, Options{Mode: "bootstrap"}); err != nil {
		t.Fatal(err)
	}
	assertFileContains(t, filepath.Join(fastapiRoot, "requirements.txt"), "opentelemetry-instrumentation-fastapi==0.64b0")
	assertFileNotContains(t, filepath.Join(fastapiRoot, "requirements.txt"), "opentelemetry-instrumentation-flask")
	assertFileContains(t, filepath.Join(fastapiRoot, "extent_instrumentation.py"), "except Exception:")
}

func TestGoBootstrapHandlesSignalsMetricsAndLifecycle(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "go.mod"), "module example.com/payments\n\ngo 1.22\n")
	mustWrite(t, filepath.Join(root, "main.go"), "package main\n\nfunc main() {}\n")
	if _, err := Instrument(root, Options{Mode: "bootstrap"}); err != nil {
		t.Fatal(err)
	}
	otel := "internal/observability/otel.go"
	assertFileContains(t, filepath.Join(root, otel), `"os/signal"`)
	assertFileContains(t, filepath.Join(root, otel), "signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)")
	assertFileContains(t, filepath.Join(root, otel), "os.Exit(130)")
	assertFileContains(t, filepath.Join(root, otel), "os.Exit(143)")
	assertFileContains(t, filepath.Join(root, otel), "go shutdownOnSignal()")
	assertFileContains(t, filepath.Join(root, otel), `"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"`)
	assertFileContains(t, filepath.Join(root, otel), `sdkmetric "go.opentelemetry.io/otel/sdk/metric"`)
	assertFileContains(t, filepath.Join(root, otel), "meterProvider.Shutdown(ctx)")
	assertFileContains(t, filepath.Join(root, otel), "log.Fatal")
	assertFileContains(t, filepath.Join(root, "go.mod"), "go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp v1.35.0")
	assertFileContains(t, filepath.Join(root, "go.mod"), "go.opentelemetry.io/otel/sdk/metric v1.35.0")
}

func TestGoBootstrapSkipsSignalHandlerWhenAppHandlesSignals(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "go.mod"), "module example.com/payments\n\ngo 1.22\n")
	mustWrite(t, filepath.Join(root, "main.go"), "package main\n\nimport (\n\t\"os\"\n\t\"os/signal\"\n)\n\nfunc main() {\n\tc := make(chan os.Signal, 1)\n\tsignal.Notify(c, os.Interrupt)\n\t<-c\n}\n")
	if _, err := Instrument(root, Options{Mode: "bootstrap"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "internal/observability/otel.go"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "go shutdownOnSignal()") {
		t.Fatal("generated signal handler would race the application's own handler")
	}
}

func TestNodeESMRejectsIncompatibleOpenTelemetryInstrumentationPin(t *testing.T) {
	root := t.TempDir()
	original := []byte(`{"type":"module","dependencies":{"@opentelemetry/instrumentation":"0.57.0"}}`)
	mustWrite(t, filepath.Join(root, "package.json"), string(original))
	mustWrite(t, filepath.Join(root, "index.js"), "console.log('start')\n")
	if _, err := Instrument(root, Options{Mode: "bootstrap"}); err == nil {
		t.Fatal("incompatible @opentelemetry/instrumentation pin accepted")
	}
	assertExactFile(t, filepath.Join(root, "package.json"), original)
}
