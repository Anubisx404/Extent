package main

import (
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Anubisx404/Extent/internal/config"
)

func TestExitCodeClassifications(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"usage", usageError("bad arguments"), 2},
		{"safety", safetyError("conflict"), 3},
		{"unavailable", unavailableError("missing"), 4},
		{"verification", verificationError("failed"), 5},
		{"internal", errors.New("boom"), 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := exitCode(tc.err); got != tc.want {
				t.Fatalf("exitCode() = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestValidatePositionalArguments(t *testing.T) {
	if err := validatePositional("scan", []string{"repo", "extra"}, 1); err == nil {
		t.Fatal("expected extra positional argument error")
	}
	if err := validatePositional("scan", []string{"repo"}, 1); err != nil {
		t.Fatal(err)
	}
}

func TestValidateEnumsAndRequests(t *testing.T) {
	if err := validateEnum("mode", "invalid", []string{"zero-code", "bootstrap", "deep"}); err == nil {
		t.Fatal("expected invalid mode")
	}
	if err := validateEnum("format", "yaml", []string{"text", "markdown", "html", "json"}); err == nil {
		t.Fatal("expected invalid format")
	}
	if err := validateRequests(0); err == nil {
		t.Fatal("expected zero requests rejection")
	}
	if err := validateRequests(1001); err == nil {
		t.Fatal("expected excessive requests rejection")
	}
}

func TestInstrumentFlagConflicts(t *testing.T) {
	if err := validateInstrumentFlags(true, true, false); err == nil {
		t.Fatal("expected dry-run/apply conflict")
	}
	if err := validateInstrumentFlags(false, true, true); err == nil {
		t.Fatal("expected apply/undo conflict")
	}
	if err := validateInstrumentFlags(true, false, true); err == nil {
		t.Fatal("expected dry-run/undo conflict")
	}
}

func TestRunRejectsInvalidCommandInputs(t *testing.T) {
	tests := []struct {
		name string
		args []string
		code int
	}{
		{name: "unknown command", args: []string{"unknown"}, code: 2},
		{name: "version extras", args: []string{"version", "extra"}, code: 2},
		{name: "scan extras", args: []string{"scan", "one", "two"}, code: 2},
		{name: "analyze extras", args: []string{"analyze", "one", "two"}, code: 2},
		{name: "plan extras", args: []string{"plan", "one", "two"}, code: 2},
		{name: "apply extras", args: []string{"apply", "one", "two"}, code: 2},
		{name: "invalid profile", args: []string{"apply", "--profile", "unknown"}, code: 2},
		{name: "instrument extras", args: []string{"instrument", "one", "two"}, code: 2},
		{name: "invalid mode", args: []string{"instrument", "--mode", "unknown"}, code: 2},
		{name: "instrument conflict", args: []string{"instrument", "--dry-run", "--apply"}, code: 3},
		{name: "deep requires experimental", args: []string{"instrument", "--mode", "deep"}, code: 2},
		{name: "deps extras", args: []string{"deps", "one", "two"}, code: 2},
		{name: "smoke extras", args: []string{"smoke", "--url", "http://127.0.0.1", "extra"}, code: 2},
		{name: "smoke requests low", args: []string{"smoke", "--url", "http://127.0.0.1", "--requests", "0"}, code: 2},
		{name: "smoke missing service", args: []string{"smoke", "--url", "http://127.0.0.1"}, code: 2},
		{name: "report extras", args: []string{"report", "one", "two"}, code: 2},
		{name: "report format", args: []string{"report", "--format", "yaml"}, code: 2},
		{name: "report missing service", args: []string{"report"}, code: 2},
		{name: "baseline extras", args: []string{"baseline", "one", "two"}, code: 2},
		{name: "baseline missing service", args: []string{"baseline"}, code: 2},
		{name: "cardinality extras", args: []string{"cardinality", "one", "two"}, code: 2},
		{name: "score extras", args: []string{"score", "extra"}, code: 2},
		{name: "stack missing action", args: []string{"stack"}, code: 2},
		{name: "stack invalid action", args: []string{"stack", "bogus"}, code: 2},
		{name: "stack extras", args: []string{"stack", "status", "one", "two"}, code: 2},
		{name: "stack timeout zero", args: []string{"stack", "up", "--timeout", "0s"}, code: 2},
		{name: "stack timeout excessive", args: []string{"stack", "up", "--timeout", "31m"}, code: 2},
		{name: "verify extras", args: []string{"verify", "one", "two"}, code: 2},
		{name: "verify requests high", args: []string{"verify", "--requests", "1001"}, code: 2},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := run(test.args)
			if err == nil {
				t.Fatal("expected an error")
			}
			if got := exitCode(err); got != test.code {
				t.Fatalf("exitCode() = %d, want %d (error: %v)", got, test.code, err)
			}
		})
	}
}

