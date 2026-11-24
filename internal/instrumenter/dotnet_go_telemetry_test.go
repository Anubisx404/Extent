package instrumenter

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDotnetBootstrapIncludesOTLPLoggingIdempotencyAndUndo(t *testing.T) {
	root := t.TempDir()
	csprojBefore := "<Project Sdk=\"Microsoft.NET.Sdk.Web\">\n  <PropertyGroup>\n    <TargetFramework>net8.0</TargetFramework>\n  </PropertyGroup>\n</Project>\n"
	programBefore := "var builder = WebApplication.CreateBuilder(args);\nvar app = builder.Build();\napp.MapGet(\"/\", () => \"ok\");\napp.Run();\n"
	mustWrite(t, filepath.Join(root, "App.csproj"), csprojBefore)
	mustWrite(t, filepath.Join(root, "Program.cs"), programBefore)

	res1, err := Instrument(root, Options{Mode: "bootstrap"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res1.ChangedFiles) == 0 {
		t.Fatal("expected changed files on first instrumentation")
	}

	extPath := filepath.Join(root, "ExtentObservabilityExtensions.cs")
	assertFileContains(t, extPath, "using OpenTelemetry.Logs;")
	assertFileContains(t, extPath, ".WithLogging(")
	assertFileContains(t, extPath, "/v1/logs")
	assertFileContains(t, extPath, "OpenTelemetryLoggerOptions")
	assertFileContains(t, extPath, "http.request.header.x_request_id")

	res2, err := Instrument(root, Options{Mode: "bootstrap"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res2.ChangedFiles) != 0 {
		t.Fatalf("expected idempotent second run, got changed files: %v", res2.ChangedFiles)
	}

	if _, err := Instrument(root, Options{Mode: "bootstrap", Undo: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(extPath); !os.IsNotExist(err) {
		t.Fatalf("expected %s to be removed on undo", extPath)
	}
	csprojAfterUndo, err := os.ReadFile(filepath.Join(root, "App.csproj"))
	if err != nil {
		t.Fatal(err)
	}
	if string(csprojAfterUndo) != csprojBefore {
		t.Fatalf("csproj not restored on undo:\ngot:\n%s\nwant:\n%s", csprojAfterUndo, csprojBefore)
	}
	programAfterUndo, err := os.ReadFile(filepath.Join(root, "Program.cs"))
	if err != nil {
		t.Fatal(err)
	}
	if string(programAfterUndo) != programBefore {
		t.Fatalf("Program.cs not restored on undo:\ngot:\n%s\nwant:\n%s", programAfterUndo, programBefore)
	}
}

func TestGoBootstrapIncludesLogsHttpWrapAndMetricInterval(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "go.mod"), "module example.com/orders\n\ngo 1.22\n")
	mustWrite(t, filepath.Join(root, "main.go"), "package main\n\nfunc main() {}\n")

	if _, err := Instrument(root, Options{Mode: "bootstrap"}); err != nil {
		t.Fatal(err)
	}

	otelPath := filepath.Join(root, "internal/observability/otel.go")
	assertFileContains(t, otelPath, "otlploghttp")
	assertFileContains(t, otelPath, "otelslog")
	assertFileContains(t, otelPath, "otelhttp")
	assertFileContains(t, otelPath, "WrapHandler")
	assertFileContains(t, otelPath, "loggerProvider.Shutdown(ctx)")
	assertFileContains(t, otelPath, "OTEL_METRIC_EXPORT_INTERVAL")
}

func TestGoASTRewritesListenAndServeIdempotentAndUndo(t *testing.T) {
	root := t.TempDir()
	goModBefore := "module example.com/web\n\ngo 1.22\n"
	mainBefore := "package main\n\nimport (\n\t\"net/http\"\n\t\"os\"\n)\n\nfunc main() {\n\tport := os.Getenv(\"PORT\")\n\t_ = http.ListenAndServe(\":\"+port, nil)\n}\n"
	mustWrite(t, filepath.Join(root, "go.mod"), goModBefore)
	mustWrite(t, filepath.Join(root, "main.go"), mainBefore)

	res1, err := Instrument(root, Options{Mode: "bootstrap"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res1.ChangedFiles) == 0 {
		t.Fatal("expected changed files on first instrumentation")
	}

	mainPath := filepath.Join(root, "main.go")
	assertFileContains(t, mainPath, `defer extentotel.Shutdown()`)
	assertFileContains(t, mainPath, `extentotel.WrapHandler(nil, "http.server")`)

	mainAfterFirst, err := os.ReadFile(mainPath)
	if err != nil {
		t.Fatal(err)
	}

	res2, err := Instrument(root, Options{Mode: "bootstrap"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res2.ChangedFiles) != 0 {
		t.Fatalf("expected idempotent second run, got changed files: %v", res2.ChangedFiles)
	}
	mainAfterSecond, err := os.ReadFile(mainPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(mainAfterSecond) != string(mainAfterFirst) {
		t.Fatalf("main.go changed on second run:\nbefore:\n%s\nafter:\n%s", mainAfterFirst, mainAfterSecond)
	}

	if _, err := Instrument(root, Options{Mode: "bootstrap", Undo: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "internal/observability/otel.go")); !os.IsNotExist(err) {
		t.Fatal("expected internal/observability/otel.go to be removed on undo")
	}
	mainAfterUndo, err := os.ReadFile(mainPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(mainAfterUndo) != mainBefore {
		t.Fatalf("main.go not restored on undo:\ngot:\n%s\nwant:\n%s", mainAfterUndo, mainBefore)
	}
	goModAfterUndo, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	if string(goModAfterUndo) != goModBefore {
		t.Fatalf("go.mod not restored on undo:\ngot:\n%s\nwant:\n%s", goModAfterUndo, goModBefore)
	}
}

func TestGoInstrumentedNetHTTPPassesModTidyAndBuild(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "go.mod"), "module example.com/httpapp\n\ngo 1.22\n")
	mustWrite(t, filepath.Join(root, "main.go"), "package main\n\nimport (\n\t\"net/http\"\n)\n\nfunc main() {\n\thttp.HandleFunc(\"/health\", func(w http.ResponseWriter, r *http.Request) {\n\t\tw.WriteHeader(http.StatusOK)\n\t})\n\t_ = http.ListenAndServe(\":8080\", nil)\n}\n")

	if _, err := Instrument(root, Options{Mode: "bootstrap"}); err != nil {
		t.Fatal(err)
	}

	tidyCmd := exec.Command("go", "mod", "tidy")
	tidyCmd.Dir = root
	if out, err := tidyCmd.CombinedOutput(); err != nil {
		t.Fatalf("go mod tidy failed: %v\n%s", err, out)
	}

	buildCmd := exec.Command("go", "build", "./...")
	buildCmd.Dir = root
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("go build ./... failed: %v\n%s", err, out)
	}
}

func TestIsPythonProjectDetectsPyprojectAndManagePy(t *testing.T) {
	pyprojectRoot := t.TempDir()
	mustWrite(t, filepath.Join(pyprojectRoot, "pyproject.toml"), "[project]\nname = \"svc\"\n")
	if !isPythonProject(pyprojectRoot) {
		t.Fatal("expected pyproject.toml to be detected as Python project")
	}
	if !supportedRoot(pyprojectRoot) {
		t.Fatal("expected pyproject.toml root to be supported")
	}
	if !strings.Contains(zeroCodeContent(pyprojectRoot), "OTEL_PYTHON_LOG_CORRELATION=true") {
		t.Fatal("expected zeroCodeContent to include Python instructions for pyproject.toml")
	}

	manageRoot := t.TempDir()
	mustWrite(t, filepath.Join(manageRoot, "manage.py"), "print('django')\n")
	if !isPythonProject(manageRoot) {
		t.Fatal("expected manage.py to be detected as Python project")
	}
	if !supportedRoot(manageRoot) {
		t.Fatal("expected manage.py root to be supported")
	}
	if !strings.Contains(zeroCodeContent(manageRoot), "OTEL_PYTHON_LOG_CORRELATION=true") {
		t.Fatal("expected zeroCodeContent to include Python instructions for manage.py")
	}
}
