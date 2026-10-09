package stack

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Anubisx404/Extent/internal/process"
)

// TestMain makes the readiness probe a no-op by default so unit tests never touch
// the host's real Loki/Tempo ports. Readiness tests stub it explicitly.
func TestMain(m *testing.M) {
	readinessProbe = func(context.Context, string) error { return nil }
	os.Exit(m.Run())
}

// stubReadiness replaces the live /ready probe for the duration of a test.
func stubReadiness(t *testing.T, probe func(context.Context, string) error) {
	t.Helper()
	previous := readinessProbe
	readinessProbe = probe
	t.Cleanup(func() { readinessProbe = previous })
}

func TestUpWaitsForLokiAndTempoReadiness(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, composeFile), []byte("services: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var probed []string
	stubReadiness(t, func(_ context.Context, url string) error {
		probed = append(probed, url)
		return nil
	})
	if err := UpContext(context.Background(), &scriptedRunner{results: map[string]process.Result{}}, root); err != nil {
		t.Fatal(err)
	}
	if len(probed) != 2 || !strings.Contains(probed[0], ":3100/ready") || !strings.Contains(probed[1], ":3200/ready") {
		t.Fatalf("readiness probed %v, want Loki then Tempo", probed)
	}
}

func TestUpReportsComponentThatNeverBecomesReady(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, composeFile), []byte("services: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stubReadiness(t, func(ctx context.Context, url string) error {
		if strings.Contains(url, ":3200/") {
			return errors.New("status 503")
		}
		return nil
	})
	err := UpWithOptions(context.Background(), &scriptedRunner{results: map[string]process.Result{}}, root, UpOptions{Wait: true, Timeout: 50 * time.Millisecond})
	if err == nil || !strings.Contains(err.Error(), "Tempo did not become ready") {
		t.Fatalf("err = %v, want Tempo readiness failure", err)
	}
}

func TestUpPassesEnvFileWhenPresent(t *testing.T) {
	stubReadiness(t, func(context.Context, string) error { return nil })
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, composeFile), []byte("services: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, EnvFile), []byte("GRAFANA_ADMIN_PASSWORD=abc123\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := &scriptedRunner{results: map[string]process.Result{}}
	if err := UpContext(context.Background(), runner, root); err != nil {
		t.Fatal(err)
	}
	want := "docker compose -f " + composeFile + " --env-file " + EnvFile + " -p " + projectName(root)
	calls := strings.Join(runner.calls, "\n")
	for _, call := range []string{want + " config --quiet", want + " up -d --force-recreate --wait --wait-timeout 120"} {
		if !strings.Contains(calls, call) {
			t.Fatalf("missing %q in:\n%s", call, calls)
		}
	}
}

func TestComposeArgsOmitEnvFileWhenAbsent(t *testing.T) {
	root := t.TempDir()
	got := strings.Join(composeArgs(root, "p"), " ")
	if strings.Contains(got, "--env-file") {
		t.Fatalf("env file passed although %s is absent: %s", EnvFile, got)
	}
}

func TestGrafanaHintNamesUserAndFileButNeverThePassword(t *testing.T) {
	root := t.TempDir()
	const secret = "s3cret-generated-value"
	if err := os.WriteFile(filepath.Join(root, EnvFile), []byte("GRAFANA_ADMIN_USER=admin\nGRAFANA_ADMIN_PASSWORD="+secret+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	hint := GrafanaHint(root)
	want := "Grafana: http://localhost:3000 (user admin, password in .env.observability)"
	if hint != want {
		t.Fatalf("hint = %q, want %q", hint, want)
	}
	if strings.Contains(hint, secret) {
		t.Fatal("hint leaked the password")
	}
}

func TestParseEnvIgnoresCommentsAndKeepsLastValue(t *testing.T) {
	values := ParseEnv("# comment\n\nA=1\nA=2\nB = spaced \nnoequals\n")
	if values["A"] != "2" || values["B"] != "spaced" {
		t.Fatalf("values = %#v", values)
	}
	if _, ok := values["noequals"]; ok {
		t.Fatal("line without = was parsed")
	}
}
