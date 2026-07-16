package instrumenter

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstrumentNodeCommonJSInjectsBootstrapAndDependencies(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "package.json"), `{"dependencies":{"express":"^4.18.0","pg":"^8.0.0","redis":"^4.0.0"}}`)
	mustWrite(t, filepath.Join(root, "server.js"), `const express = require("express");
console.log("start");
`)

	result, err := Instrument(root, Options{})
	if err != nil {
		t.Fatal(err)
	}

	assertChanged(t, result, "server.js")
	assertFileContains(t, filepath.Join(root, "server.js"), `require("./extent.instrumentation"); // extent:otel`)
	assertFileContains(t, filepath.Join(root, "extent.instrumentation.js"), "@opentelemetry/sdk-node")
	assertFileContains(t, filepath.Join(root, "extent.instrumentation.js"), "enhancedDatabaseReporting")
	assertFileContains(t, filepath.Join(root, "package.json"), "@opentelemetry/auto-instrumentations-node")
	assertFileContains(t, filepath.Join(root, "package.json"), "@opentelemetry/instrumentation-pg")
	assertFileContains(t, filepath.Join(root, "package.json"), "@opentelemetry/instrumentation-redis")

	second, err := Instrument(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.ChangedFiles) != 0 {
		t.Fatalf("expected idempotent second run, changed %#v", second.ChangedFiles)
	}
}

func TestInstrumentNodeESMUsesImportBootstrap(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "package.json"), `{"type":"module","dependencies":{"express":"^4.18.0"}}`)
	mustWrite(t, filepath.Join(root, "index.js"), `import express from "express";
console.log(express);
`)

	_, err := Instrument(root, Options{})
	if err != nil {
		t.Fatal(err)
	}

	assertFileContains(t, filepath.Join(root, "index.js"), `import "./extent.instrumentation.mjs"; // extent:otel`)
	assertFileContains(t, filepath.Join(root, "extent.instrumentation.mjs"), "@opentelemetry/sdk-node")
}

func TestInstrumentPythonInjectsBootstrapAndRequirements(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "requirements.txt"), "fastapi\nsqlalchemy\npsycopg2\n")
	mustWrite(t, filepath.Join(root, "main.py"), "print('start')\n")

	result, err := Instrument(root, Options{})
	if err != nil {
		t.Fatal(err)
	}

	assertChanged(t, result, "main.py")
	assertFileContains(t, filepath.Join(root, "main.py"), "import extent_instrumentation  # extent:otel")
	assertFileContains(t, filepath.Join(root, "extent_instrumentation.py"), "OTLPSpanExporter")
	assertFileContains(t, filepath.Join(root, "extent_instrumentation.py"), "SQLAlchemyInstrumentor")
	assertFileContains(t, filepath.Join(root, "extent_instrumentation.py"), "RequestsInstrumentor")
	assertFileContains(t, filepath.Join(root, "requirements.txt"), "opentelemetry-exporter-otlp")
	assertFileContains(t, filepath.Join(root, "requirements.txt"), "opentelemetry-instrumentation-sqlalchemy")
	assertFileContains(t, filepath.Join(root, "requirements.txt"), "opentelemetry-instrumentation-psycopg2")
}

func TestInstrumentGoAddsBootstrapWithASTImport(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "go.mod"), "module example.com/payments\n\ngo 1.23\n")
	mustWrite(t, filepath.Join(root, "main.go"), `package main

import "fmt"

func main() {
	fmt.Println("start")
}
`)

	result, err := Instrument(root, Options{})
	if err != nil {
		t.Fatal(err)
	}

	assertChanged(t, result, "main.go")
	assertChanged(t, result, "internal/observability/otel.go")
	assertFileContains(t, filepath.Join(root, "main.go"), `_ "example.com/payments/internal/observability"`)
	assertFileContains(t, filepath.Join(root, "internal/observability/otel.go"), "otlptracehttp")

	second, err := Instrument(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.ChangedFiles) != 0 {
		t.Fatalf("expected idempotent second run, changed %#v", second.ChangedFiles)
	}
}

func TestInstrumentDeepWritesCodemodBundle(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "package.json"), `{"dependencies":{"express":"^4.18.0"}}`)
	mustWrite(t, filepath.Join(root, "server.js"), `const express = require("express");`)

	result, err := Instrument(root, Options{Mode: "deep"})
	if err != nil {
		t.Fatal(err)
	}

	assertChanged(t, result, "extent.codemods/manifest.json")
	assertChanged(t, result, "extent.codemods/node/deep-instrument.mjs")
	assertFileContains(t, filepath.Join(root, "extent.codemods", "node", "deep-instrument.mjs"), "ts-morph")
	assertFileContains(t, filepath.Join(root, "extent.codemods", "python", "deep_instrument.py"), "libcst")
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func assertChanged(t *testing.T, result Result, rel string) {
	t.Helper()
	for _, changed := range result.ChangedFiles {
		if changed == rel {
			return
		}
	}
	t.Fatalf("expected %s in changed files %#v", rel, result.ChangedFiles)
}

func assertFileContains(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), want) {
		t.Fatalf("expected %s to contain %q, got:\n%s", path, want, string(data))
	}
}
