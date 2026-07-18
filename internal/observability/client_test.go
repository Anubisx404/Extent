package observability

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestClientPreservesBasePathBoundsBodiesAndRedactsErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/prefix/api/v1/query" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		_, _ = io.WriteString(w, strings.Repeat("x", 32))
	}))
	defer server.Close()
	base, err := ValidateURL(server.URL + "/prefix?token=fixture-secret")
	if err != nil {
		t.Fatal(err)
	}
	client := NewClient(time.Second, 8)
	_, err = client.Do(context.Background(), http.MethodGet, base, "api/v1/query", url.Values{"api_key": {"another-secret"}}, nil)
	if err == nil {
		t.Fatal("oversized response accepted")
	}
	message := err.Error()
	if strings.Contains(message, "fixture-secret") || strings.Contains(message, "another-secret") {
		t.Fatalf("secret leaked in error: %s", message)
	}
	if !strings.Contains(message, "%5BREDACTED%5D") && !strings.Contains(message, "[REDACTED]") {
		t.Fatalf("error did not disclose redaction: %s", message)
	}
}

func TestClientRejectsRedirectAndUnsafeBase(t *testing.T) {
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://example.test/elsewhere", http.StatusFound)
	}))
	defer redirect.Close()
	base, _ := url.Parse(redirect.URL)
	if _, err := NewClient(time.Second, 1024).Do(context.Background(), http.MethodGet, base, "/", nil, nil); err == nil || !strings.Contains(err.Error(), "redirect rejected") {
		t.Fatalf("redirect error = %v", err)
	}
	unsafe, _ := url.Parse("ftp://example.test")
	if _, err := NewClient(time.Second, 1024).Do(context.Background(), http.MethodGet, unsafe, "/", nil, nil); err == nil {
		t.Fatal("unsafe base URL accepted")
	}
}

func TestPrometheusQueryRequiresBoundedPlaceholders(t *testing.T) {
	var query string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.Query().Get("query")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"status":"success","data":{"result":[{"metric":{"service_name":"checkout"},"value":[1,"2"]}]}}`)
	}))
	defer server.Close()
	prometheus, err := NewPrometheus(server.URL+"/prom", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	got, err := prometheus.Query(context.Background(), `sum(rate(http_server_duration_seconds_count{service_name=$service}[$lookback]))`, `checkout"} or vector(1)`, "5m")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Value != 2 {
		t.Fatalf("samples = %#v", got)
	}
	if strings.Contains(query, `service_name="checkout"} or vector(1)`) || !strings.Contains(query, `service_name="checkout\"} or vector(1)"`) || !strings.Contains(query, `[5m]`) {
		t.Fatalf("unsafe or incorrect query: %s", query)
	}
	if _, err := prometheus.Query(context.Background(), "sum(up)", "checkout", "5m"); err == nil {
		t.Fatal("query without service placeholder accepted")
	}
	if _, err := prometheus.Query(context.Background(), `up{service_name=$service}`, "checkout", "forever"); err == nil {
		t.Fatal("invalid lookback accepted")
	}
}
