package recommendations

import "strings"

type Input struct {
	SlowestPath        string
	DBShare            float64
	RepeatedDBPatterns int
	LogsMissingTraceID int
	ContainerCPU       float64
	HostCPU            float64
	TraceExamples      []string
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