func TestEverySubcommandHelpSucceeds(t *testing.T) {
	commands := []string{"scan", "analyze", "plan", "apply", "instrument", "deps", "smoke", "report", "baseline", "cardinality", "score", "stack", "verify", "doctor"}
	for _, command := range commands {
		t.Run(command, func(t *testing.T) {
			if err := run([]string{command, "--help"}); err != nil {
				t.Fatalf("help returned %v (exit %d)", err, exitCode(err))
			}
		})
	}
}

func TestParseFlagsIdentifiesHelp(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	help, err := parseFlags(fs, []string{"--help"})
	if err != nil || !help {
		t.Fatalf("parseFlags help = %v, %v", help, err)
	}
}

func TestVerifyJSONStillReturnsVerificationFailure(t *testing.T) {
	err := run([]string{"verify", "--json", t.TempDir()})
	if err == nil || exitCode(err) != 5 {
		t.Fatalf("verify JSON error = %v, exit = %d", err, exitCode(err))
	}
}

func TestInstrumentDefaultsToPreviewAndRequiresApplyForMutation(t *testing.T) {
	root := t.TempDir()
	packagePath := filepath.Join(root, "package.json")
	serverPath := filepath.Join(root, "server.js")
	packageJSON := []byte(`{"dependencies":{"express":"4.21.2"}}`)
	server := []byte("console.log('start')\n")
	if err := os.WriteFile(packagePath, packageJSON, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(serverPath, server, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"instrument", root}); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(packagePath); err != nil || string(got) != string(packageJSON) {
		t.Fatalf("preview changed package: %q, %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(root, ".extent")); !os.IsNotExist(err) {
		t.Fatalf("preview created transaction state: %v", err)
	}
	if err := run([]string{"instrument", "--apply", root}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "extent.instrumentation.js")); err != nil {
		t.Fatalf("apply did not create bootstrap: %v", err)
	}
}

func TestApplyConsumesValidatedExtentConfigAndPreservesIt(t *testing.T) {
	root := t.TempDir()
	resolved := config.Defaults()
	resolved.Profile.Name = "low-resource"
	resolved.Profile.Recipes = []string{"node/express"}
	resolved.Service.Name = "configured-service"
	configBytes, err := config.Marshal(resolved)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "extent.yaml"), configBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"dependencies":{"express":"4.21.2"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"apply", root}); err != nil {
		t.Fatal(err)
	}
	preserved, err := os.ReadFile(filepath.Join(root, "extent.yaml"))
	if err != nil || string(preserved) != string(configBytes) {
		t.Fatalf("extent.yaml was not preserved: %v\n%s", err, preserved)
	}
	collector, err := os.ReadFile(filepath.Join(root, "otel-collector.yml"))
	if err != nil || !strings.Contains(string(collector), "limit_mib: 256") {
		t.Fatalf("config profile not consumed: %v\n%s", err, collector)
	}
}

func TestApplyRejectsInvalidConfigAndUnknownRecipeBeforeMutation(t *testing.T) {
	for name, document := range map[string][]byte{
		"unknown field":  []byte("version: 1\nunknown: true\n"),
		"unknown recipe": mustConfigBytes(t, "does-not-exist"),
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "extent.yaml"), document, 0o600); err != nil {
				t.Fatal(err)
			}
			err := run([]string{"apply", root})
			if err == nil || exitCode(err) != 2 {
				t.Fatalf("error = %v, exit = %d", err, exitCode(err))
			}
			if _, err := os.Stat(filepath.Join(root, "docker-compose.observability.yml")); !os.IsNotExist(err) {
				t.Fatalf("invalid config mutated project: %v", err)
			}
		})
	}
}

func mustConfigBytes(t *testing.T, recipe string) []byte {
	t.Helper()
	c := config.Defaults()
	c.Profile.Recipes = []string{recipe}
	b, err := config.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
