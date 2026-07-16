package verifier

import (
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
