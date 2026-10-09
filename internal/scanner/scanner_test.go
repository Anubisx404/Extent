package scanner

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScanDetectsNodeExpressAndCompose(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "package.json"), `{"dependencies":{"express":"^4.0.0","pg":"^8.0.0"}}`)
	mustWrite(t, filepath.Join(root, "package-lock.json"), `{"lockfileVersion":3}`)
	mustWrite(t, filepath.Join(root, "docker-compose.yml"), "services: {}\n")
	mustWrite(t, filepath.Join(root, "server.js"), "console.log('ok')\n")

	result, err := Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, result.Runtimes, "node")
	assertContains(t, result.Frameworks, "express")
	assertContains(t, result.PackageManagers, "npm")
	assertContains(t, result.DatabaseLibraries, "postgres")
	assertContains(t, result.ComposeFiles, "docker-compose.yml")
	assertContains(t, result.Entrypoints, "server.js")
}

func TestScanDetectsPythonDatabaseLibraries(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "requirements.txt"), "fastapi\nsqlalchemy\nredis\n")

	result, err := Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, result.DatabaseLibraries, "sqlalchemy")
	assertContains(t, result.DatabaseLibraries, "redis")
}

func TestScanDetectsDotnetSlnx(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "MyApp.slnx"), `<Solution><Project Path="src/WebApi/WebApi.csproj" /></Solution>`)

	result, err := Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, result.Runtimes, "dotnet")
	assertContains(t, result.PackageManagers, "dotnet")
}

func TestScanDetectsAppSettingsAndHttpAndLaunchSettings(t *testing.T) {
	root := t.TempDir()
	appsettings := `{
  "ConnectionStrings": {
    "DefaultConnection": "Server=localhost,1433;Database=OrderDb;Trusted_Connection=True;",
    "Cache": "localhost:6379,abortConnect=false"
  },
  "Urls": "http://localhost:5000;https://localhost:5001",
  "OpenTelemetry": {
    "Endpoint": "http://localhost:4318"
  }
}`
	httpFile := `@WebApi_HostAddress = http://localhost:5142

GET {{WebApi_HostAddress}}/weatherforecast/
Accept: application/json

###

POST http://localhost:5142/api/orders
Content-Type: application/json
`
	launchSettings := `{
  "profiles": {
    "http": {
      "commandName": "Project",
      "applicationUrl": "http://localhost:5142"
    }
  }
}`
	mustWrite(t, filepath.Join(root, "appsettings.json"), appsettings)
	mustWrite(t, filepath.Join(root, "WebApi.http"), httpFile)
	mustWrite(t, filepath.Join(root, "Properties", "launchSettings.json"), launchSettings)

	result, err := Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, result.DatabaseLibraries, "sqlserver")
	assertContains(t, result.DatabaseLibraries, "redis")

	hasConnStr := false
	hasOtel := false
	hasAppUrl := false
	hasHttpHost := false
	hasHttpEndpoint := false
	hasDevUrl := false

	for _, s := range result.Signals {
		if s.Kind == "connection-string" && s.Value == "DefaultConnection" {
			hasConnStr = true
		}
		if s.Kind == "otel-config" {
			hasOtel = true
		}
		if s.Kind == "app-url" && s.Value == "http://localhost:5000" {
			hasAppUrl = true
		}
		if s.Kind == "http-host" && s.Value == "http://localhost:5142" {
			hasHttpHost = true
		}
		if s.Kind == "http-endpoint" && s.Value == "GET {{WebApi_HostAddress}}/weatherforecast/" {
			hasHttpEndpoint = true
		}
		if s.Kind == "dev-url" && s.Value == "http://localhost:5142" {
			hasDevUrl = true
		}
	}

	if !hasConnStr || !hasOtel || !hasAppUrl || !hasHttpHost || !hasHttpEndpoint || !hasDevUrl {
		t.Fatalf("missing expected signals: %#v", result.Signals)
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

func TestScanDetectsGoNetHTTPWithoutRouterFramework(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "go.mod"), "module example.com/shop\n\ngo 1.22\n")
	mustWrite(t, filepath.Join(root, "main.go"), `package main

import (
	"net/http"
)

func main() {
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {})
	_ = http.ListenAndServe(":8080", nil)
}
`)

	result, err := Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, result.Frameworks, "nethttp")
	assertContains(t, result.Entrypoints, "main.go")
}

func TestScanNetHTTPIgnoresTestOnlyUsageAndYieldsToRouters(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "go.mod"), "module example.com/shop\n\ngo 1.22\n\nrequire github.com/go-chi/chi/v5 v5.0.0\n")
	mustWrite(t, filepath.Join(root, "handler_test.go"), `package main

import "net/http"

func helper() { _ = http.HandleFunc }
`)
	mustWrite(t, filepath.Join(root, "main.go"), `package main

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

func main() {
	r := chi.NewRouter()
	http.Handle("/", r)
}
`)

	result, err := Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, result.Frameworks, "chi")
	for _, f := range result.Frameworks {
		if f == "nethttp" {
			t.Fatalf("nethttp should not be reported alongside chi: %#v", result.Frameworks)
		}
	}
}

func TestScanNetHTTPSkipsTestOnlyFiles(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "go.mod"), "module example.com/shop\n\ngo 1.22\n")
	mustWrite(t, filepath.Join(root, "server_test.go"), `package main

import (
	"net/http"
	"testing"
)

func TestHealth(t *testing.T) {
	http.HandleFunc("/health", nil)
}
`)

	result, err := Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range result.Frameworks {
		if f == "nethttp" {
			t.Fatalf("test-only usage detected as nethttp: %#v", result.Frameworks)
		}
	}
}
