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

func TestAnalyzeMultiFileRouterProject(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "package.json"), `{
  "name": "ecommerce-api",
  "dependencies": {
    "express": "^4.19.2",
    "pg": "^8.11.5",
    "redis": "^4.6.13",
    "pino": "^9.0.0",
    "axios": "^1.6.8",
    "bullmq": "^5.7.1"
  }
}`)
	mustWrite(t, filepath.Join(root, "server.js"), `
const express = require('express');
const app = express();
const productsRouter = require('./routes/products');
app.use('/api/products', productsRouter);
`)
	mustWrite(t, filepath.Join(root, "routes", "products.js"), `
const express = require('express');
const router = express.Router();
router.get('/', (req, res) => {});
router.get('/:id', (req, res) => {});
router.post('/', (req, res) => {});
module.exports = router;
`)

	mustWrite(t, filepath.Join(root, "docker-compose.yml"), `
version: '3.8'
services:
  api:
    ports:
      - "3000:3000"
    environment:
      - PORT=3000
    networks:
      - ecommerce-net
networks:
  ecommerce-net:
    driver: bridge
`)

	result, err := Analyze(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Routes) != 3 {
		t.Fatalf("expected 3 routes, got %#v", result.Routes)
	}
	routePaths := map[string]bool{}
	for _, r := range result.Routes {
		routePaths[r.Method+" "+r.Path] = true
	}
	if !routePaths["GET /api/products"] {
		t.Fatalf("expected route GET /api/products, got %#v", routePaths)
	}
	if !routePaths["GET /api/products/:id"] {
		t.Fatalf("expected route GET /api/products/:id, got %#v", routePaths)
	}
	if !routePaths["POST /api/products"] {
		t.Fatalf("expected route POST /api/products, got %#v", routePaths)
	}
	assertContains(t, result.Loggers, "pino")
	assertContains(t, result.Queues, "bullmq")
	assertContains(t, result.ExternalHTTPClients, "axios")
	assertContains(t, result.DatabaseLibraries, "postgres")
	assertContains(t, result.DatabaseLibraries, "redis")
	if len(result.Docker.Networks) != 1 || result.Docker.Networks[0] != "ecommerce-net" {
		t.Fatalf("expected exactly [ecommerce-net] network, got %#v", result.Docker.Networks)
	}
}
