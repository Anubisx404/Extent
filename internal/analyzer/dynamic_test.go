package analyzer

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func requireNode(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not on PATH")
	}
}

func TestDynamicInspectionIsNotRunUnlessRequested(t *testing.T) {
	root := t.TempDir()
	sentinel := filepath.Join(root, "sentinel.txt")
	mustWrite(t, filepath.Join(root, "server.js"), `require("fs").writeFileSync(`+quoteJS(sentinel)+`, "ran");`)

	if _, err := Analyze(root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
		t.Fatalf("entrypoint executed without Dynamic: stat err = %v", err)
	}
}

func TestDynamicCrashBecomesWarning(t *testing.T) {
	requireNode(t)
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "server.js"), `throw new Error("boom at load");`)

	result, err := AnalyzeWithOptions(root, Options{Dynamic: true})
	if err != nil {
		t.Fatalf("dynamic crash must not be an error: %v", err)
	}
	if len(result.Routes) != 0 {
		t.Fatalf("expected no routes, got %#v", result.Routes)
	}
	if !hasDynamicWarning(result, "server.js", "boom at load") {
		t.Fatalf("expected dynamic warning mentioning the crash, got %#v", result.Warnings)
	}
}

func TestDynamicTimeoutBecomesWarning(t *testing.T) {
	requireNode(t)
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "server.js"), `setInterval(() => {}, 1000);`)

	result, err := AnalyzeWithOptions(root, Options{Dynamic: true})
	if err != nil {
		t.Fatalf("dynamic timeout must not be an error: %v", err)
	}
	if !hasDynamicWarning(result, "server.js", "timed out") {
		t.Fatalf("expected timeout warning, got %#v", result.Warnings)
	}
}

func TestDynamicInspectionReadsRoutesWithMinimalEnvironment(t *testing.T) {
	requireNode(t)
	if runtime.GOOS == "windows" {
		t.Skip("minimal environment is enforced only on Unix-like systems")
	}
	t.Setenv("EXTENT_ANALYSIS_SECRET", "leaked")
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "server.js"), `module.exports = {_router: {stack: [
  {route: {path: process.env.NODE_ENV || "missing-node-env", methods: {get: true}}},
  {route: {path: process.env.EXTENT_ANALYSIS_SECRET || "absent", methods: {post: true}}}
]}};`)

	result, err := AnalyzeWithOptions(root, Options{Dynamic: true})
	if err != nil {
		t.Fatal(err)
	}
	var sawNodeEnv, sawSecret bool
	for _, r := range result.Routes {
		if r.Path == "extent-analysis" && r.Method == "GET" {
			sawNodeEnv = true
		}
		if r.Path == "leaked" {
			sawSecret = true
		}
	}
	if !sawNodeEnv {
		t.Fatalf("expected NODE_ENV=extent-analysis in child, routes = %#v, warnings = %#v", result.Routes, result.Warnings)
	}
	if sawSecret {
		t.Fatalf("inherited environment leaked into dynamic process: %#v", result.Routes)
	}
}

func hasDynamicWarning(result Result, path, substring string) bool {
	for _, w := range result.Warnings {
		if w.Kind == "dynamic" && w.Path == path && strings.Contains(w.Detail, substring) {
			return true
		}
	}
	return false
}

func quoteJS(s string) string {
	return `"` + strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `"`, `\"`) + `"`
}
