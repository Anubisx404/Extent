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
}

