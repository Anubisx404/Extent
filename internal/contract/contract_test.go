package contract

import (
	"strings"
	"testing"

	"extent/internal/analyzer"
)

func TestRenderIncludesServiceSignalsSLOAndRedaction(t *testing.T) {
	doc := Render(analyzer.Result{
		ServiceName:       "payments-api",
		Runtime:           []string{"node"},
		Frameworks:        []string{"express"},
		DatabaseLibraries: []string{"postgres"},
	})

	for _, want := range []string{
		"service:",
		"name: payments-api",
		"traces: true",
		"http_latency_p95_ms: 300",
		"db_statement: sanitize",
		"database: true",
	} {
		if !strings.Contains(doc, want) {
			t.Fatalf("expected contract to contain %q:\n%s", want, doc)
		}
	}
}

