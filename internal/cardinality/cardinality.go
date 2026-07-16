package cardinality

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type Report struct {
	Score       int       `json:"score"`
	Findings    []Finding `json:"findings,omitempty"`
	Suggestions []string  `json:"suggestions,omitempty"`
}

type Finding struct {
	File   string `json:"file"`
	Label  string `json:"label"`
	Reason string `json:"reason"`
}

var highCardinalityLabels = map[string]string{
	"trace_id":          "trace identifiers belong in structured metadata, not Loki labels",
	"span_id":           "span identifiers belong in structured metadata, not Loki labels",
	"user_id":           "user identifiers create unbounded label cardinality",
	"request_id":        "request identifiers create one label value per request",
	"order_id":          "business entity identifiers create high-cardinality labels",
	"db_statement":      "SQL statements should be sanitized metadata, not labels",
	"exception_message": "exception messages create unbounded labels",
}

func Analyze(root string) Report {
	var findings []Finding
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if !strings.HasSuffix(rel, ".yml") && !strings.HasSuffix(rel, ".yaml") && !strings.HasSuffix(rel, ".json") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		text := strings.ToLower(string(data))
		for label, reason := range highCardinalityLabels {
			if labelAppears(text, label) {
				findings = append(findings, Finding{File: rel, Label: label, Reason: reason})
			}
		}
		return nil
	})
	sort.Slice(findings, func(i, j int) bool {
		return findings[i].File+findings[i].Label < findings[j].File+findings[j].Label
	})
	score := 100 - len(findings)*15
	if score < 0 {
		score = 0
	}
	report := Report{Score: score, Findings: findings}
	if len(findings) > 0 {
		report.Suggestions = append(report.Suggestions, "Move trace_id/span_id/user/request/business identifiers into structured metadata or log fields.")
		report.Suggestions = append(report.Suggestions, "Keep Loki labels to low-cardinality dimensions such as service_name, environment, level, and runtime.")
	}
	return report
}

func labelAppears(text, label string) bool {
	labelPattern := regexp.QuoteMeta(label)
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`\{[^}]*\b` + labelPattern + `\s*(=|=~|!=|!~)`),
		regexp.MustCompile(`(?m)^\s*-\s*` + labelPattern + `\s*$`),
	}
	for _, rx := range patterns {
		if rx.MatchString(text) {
			return true
		}
	}
	return false
}
