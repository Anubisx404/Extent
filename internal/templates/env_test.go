package templates

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/Anubisx404/Extent/internal/planner"
	"github.com/Anubisx404/Extent/internal/stack"
)

var grafanaPasswordPattern = regexp.MustCompile(`^[A-Za-z0-9]{24}$`)

func envPassword(t *testing.T, root string) string {
	t.Helper()
	values := stack.ParseEnv(mustRead(t, filepath.Join(root, stack.EnvFile)))
	password := values[stack.GrafanaPasswordKey]
	if !grafanaPasswordPattern.MatchString(password) {
		t.Fatalf("GRAFANA_ADMIN_PASSWORD = %q, want 24 alphanumeric characters", password)
	}
	return password
}

func TestGrafanaPasswordIsGeneratedOnceAndStableAcrossReapply(t *testing.T) {
	root := t.TempDir()
	plan := planner.Plan{Root: root, Detected: []string{"runtime:node"}}
	if _, err := WriteLGTM(root, plan, WriteOptions{}); err != nil {
		t.Fatal(err)
	}
	first := envPassword(t, root)
	firstEnv := mustRead(t, filepath.Join(root, stack.EnvFile))

	// Re-apply with and without Overwrite; neither may churn the password.
	for _, opts := range []WriteOptions{{}, {Overwrite: true}} {
		if _, err := WriteLGTM(root, plan, opts); err != nil {
			t.Fatalf("re-apply %+v: %v", opts, err)
		}
		if got := envPassword(t, root); got != first {
			t.Fatalf("re-apply %+v changed the Grafana password", opts)
		}
	}
	if got := mustRead(t, filepath.Join(root, stack.EnvFile)); got != firstEnv {
		t.Fatalf("re-apply rewrote %s:\n%s", stack.EnvFile, got)
	}
}

func TestGrafanaPasswordDiffersBetweenProjects(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	if _, err := WriteLGTM(a, planner.Plan{Root: a}, WriteOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteLGTM(b, planner.Plan{Root: b}, WriteOptions{}); err != nil {
		t.Fatal(err)
	}
	if envPassword(t, a) == envPassword(t, b) {
		t.Fatal("two projects generated the same Grafana password")
	}
}

func TestUserSetGrafanaPasswordIsNeverReplacedWithoutOverwrite(t *testing.T) {
	root := t.TempDir()
	custom := "OTEL_SERVICE_NAME=mine\nGRAFANA_ADMIN_PASSWORD=my-own-secret\n"
	if err := os.WriteFile(filepath.Join(root, stack.EnvFile), []byte(custom), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteLGTM(root, planner.Plan{Root: root}, WriteOptions{}); err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, filepath.Join(root, stack.EnvFile)); got != custom {
		t.Fatalf("user password or content changed:\n%s", got)
	}
}

func TestLegacyEnvFileGetsPasswordAppendedWithoutLosingContent(t *testing.T) {
	root := t.TempDir()
	legacy := "OTEL_SERVICE_NAME=legacy-app\nOTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318\n"
	if err := os.WriteFile(filepath.Join(root, stack.EnvFile), []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteLGTM(root, planner.Plan{Root: root}, WriteOptions{}); err != nil {
		t.Fatalf("legacy env file without password refused: %v", err)
	}
	got := mustRead(t, filepath.Join(root, stack.EnvFile))
	if !strings.HasPrefix(got, legacy) {
		t.Fatalf("legacy content was not preserved:\n%s", got)
	}
	envPassword(t, root)
}

func TestLegacyEnvFileWithEmptyPasswordLineIsFilledInPlace(t *testing.T) {
	root := t.TempDir()
	legacy := "OTEL_SERVICE_NAME=legacy-app\nGRAFANA_ADMIN_PASSWORD=\n"
	if err := os.WriteFile(filepath.Join(root, stack.EnvFile), []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteLGTM(root, planner.Plan{Root: root}, WriteOptions{}); err != nil {
		t.Fatal(err)
	}
	got := mustRead(t, filepath.Join(root, stack.EnvFile))
	if strings.Count(got, "GRAFANA_ADMIN_PASSWORD=") != 1 || !strings.HasPrefix(got, "OTEL_SERVICE_NAME=legacy-app\n") {
		t.Fatalf("empty password line not filled in place:\n%s", got)
	}
	envPassword(t, root)
}

func TestGeneratedGrafanaPasswordIsNotTheOldDefault(t *testing.T) {
	root := t.TempDir()
	if _, err := WriteLGTM(root, planner.Plan{Root: root}, WriteOptions{}); err != nil {
		t.Fatal(err)
	}
	if envPassword(t, root) == "admin" {
		t.Fatal("generated password is the default admin")
	}
}

func TestEnvObservabilityUsesLocalhostWhenNoAppComposeAndRealServiceName(t *testing.T) {
	hostRoot := t.TempDir()
	hostPlan := planner.Plan{Root: hostRoot, Detected: []string{"runtime:go"}}
	if _, err := WriteLGTM(hostRoot, hostPlan, WriteOptions{}); err != nil {
		t.Fatal(err)
	}
	hostEnv := mustRead(t, filepath.Join(hostRoot, stack.EnvFile))
	if !strings.Contains(hostEnv, "OTEL_SERVICE_NAME="+filepath.Base(hostRoot)) {
		t.Fatalf("expected real service name in env:\n%s", hostEnv)
	}
	if !strings.Contains(hostEnv, "OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318") {
		t.Fatalf("expected localhost OTLP endpoint when no app compose exists:\n%s", hostEnv)
	}

	dockerRoot := t.TempDir()
	dockerPlan := planner.Plan{Root: dockerRoot, Detected: []string{"runtime:go", "docker-compose:docker-compose.yml"}}
	if _, err := WriteLGTM(dockerRoot, dockerPlan, WriteOptions{}); err != nil {
		t.Fatal(err)
	}
	dockerEnv := mustRead(t, filepath.Join(dockerRoot, stack.EnvFile))
	if !strings.Contains(dockerEnv, "OTEL_EXPORTER_OTLP_ENDPOINT=http://otel-collector:4318") {
		t.Fatalf("expected otel-collector OTLP endpoint when app compose exists:\n%s", dockerEnv)
	}
}
