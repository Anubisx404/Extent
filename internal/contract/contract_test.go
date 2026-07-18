package contract

import (
	"strings"
	"testing"

	"github.com/Anubisx404/Extent/internal/analyzer"
	"github.com/Anubisx404/Extent/internal/config"
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

func TestRenderRoundTripsAndQuotesAnalyzerName(t *testing.T) {
	doc := Render(analyzer.Result{ServiceName: "evil: true\nunknown: yes", DatabaseLibraries: []string{"redis"}, Queues: []string{"rabbit"}})
	c, err := config.Parse([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	if c.Service.Name != "evil: true\nunknown: yes" || !c.Instrumentation.Redis || !c.Instrumentation.Queues {
		t.Fatalf("unexpected config: %+v", c)
	}
}
