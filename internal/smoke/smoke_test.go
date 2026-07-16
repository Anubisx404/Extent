package smoke

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRunSendsTrafficAndPassesWhenCollectorCountersArePositive(t *testing.T) {
	appHits := 0
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		appHits++
		w.WriteHeader(http.StatusNoContent)
	}))
	defer app.Close()

	prom := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"success","data":{"result":[{"value":[1,"2"]}]}}`))
	}))
	defer prom.Close()

	report := Run(Config{URL: app.URL, PrometheusURL: prom.URL, Requests: 2})
	if !report.OK {
		t.Fatalf("expected smoke report OK, got %#v", report)
	}
	if appHits != 2 {
		t.Fatalf("expected 2 app hits, got %d", appHits)
	}
}

func TestRunFailsWhenPrometheusCountersAreZero(t *testing.T) {
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer app.Close()

	prom := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"success","data":{"result":[{"value":[1,"0"]}]}}`))
	}))
	defer prom.Close()

	report := Run(Config{URL: app.URL, PrometheusURL: prom.URL, Requests: 1})
	if report.OK {
		t.Fatalf("expected smoke report to fail on zero counters, got %#v", report)
	}
}

