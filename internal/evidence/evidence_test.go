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
			if !strings.Contains(r.URL.Query().Get("query"), "http_request_header_x_request_id") {
				t.Fatalf("expected correlated Loki query, got: %s", r.URL.Query().Get("query"))
			}
			w.Write([]byte(`{"status":"success","data":{"result":[{"stream":{"level":"error","service_name":"checkout","http_request_header_x_request_id":"req-1"},"values":[["1","{\"trace_id\":\"trace-1\",\"span_id\":\"span-1\",\"message\":\"payment failed\"}"]]}]}}`))
		case "/api/v1/query":
			if !strings.Contains(r.URL.Query().Get("query"), `service_name="checkout"`) {
				t.Fatalf("unscoped Prometheus query: %s", r.URL.RawQuery)
			}
			if strings.Contains(r.URL.Query().Get("query"), "http_server_duration_milliseconds_bucket") {
				w.Write([]byte(`{"status":"success","data":{"result":[{"metric":{"route":"/checkout"},"value":[1,"1.8"]}]}}`))
				return
			}
			w.Write([]byte(`{"status":"success","data":{"result":[]}}`))

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
	if len(result.Logs) != 1 || result.Logs[0].Message != "payment failed" {
		t.Fatalf("expected Loki log event, got %#v", result.Logs)
	}
	if result.Logs[0].RequestID != "req-1" {
		t.Fatalf("expected Loki request correlation, got %#v", result.Logs[0])
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
	if len(result.Provenance) != 4 {
		t.Fatalf("expected source provenance, got %#v", result.Provenance)
	}
}

func TestCollectPullsSaturationAndSlopeEvidence(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/search":
			w.Write([]byte(`{"traces":[]}`))
		case "/loki/api/v1/query_range":
			w.Write([]byte(`{"status":"success","data":{"result":[]}}`))
		case "/api/v1/query":
			q := r.URL.Query().Get("query")
			switch {
			case strings.Contains(q, "429"):
				w.Write([]byte(`{"status":"success","data":{"result":[{"metric":{},"value":[1,"15.0"]}]}}`))
			case strings.Contains(q, "db_client_connections") || strings.Contains(q, "waiting"):
				w.Write([]byte(`{"status":"success","data":{"result":[{"metric":{},"value":[1,"8.0"]}]}}`))
			case strings.Contains(q, "backlog") || strings.Contains(q, "lag") || strings.Contains(q, "queue"):
				w.Write([]byte(`{"status":"success","data":{"result":[{"metric":{},"value":[1,"120.0"]}]}}`))
			case strings.Contains(q, "deriv"):
				w.Write([]byte(`{"status":"success","data":{"result":[{"metric":{},"value":[1,"1048576.0"]}]}}`))
			default:
				w.Write([]byte(`{"status":"success","data":{"result":[]}}`))
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	result := Collect(Config{PrometheusURL: server.URL, LokiURL: server.URL, TempoURL: server.URL, Window: "10m", ServiceName: "checkout"})

	if result.Saturation == nil {
		t.Fatalf("expected saturation evidence, got nil")
	}
	if result.Saturation.RateLimit429Count != 15.0 {
		t.Fatalf("expected RateLimit429Count 15.0, got %f", result.Saturation.RateLimit429Count)
	}
	if result.Saturation.DBPoolWaiting != 8.0 {
		t.Fatalf("expected DBPoolWaiting 8.0, got %f", result.Saturation.DBPoolWaiting)
	}
	if result.Saturation.QueueLag != 120.0 {
		t.Fatalf("expected QueueLag 120.0, got %f", result.Saturation.QueueLag)
	}
	if result.Saturation.MemoryGrowthBytesSec != 1048576.0 {
		t.Fatalf("expected MemoryGrowthBytesSec 1048576.0, got %f", result.Saturation.MemoryGrowthBytesSec)
	}
}
