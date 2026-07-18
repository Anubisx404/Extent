package evidence

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCollectPullsTempoLokiAndPrometheusEvidence(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/search":
			if !strings.Contains(r.URL.Query().Get("q"), `resource.service.name = "checkout"`) {
				t.Fatalf("unscoped Tempo query: %s", r.URL.RawQuery)
			}
			w.Write([]byte(`{"traces":[{"traceID":"0123456789abcdef0123456789abcdef","rootServiceName":"checkout","rootTraceName":"GET /checkout","durationMs":1800}]}`))
		case "/api/traces/0123456789abcdef0123456789abcdef":
			w.Write([]byte(`{"batches":[{"scopeSpans":[{"spans":[
				{"traceID":"0123456789abcdef0123456789abcdef","spanID":"db1","name":"SELECT product_stock","durationNanos":700000000,"attributes":[{"key":"db.system","value":{"stringValue":"postgresql"}},{"key":"db.statement","value":{"stringValue":"SELECT * FROM product_stock WHERE product_id = ?"}}]},
				{"traceID":"0123456789abcdef0123456789abcdef","spanID":"db2","name":"SELECT product_stock","durationNanos":650000000,"attributes":[{"key":"db.system","value":{"stringValue":"postgresql"}},{"key":"db.statement","value":{"stringValue":"SELECT * FROM product_stock WHERE product_id = ?"}}]},
				{"traceID":"0123456789abcdef0123456789abcdef","spanID":"http1","name":"GET https://payments.example","durationNanos":200000000,"attributes":[{"key":"http.request.method","value":{"stringValue":"GET"}},{"key":"server.address","value":{"stringValue":"payments.example"}}]},
				{"traceID":"0123456789abcdef0123456789abcdef","spanID":"queue1","name":"publish checkout","durationNanos":50000000,"attributes":[{"key":"messaging.system","value":{"stringValue":"rabbitmq"}}]}
			]}]}]}`))
		case "/loki/api/v1/query_range":
			if !strings.Contains(r.URL.Query().Get("query"), `service_name="checkout"`) {
				t.Fatalf("unscoped Loki query: %s", r.URL.RawQuery)
			}
			w.Write([]byte(`{"status":"success","data":{"result":[{"stream":{"level":"error","service_name":"checkout"},"values":[["1","{\"trace_id\":\"trace-1\",\"span_id\":\"span-1\",\"message\":\"payment failed\"}"]]}]}}`))
		case "/api/v1/query":
			if !strings.Contains(r.URL.Query().Get("query"), `service_name="checkout"`) {
				t.Fatalf("unscoped Prometheus query: %s", r.URL.RawQuery)
			}
			w.Write([]byte(`{"status":"success","data":{"result":[{"metric":{"route":"/checkout"},"value":[1,"1.8"]}]}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	result := Collect(Config{PrometheusURL: server.URL, LokiURL: server.URL, TempoURL: server.URL, Window: "30m", ServiceName: "checkout"})

	if len(result.Traces) != 1 || result.Traces[0].TraceID != "0123456789abcdef0123456789abcdef" {
		t.Fatalf("expected trace evidence, got %#v", result.Traces)
	}
	if len(result.LogAnomalies) != 1 || result.LogAnomalies[0].TraceID != "trace-1" {
		t.Fatalf("expected Loki log anomaly with trace id, got %#v", result.LogAnomalies)
	}
	if len(result.Metrics) != 1 || result.Metrics[0].Name != "http_p95_latency" {
		t.Fatalf("expected Prometheus metric evidence, got %#v", result.Metrics)
	}
	if len(result.Spans) != 4 {
		t.Fatalf("expected detailed spans, got %#v", result.Spans)
	}
	if len(result.DBFindings) == 0 || result.DBFindings[0].RepeatCount != 2 {
		t.Fatalf("expected repeated DB statement finding, got %#v", result.DBFindings)
	}
	if len(result.ExternalHTTP) != 1 || result.ExternalHTTP[0].Destination != "payments.example" {
		t.Fatalf("expected external HTTP evidence, got %#v", result.ExternalHTTP)
	}
	if len(result.QueueFindings) != 1 || result.QueueFindings[0].System != "rabbitmq" {
		t.Fatalf("expected queue evidence, got %#v", result.QueueFindings)
	}
	if len(result.Provenance) != 3 {
		t.Fatalf("expected source provenance, got %#v", result.Provenance)
	}
}
