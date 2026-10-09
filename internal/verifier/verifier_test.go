package verifier

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestVerifyChecksGrafanaCorrelationConfig(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{
		"docker-compose.observability.yml",
		"otel-collector.yml",
		"tempo.yml",
		"loki.yml",
		"prometheus.yml",
		"prometheus-alerts.yml",
		"extent.yaml",
		".env.observability",
		"grafana/dashboards/service-overview.json",
	} {
		mustWrite(t, filepath.Join(root, filepath.FromSlash(rel)), "ok")
	}
	mustWrite(t, filepath.Join(root, "grafana", "provisioning", "datasources", "datasources.yml"), `
datasources:
  - name: Tempo
    jsonData:
      tracesToLogsV2:
        datasourceUid: loki
        filterByTraceID: true
        filterBySpanID: true
      tracesToMetrics:
        datasourceUid: prometheus
      serviceMap:
        datasourceUid: prometheus
`)

	report := VerifyConfig(Config{Root: root})

	if !hasCheck(report, "Grafana trace/log/metric correlation", true) {
		t.Fatalf("expected Grafana correlation check to pass, got %#v", report.Checks)
	}
}

func TestVerifyChecksLiveGrafanaTempoDatasource(t *testing.T) {
	grafana := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/datasources/uid/tempo" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Write([]byte(`{"uid":"tempo","jsonData":{"tracesToLogsV2":{"datasourceUid":"loki","filterByTraceID":true,"filterBySpanID":true},"tracesToMetrics":{"datasourceUid":"prometheus"},"serviceMap":{"datasourceUid":"prometheus"}}}`))
	}))
	defer grafana.Close()

	report := VerifyConfig(Config{Root: t.TempDir(), GrafanaURL: grafana.URL})

	if !hasCheck(report, "Grafana API Tempo correlation", true) {
		t.Fatalf("expected live Grafana API correlation check to pass, got %#v", report.Checks)
	}
}

func TestGrafanaAPIAuthReadsStackEnvFile(t *testing.T) {
	clearGrafanaEnv(t)
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, ".env.observability"), "GRAFANA_ADMIN_USER=admin\nGRAFANA_ADMIN_PASSWORD=generated-pw\n")
	got := runGrafanaAuthCheckIn(t, root, Config{})
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("admin:generated-pw"))
	if got != want {
		t.Fatalf("Authorization = %q, want %q", got, want)
	}
}

func TestGrafanaAPIAuthNeverDefaultsToAdminPassword(t *testing.T) {
	clearGrafanaEnv(t)
	grafana := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Errorf("sent credentials %q without any configured password", r.Header.Get("Authorization"))
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer grafana.Close()
	report := VerifyConfig(Config{Root: t.TempDir(), GrafanaURL: grafana.URL})
	if !hasCheck(report, "Grafana API Tempo correlation", false) {
		t.Fatalf("expected unauthenticated Grafana check to fail, got %#v", report.Checks)
	}
}

func TestGrafanaAPIAuthFlagsOverrideStackEnvFile(t *testing.T) {
	clearGrafanaEnv(t)
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, ".env.observability"), "GRAFANA_ADMIN_PASSWORD=generated-pw\n")
	got := runGrafanaAuthCheckIn(t, root, Config{GrafanaUser: "ops", GrafanaPassword: "pw"})
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("ops:pw"))
	if got != want {
		t.Fatalf("Authorization = %q, want %q", got, want)
	}
}

func TestGrafanaAPIAuthUsesBearerWhenTokenSet(t *testing.T) {
	clearGrafanaEnv(t)
	got := runGrafanaAuthCheck(t, Config{GrafanaToken: "secret-token", GrafanaUser: "ignored", GrafanaPassword: "ignored"})
	if got != "Bearer secret-token" {
		t.Fatalf("Authorization = %q, want Bearer secret-token", got)
	}
}

func TestGrafanaAPIAuthUsesConfiguredBasicCredentials(t *testing.T) {
	clearGrafanaEnv(t)
	got := runGrafanaAuthCheck(t, Config{GrafanaUser: "ops", GrafanaPassword: "pw"})
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("ops:pw"))
	if got != want {
		t.Fatalf("Authorization = %q, want %q", got, want)
	}
}

func TestGrafanaAPIAuthFallsBackToEnvironment(t *testing.T) {
	clearGrafanaEnv(t)
	t.Setenv("EXTENT_GRAFANA_TOKEN", "env-token")
	if got := runGrafanaAuthCheck(t, Config{}); got != "Bearer env-token" {
		t.Fatalf("Authorization = %q, want Bearer env-token", got)
	}

	clearGrafanaEnv(t)
	t.Setenv("EXTENT_GRAFANA_USER", "envuser")
	t.Setenv("EXTENT_GRAFANA_PASSWORD", "envpw")
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("envuser:envpw"))
	if got := runGrafanaAuthCheck(t, Config{}); got != want {
		t.Fatalf("Authorization = %q, want %q", got, want)
	}
}

// runGrafanaAuthCheck runs the live Grafana datasource check against an httptest
// server and returns the Authorization header the server received.
func runGrafanaAuthCheck(t *testing.T, cfg Config) string {
	t.Helper()
	return runGrafanaAuthCheckIn(t, t.TempDir(), cfg)
}

func runGrafanaAuthCheckIn(t *testing.T, root string, cfg Config) string {
	t.Helper()
	var gotAuth string
	grafana := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		if gotAuth == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Write([]byte(`{"uid":"tempo","jsonData":{"tracesToLogsV2":{"datasourceUid":"loki","filterByTraceID":true,"filterBySpanID":true},"tracesToMetrics":{"datasourceUid":"prometheus"},"serviceMap":{"datasourceUid":"prometheus"}}}`))
	}))
	defer grafana.Close()

	cfg.Root = root
	cfg.GrafanaURL = grafana.URL
	report := VerifyConfig(cfg)
	if !hasCheck(report, "Grafana API Tempo correlation", true) {
		t.Fatalf("expected Grafana API check to pass, got %#v", report.Checks)
	}
	return gotAuth
}

func clearGrafanaEnv(t *testing.T) {
	t.Helper()
	t.Setenv("EXTENT_GRAFANA_TOKEN", "")
	t.Setenv("EXTENT_GRAFANA_USER", "")
	t.Setenv("EXTENT_GRAFANA_PASSWORD", "")
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

func hasCheck(report Report, name string, ok bool) bool {
	for _, check := range report.Checks {
		if check.Name == name && check.OK == ok {
			return true
		}
	}
	return false
}
