package analyzer

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAnalyzeDetectsRoutesQueuesLoggersAndTestCommands(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "package.json"), `{
  "scripts": {"test": "vitest"},
  "dependencies": {
    "express": "^4.18.0",
    "pino": "^9.0.0",
    "bullmq": "^5.0.0",
    "axios": "^1.0.0",
    "pg": "^8.0.0"
  }
}`)
	mustWrite(t, filepath.Join(root, "server.js"), `
const express = require("express");
const app = express();
app.get("/health", handler);
app.post("/checkout", handler);
`)
	mustWrite(t, filepath.Join(root, "docker-compose.yml"), `
services:
  api:
    ports:
      - "8080:8080"
    env_file:
      - .env
`)

	result, err := Analyze(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Routes) != 2 {
		t.Fatalf("expected 2 routes, got %#v", result.Routes)
	}
	assertContains(t, result.Loggers, "pino")
	assertContains(t, result.Queues, "bullmq")
	assertContains(t, result.ExternalHTTPClients, "axios")
	assertContains(t, result.DatabaseLibraries, "postgres")
	assertContains(t, result.TestCommands, "npm test")
	if len(result.Docker.Ports) == 0 {
		t.Fatalf("expected docker port detection")
	}
}

func TestAnalyzePropagatesScanWarnings(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "package.json"), `{not-json`)
	result, err := Analyze(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Warnings) != 1 || result.Warnings[0].Path != "package.json" {
		t.Fatalf("warnings = %#v", result.Warnings)
	}
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

func assertContains(t *testing.T, values []string, want string) {
	t.Helper()
	for _, value := range values {
		if value == want {
			return
		}
	}
	t.Fatalf("expected %q in %#v", want, values)
}
