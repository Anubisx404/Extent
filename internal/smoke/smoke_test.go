package smoke

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRunUsesPostRequestDeltasAndLabelsGlobalEvidenceAsSupporting(t *testing.T) {
	appHits := 0
	correlation := ""
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		appHits++
		if correlation == "" {
			correlation = r.Header.Get("X-Request-ID")
		} else if r.Header.Get("X-Request-ID") != correlation {
			t.Fatalf("correlation token changed between requests")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer app.Close()

	queryCalls := map[string]int{}
	prom := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		expr := r.URL.Query().Get("query")
		queryCalls[expr]++
		value := "2"
		if queryCalls[expr] > 1 {
			value = "3"
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"success","data":{"result":[{"value":[1,"` + value + `"]}]}}`))
	}))
	defer prom.Close()
	tempo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		traceID := strings.TrimPrefix(r.URL.Path, "/api/traces/")
		if correlation == "" || traceID != correlation {
			t.Fatalf("Tempo lookup was not constrained by the synthetic trace ID: path=%s correlation=%s", r.URL.Path, correlation)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"batches":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"checkout"}}]},"scopeSpans":[{"spans":[{"attributes":[{"key":"http.request.header.x_request_id","value":{"arrayValue":{"values":[{"stringValue":"` + correlation + `"}]}}}]}]}]}]}`))
	}))
	defer tempo.Close()
	loki := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query().Get("query")
		if !strings.Contains(query, `service_name="checkout"`) || correlation == "" || !strings.Contains(query, correlation) {
			t.Fatalf("unsafe or unconstrained Loki query: %s", query)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"success","data":{"result":[{}]}}`))
	}))
	defer loki.Close()

	report := Run(Config{URL: app.URL, PrometheusURL: prom.URL, TempoURL: tempo.URL, LokiURL: loki.URL, ServiceName: "checkout", Requests: 2})
	if !report.OK {
		t.Fatalf("expected smoke report OK, got %#v", report)
	}
	if appHits != 2 {
		t.Fatalf("expected 2 app hits, got %d", appHits)
	}
	if correlation == "" || report.CorrelationID != correlation {
		t.Fatalf("correlation evidence missing: header=%q report=%#v", correlation, report)
	}
	if !report.TargetVerified {
		t.Fatal("correlated target trace was not verified")
	}
	for _, check := range report.Checks[1:4] {
		if check.Status != "supporting" || check.Scope != "global" || check.Value != 1 {
			t.Fatalf("global delta check = %#v", check)
		}
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

	report := Run(Config{URL: app.URL, PrometheusURL: prom.URL, Requests: 1, SettleTimeout: 10 * time.Millisecond})
	if report.OK {
		t.Fatalf("expected smoke report to fail on zero counters, got %#v", report)
	}
}
