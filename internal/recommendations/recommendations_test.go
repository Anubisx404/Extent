package recommendations

import "testing"

func TestSynthesizeProducesEvidenceBackedRecommendations(t *testing.T) {
	result := Synthesize(Input{
		SlowestPath:        "/checkout",
		DBShare:            0.72,
		RepeatedDBPatterns: 34,
		LogsMissingTraceID: 9,
		ContainerCPU:       0.2,
		HostCPU:            0.3,
	})

	joined := result.Markdown()
	for _, want := range []string{"N+1", "/checkout", "trace_id", "PostgreSQL", "CPU is not the primary"} {
		if !contains(joined, want) {
			t.Fatalf("expected recommendations to contain %q, got:\n%s", want, joined)
		}
	}
}

func contains(text, want string) bool {
	return len(text) >= len(want) && (text == want || len(want) == 0 || index(text, want) >= 0)
}

func index(text, want string) int {
	for i := 0; i+len(want) <= len(text); i++ {
		if text[i:i+len(want)] == want {
			return i
		}
	}
	return -1
}
