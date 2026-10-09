package scorer

import (
	"net/http"
	"net/http/httptest"
	"net/url"
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
	for _, dimension := range score.Dimensions[:4] {
		if dimension.Status != "missing" || dimension.Score != 0 {
			t.Fatalf("dimension fabricated partial credit: %#v", dimension)
		}
	}
	if host := score.Dimensions[4]; host.Status != "unknown" || host.Score != 0 {
		t.Fatalf("absent optional host metrics should be unknown, not scored: %#v", host)
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

func TestServiceScopedDimensionsReachFullScore(t *testing.T) {
	var promQueries []string
	var lokiQueries []url.Values
	prom := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		promQueries = append(promQueries, r.URL.Query().Get("query"))
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"success","data":{"result":[{"value":[1,"1"]}]}}`))
	}))
	defer prom.Close()
	loki := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lokiQueries = append(lokiQueries, r.URL.Query())
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"success","data":{"result":[{"stream":{"service_name":"checkout"},"values":[["1","{\"trace_id\":\"0af7651916cd43dd8448eb211c80319c\"}"]]}]}}`))
	}))
	defer loki.Close()
	tempo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/traces/0af7651916cd43dd8448eb211c80319c") {
			t.Errorf("unexpected Tempo path %s", r.URL.Path)
		}
		w.Write([]byte(`{}`))
	}))
	defer tempo.Close()

	score := Build(Config{PrometheusURL: prom.URL, ServiceName: "checkout", LokiURL: loki.URL, TempoURL: tempo.URL})
	if score.Total != 100 {
		t.Fatalf("total = %d, want 100: %#v", score.Total, score.Dimensions)
	}
	if score.Coverage != 1 {
		t.Fatalf("coverage = %v, want 1", score.Coverage)
	}
	identity := score.Dimensions[5]
	correlation := score.Dimensions[6]
	if identity.Status != "measured" || identity.Score != 100 {
		t.Fatalf("service identity = %#v", identity)
	}
	if correlation.Status != "measured" || correlation.Score != 100 {
		t.Fatalf("correlation = %#v", correlation)
	}
	if !strings.Contains(correlation.Detail, "resolves in Tempo") {
		t.Fatalf("expected Tempo detail, got %q", correlation.Detail)
	}
	if !contains(promQueries, `sum(http_server_duration_milliseconds_count{service_name="checkout"}) > 0`) {
		t.Fatalf("service identity query not issued: %#v", promQueries)
	}
	if len(lokiQueries) != 1 {
		t.Fatalf("expected one Loki query, got %d", len(lokiQueries))
	}
	if got := lokiQueries[0].Get("query"); got != `{service_name="checkout"} | trace_id != ""` {
		t.Fatalf("Loki query = %q", got)
	}
	if got := lokiQueries[0].Get("limit"); got != "1" {
		t.Fatalf("Loki limit = %q, want 1", got)
	}
}

func TestCorrelationMissingWhenNoLogCarriesTraceID(t *testing.T) {
	prom := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"success","data":{"result":[{"value":[1,"1"]}]}}`))
	}))
	defer prom.Close()
	loki := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"success","data":{"result":[]}}`))
	}))
	defer loki.Close()

	score := Build(Config{PrometheusURL: prom.URL, ServiceName: "checkout", LokiURL: loki.URL})
	correlation := score.Dimensions[6]
	if correlation.Status != "missing" || correlation.Score != 0 {
		t.Fatalf("correlation = %#v", correlation)
	}
	// Seven scored dimensions: five global and identity at 100, correlation at 0.
	if score.Total != 600/7 {
		t.Fatalf("total = %d, want %d", score.Total, 600/7)
	}
}

func TestCorrelationUnavailableWithoutLokiAndExcludedFromTotal(t *testing.T) {
	prom := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"success","data":{"result":[{"value":[1,"1"]}]}}`))
	}))
	defer prom.Close()

	score := Build(Config{PrometheusURL: prom.URL, ServiceName: "checkout"})
	correlation := score.Dimensions[6]
	if correlation.Status != "unavailable" || !strings.Contains(correlation.Detail, "--loki") {
		t.Fatalf("correlation = %#v", correlation)
	}
	if score.Total != 100 {
		t.Fatalf("unavailable correlation counted as zero: total = %d", score.Total)
	}
	if !strings.Contains(score.Summary, "6/7 measured") {
		t.Fatalf("summary does not state measured count: %q", score.Summary)
	}
	if !strings.Contains(score.Summary, "excluded from the score") {
		t.Fatalf("summary does not explain exclusions: %q", score.Summary)
	}
}

func TestWithoutServiceUnknownDimensionsAreExcluded(t *testing.T) {
	prom := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"success","data":{"result":[{"value":[1,"1"]}]}}`))
	}))
	defer prom.Close()

	score := Build(Config{PrometheusURL: prom.URL})
	if score.Total != 100 {
		t.Fatalf("unknown dimensions counted as zero: total = %d", score.Total)
	}
	if !strings.Contains(score.Summary, "5/7 measured") {
		t.Fatalf("summary = %q", score.Summary)
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
