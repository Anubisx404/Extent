package recommendations

import (
	"fmt"
	"strings"
)

type Input struct {
	SlowestPath          string
	DBShare              float64
	RepeatedDBPatterns   int
	LogsMissingTraceID   int
	ContainerCPU         float64
	HostCPU              float64
	RateLimit429Count    float64
	DBPoolWaiting        float64
	QueueLag             float64
	MemoryGrowthBytesSec float64
	TraceExamples        []string
	SoakErrorRate        float64
	SoakP50MS            float64
	SoakP95MS            float64
	SoakP99MS            float64
}

type Result struct {
	Items []Item `json:"items"`
}

type Item struct {
	Title      string `json:"title"`
	Evidence   string `json:"evidence"`
	Suggestion string `json:"suggestion"`
}

func Synthesize(input Input) Result {
	var items []Item
	path := input.SlowestPath
	if path == "" {
		path = "the slowest route"
	}
	if input.DBShare >= 0.6 {
		items = append(items, Item{
			Title:      "Database-bound request path",
			Evidence:   path + " spends most observed time in PostgreSQL/database spans.",
			Suggestion: "Inspect query plans, add missing indexes, and batch repeated lookups before scaling app CPU.",
		})
	}
	if input.RepeatedDBPatterns > 0 {
		items = append(items, Item{
			Title:      "Possible N+1 query pattern",
			Evidence:   "The same normalized database statement pattern repeated many times in request traces.",
			Suggestion: "Replace per-row queries with a single WHERE id IN (...) lookup or preloaded relation.",
		})
	}
	if input.LogsMissingTraceID > 0 {
		items = append(items, Item{
			Title:      "Log correlation gap",
			Evidence:   "Some error logs are missing trace_id/span_id fields.",
			Suggestion: "Patch logger bindings so every request/job log includes trace_id, span_id, service.name, and environment.",
		})
	}
	if input.ContainerCPU < 0.6 && input.HostCPU < 0.6 && input.DBShare >= 0.6 {
		items = append(items, Item{
			Title:      "CPU is not the primary bottleneck",
			Evidence:   "Container and host CPU are below saturation while DB span share is high.",
			Suggestion: "Prioritize query batching/indexing over increasing CPU limits.",
		})
	}
	if input.RateLimit429Count > 0 {
		items = append(items, Item{
			Title:      "Rate limiting / HTTP 429 throttling detected",
			Evidence:   fmt.Sprintf("%.0f throttled HTTP 429 responses observed under load.", input.RateLimit429Count),
			Suggestion: "Increase API rate limits, implement backoff/jitter on clients, or scale horizontal replica count.",
		})
	}
	if input.DBPoolWaiting > 0 {
		items = append(items, Item{
			Title:      "Database connection pool exhaustion",
			Evidence:   fmt.Sprintf("%.0f requests/clients waiting for available database connections.", input.DBPoolWaiting),
			Suggestion: "Increase database pool max connections, optimize connection lease durations, or add read replicas.",
		})
	}
	if input.QueueLag > 0 {
		items = append(items, Item{
			Title:      "Queue consumer starvation / backpressure",
			Evidence:   fmt.Sprintf("Queue message backlog detected (%.0f messages waiting).", input.QueueLag),
			Suggestion: "Scale queue worker concurrency, increase consumer batch sizes, or partition high-traffic topics.",
		})
	}
	if input.MemoryGrowthBytesSec > 0 {
		items = append(items, Item{
			Title:      "Potential memory leak detected",
			Evidence:   fmt.Sprintf("Positive memory growth slope (%.0f bytes/sec) observed during observation window.", input.MemoryGrowthBytesSec),
			Suggestion: "Profile heap allocations with pprof, check for unbounded cache growth or unclosed connection handles.",
		})
	}
	if input.SoakErrorRate > 0.01 {
		items = append(items, Item{
			Title:      "Elevated HTTP error rate under load",
			Evidence:   fmt.Sprintf("%.1f%% of synthetic requests failed or returned HTTP >= 400.", input.SoakErrorRate*100),
			Suggestion: "Inspect error logs and handler panic/timeout paths before increasing concurrency.",
		})
	}
	if input.SoakP95MS >= 500 {
		items = append(items, Item{
			Title:      "High endpoint latency under load",
			Evidence:   fmt.Sprintf("Client-observed p95 latency reached %.1f ms under load.", input.SoakP95MS),
			Suggestion: "Profile hot request handlers, downstream calls, and connection pool contention.",
		})
	}
	if input.SoakP50MS > 0 && input.SoakP99MS >= input.SoakP50MS*4 && input.SoakP99MS >= 50 {
		items = append(items, Item{
			Title:      "Tail latency amplification (p99/p50 skew)",
			Evidence:   fmt.Sprintf("p99 latency (%.1f ms) is %.1fx p50 (%.1f ms).", input.SoakP99MS, input.SoakP99MS/input.SoakP50MS, input.SoakP50MS),
			Suggestion: "Check GC pauses, lock contention, or queueing delays under concurrent load.",
		})
	}
	if len(items) == 0 {
		items = append(items, Item{
			Title:      "Need more telemetry evidence",
			Evidence:   "No dominant bottleneck was identified from the current signals.",
			Suggestion: "Run `extent verify --url`, generate realistic traffic, then rerun `extent report --format markdown`.",
		})
	}
	return Result{Items: items}
}

func (r Result) Markdown() string {
	var b strings.Builder
	for _, item := range r.Items {
		b.WriteString("- " + item.Title + ": " + item.Evidence + " " + item.Suggestion + "\n")
	}
	return b.String()
}
