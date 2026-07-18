package scorer

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBuildScoreReportsDimensions(t *testing.T) {
	prom := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"success","data":{"result":[{"value":[1,"1"]}]}}`))
	}))
	defer prom.Close()

	score := Build(Config{PrometheusURL: prom.URL})
	if score.Total <= 0 {
		t.Fatalf("expected positive score, got %#v", score)
	}
	if !strings.Contains(score.Summary, "Telemetry Quality Score") {
		t.Fatalf("expected score summary, got %q", score.Summary)
	}
	if score.Coverage != 5.0/7.0 {
		t.Fatalf("coverage = %v, want %v", score.Coverage, 5.0/7.0)
	}
	if score.Dimensions[5].Status != "unknown" || score.Dimensions[6].Status != "unknown" {
		t.Fatalf("service/correlation dimensions fabricated: %#v", score.Dimensions)
	}
}

func TestMissingSignalsReceiveZeroNotFabricatedPartialCredit(t *testing.T) {
	prom := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"success","data":{"result":[]}}`))
	}))
	defer prom.Close()

	score := Build(Config{PrometheusURL: prom.URL})
	if score.Total != 0 {
		t.Fatalf("missing telemetry received credit: %#v", score)
	}
	for _, dimension := range score.Dimensions[:5] {
		if dimension.Status != "missing" || dimension.Score != 0 {
			t.Fatalf("dimension fabricated partial credit: %#v", dimension)
		}
	}
}

func TestUnavailablePrometheusIsNotCountedAsMeasuredZero(t *testing.T) {
	score := Build(Config{PrometheusURL: "ftp://example.test"})
	if score.Coverage != 0 || score.Total != 0 {
		t.Fatalf("unavailable backend counted as measured: %#v", score)
	}
	for _, dimension := range score.Dimensions[:5] {
		if dimension.Status != "unavailable" {
			t.Fatalf("dimension status = %#v", dimension)
		}
	}
}
