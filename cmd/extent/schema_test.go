package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
)

// schemaDir is the repository-root directory holding the JSON Schema files,
// relative to this package directory (go test runs in cmd/extent).
const schemaDir = "../../schemas"

// payloadSchema is the subset of a draft 2020-12 schema the tests check: the
// const value of the "schema" property and the top-level required list.
type payloadSchema struct {
	Properties map[string]json.RawMessage `json:"properties"`
	Required   []string                   `json:"required"`
}

func loadPayloadSchema(t *testing.T, file string) (payloadSchema, string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(schemaDir, file))
	if err != nil {
		t.Fatalf("read schema %s: %v", file, err)
	}
	var doc payloadSchema
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse schema %s: %v", file, err)
	}
	var schemaProp struct {
		Const string `json:"const"`
	}
	if err := json.Unmarshal(doc.Properties["schema"], &schemaProp); err != nil || schemaProp.Const == "" {
		t.Fatalf("schema %s has no const for the \"schema\" property", file)
	}
	if !slices.Contains(doc.Required, "schema") {
		t.Fatalf("schema %s does not require the \"schema\" property", file)
	}
	return doc, schemaProp.Const
}

// writeFakeDocker installs a docker executable that answers the preflight and
// `compose ps --format json` calls used by `extent stack status`, so the stack
// command can be exercised without a Docker daemon.
func writeFakeDocker(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake docker is a POSIX shell script")
	}
	dir := t.TempDir()
	script := `#!/bin/sh
case "$*" in
  --version*) echo "Docker version 0.0.0" ;;
  "compose version"*) echo "Docker Compose version v2.0.0" ;;
  info*) echo '"0.0.0"' ;;
  *" ps --format json"*) echo '[{"Name":"checkout-app-1","Service":"app","State":"running","Health":"healthy","ExitCode":0}]' ;;
  *) echo "unexpected docker call: $*" >&2; exit 1 ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestVersionJSONHasExpectedKeys(t *testing.T) {
	out, _, err := captureStreams(t, func() error { return run([]string{"version", "--json"}) })
	if err != nil {
		t.Fatalf("version --json: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("version --json is not an object: %v\n%s", err, out)
	}
	for _, key := range []string{"schema", "version", "commit", "date", "go", "os", "arch"} {
		if _, ok := payload[key]; !ok {
			t.Errorf("version --json missing key %q: %s", key, out)
		}
	}
	if payload["schema"] != "extent.version/v1" {
		t.Errorf("schema = %v, want extent.version/v1", payload["schema"])
	}
	if len(payload) != 7 {
		t.Errorf("version --json has %d keys, want exactly 7: %s", len(payload), out)
	}
}

func TestWriteJSONSchemaPreservesFieldOrderAndEmptyObjects(t *testing.T) {
	type payload struct {
		Root  string   `json:"root"`
		Items []string `json:"items"`
	}
	out, _, err := captureStreams(t, func() error {
		return writeJSONSchema("extent.test/v1", payload{Root: "r", Items: nil})
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "{\n  \"schema\": \"extent.test/v1\",\n  \"root\": \"r\",\n  \"items\": null\n}\n"
	if out != want {
		t.Fatalf("output =\n%s\nwant\n%s", out, want)
	}
	out, _, err = captureStreams(t, func() error {
		return writeJSONSchema("extent.test/v1", struct{}{})
	})
	if err != nil || out != "{\n  \"schema\": \"extent.test/v1\"\n}\n" {
		t.Fatalf("empty object: %q, %v", out, err)
	}
	if _, _, err := captureStreams(t, func() error { return writeJSONSchema("extent.test/v1", []int{1}) }); err == nil {
		t.Fatal("non-object payload accepted")
	}
}

// jsonSchemaCase describes one --json command run against an offline fixture.
type jsonSchemaCase struct {
	name       string
	schemaFile string
	setup      func(t *testing.T) []string
	// allowed lists the exit codes the command may return. Commands that report
	// failed verification still print their JSON first, so exit 5 is accepted
	// where the fixture's backends are deliberately unreachable.
	allowed []int
}

func TestJSONPayloadsMatchSchemas(t *testing.T) {
	cases := []jsonSchemaCase{
		{
			name:       "version",
			schemaFile: "version.v1.json",
			setup:      func(t *testing.T) []string { return []string{"version", "--json"} },
			allowed:    []int{0},
		},
		{
			name:       "scan",
			schemaFile: "scan.v1.json",
			setup:      func(t *testing.T) []string { return []string{"scan", "--json", writeFixtureProject(t)} },
			allowed:    []int{0},
		},
		{
			name:       "analyze",
			schemaFile: "analyze.v1.json",
			setup:      func(t *testing.T) []string { return []string{"analyze", "--json", writeFixtureProject(t)} },
			allowed:    []int{0},
		},
		{
			name:       "plan",
			schemaFile: "plan.v1.json",
			setup:      func(t *testing.T) []string { return []string{"plan", "--json", writeFixtureProject(t)} },
			allowed:    []int{0},
		},
		{
			name:       "instrument",
			schemaFile: "instrument.v1.json",
			setup:      func(t *testing.T) []string { return []string{"instrument", "--json", writeFixtureProject(t)} },
			allowed:    []int{0},
		},
		{
			name:       "cardinality",
			schemaFile: "cardinality.v1.json",
			setup:      func(t *testing.T) []string { return []string{"cardinality", "--json", writeFixtureProject(t)} },
			allowed:    []int{0},
		},
		{
			name:       "score",
			schemaFile: "score.v1.json",
			setup: func(t *testing.T) []string {
				return []string{"score", "--json", "--prometheus", unreachableURL(t)}
			},
			allowed: []int{0},
		},
		{
			name:       "stack-status",
			schemaFile: "stack-status.v1.json",
			setup: func(t *testing.T) []string {
				writeFakeDocker(t)
				root := writeFixtureProject(t)
				if err := os.WriteFile(filepath.Join(root, "docker-compose.observability.yml"), []byte("services: {}\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				return []string{"stack", "status", "--json", root}
			},
			allowed: []int{0},
		},
		{
			name:       "verify",
			schemaFile: "verify.v1.json",
			setup: func(t *testing.T) []string {
				backend := unreachableURL(t)
				return []string{"verify", "--json", "--prometheus", backend, "--loki", backend, "--tempo", backend, writeFixtureProject(t)}
			},
			allowed: []int{5},
		},
		{
			name:       "doctor",
			schemaFile: "doctor.v1.json",
			setup: func(t *testing.T) []string {
				backend := unreachableURL(t)
				return []string{"doctor", "--json", "--prometheus", backend, "--loki", backend, "--tempo", backend, writeFixtureProject(t)}
			},
			allowed: []int{5},
		},
		{
			name:       "report",
			schemaFile: "report.v1.json",
			setup: func(t *testing.T) []string {
				return []string{"report", "--json", "--service", "checkout", "--prometheus", unreachableURL(t), writeFixtureProject(t)}
			},
			allowed: []int{5},
		},
		{
			name:       "baseline",
			schemaFile: "baseline.v1.json",
			setup: func(t *testing.T) []string {
				stub := evidenceStub(t)
				return []string{"baseline", "--json", "--service", "checkout", "--prometheus", stub, "--loki", stub, "--tempo", stub, writeFixtureProject(t)}
			},
			allowed: []int{0},
		},
		{
			name:       "smoke",
			schemaFile: "smoke.v1.json",
			setup: func(t *testing.T) []string {
				// smoke waits out its telemetry settle timeout (15s) when the
				// backends are unreachable, so it is skipped in -short runs.
				if testing.Short() {
					t.Skip("smoke waits for its settle timeout; skipped in -short")
				}
				app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusOK)
				}))
				t.Cleanup(app.Close)
				backend := unreachableURL(t)
				return []string{"smoke", "--json", "--url", app.URL, "--service", "checkout", "--requests", "1",
					"--prometheus", backend, "--tempo", backend, "--loki", backend}
			},
			allowed: []int{5},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			schema, wantName := loadPayloadSchema(t, tc.schemaFile)
			args := tc.setup(t)
			out, _, err := captureStreams(t, func() error { return run(args) })
			code := 0
			if err != nil {
				code = exitCode(err)
			}
			if !slices.Contains(tc.allowed, code) {
				t.Fatalf("%v exit = %d (error %v), want one of %v", args, code, err, tc.allowed)
			}
			var payload map[string]json.RawMessage
			if err := json.Unmarshal([]byte(out), &payload); err != nil {
				t.Fatalf("%v stdout is not a JSON object: %v\n%s", args, err, out)
			}
			var got string
			if err := json.Unmarshal(payload["schema"], &got); err != nil || got != wantName {
				t.Fatalf("schema = %q (%v), want %q", got, err, wantName)
			}
			for _, key := range schema.Required {
				if _, ok := payload[key]; !ok {
					t.Errorf("payload missing required property %q (schema %s)", key, tc.schemaFile)
				}
			}
		})
	}
}
