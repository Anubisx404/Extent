package cardinality

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/Anubisx404/Extent/internal/scanner"
	"gopkg.in/yaml.v3"
)

type Report struct {
	Score       int       `json:"score"`
	Findings    []Finding `json:"findings,omitempty"`
	Suggestions []string  `json:"suggestions,omitempty"`
	Unparsed    []string  `json:"unparsed,omitempty"`
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
	var unparsed []string
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if path != root && d.IsDir() && scanner.IsExcludedDir(d.Name()) {
			return filepath.SkipDir
		}
		if d.IsDir() {
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if !strings.HasSuffix(rel, ".yml") && !strings.HasSuffix(rel, ".yaml") && !strings.HasSuffix(rel, ".json") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			unparsed = append(unparsed, rel)
			return nil
		}
		var document yaml.Node
		if err := yaml.Unmarshal(data, &document); err != nil {
			unparsed = append(unparsed, rel)
			return nil
		}
		for label := range structuralLabels(&document) {
			if reason, risky := highCardinalityLabels[label]; risky {
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
	sort.Strings(unparsed)
	report := Report{Score: score, Findings: findings, Unparsed: unparsed}
	if len(findings) > 0 {
		report.Suggestions = append(report.Suggestions, "Move trace_id/span_id/user/request/business identifiers into structured metadata or log fields.")
		report.Suggestions = append(report.Suggestions, "Keep Loki labels to low-cardinality dimensions such as service_name, environment, level, and runtime.")
	}
	return report
}

func structuralLabels(node *yaml.Node) map[string]bool {
	found := map[string]bool{}
	var visit func(*yaml.Node, string)
	visit = func(current *yaml.Node, parentKey string) {
		if current == nil {
			return
		}
		switch current.Kind {
		case yaml.DocumentNode, yaml.SequenceNode:
			for _, child := range current.Content {
				visit(child, parentKey)
			}
		case yaml.MappingNode:
			for i := 0; i+1 < len(current.Content); i += 2 {
				key := strings.ToLower(strings.TrimSpace(current.Content[i].Value))
				value := current.Content[i+1]
				if key == "labels" || key == "labelnames" || key == "label_names" {
					collectLabelNames(value, found)
				}
				if (key == "expr" || key == "query" || key == "logql") && value.Kind == yaml.ScalarNode {
					collectQueryLabels(strings.ToLower(value.Value), found)
				}
				visit(value, key)
			}
		case yaml.ScalarNode:
			if parentKey == "expr" || parentKey == "query" || parentKey == "logql" {
				collectQueryLabels(strings.ToLower(current.Value), found)
			}
		}
	}
	visit(node, "")
	return found
}

func collectLabelNames(node *yaml.Node, found map[string]bool) {
	switch node.Kind {
	case yaml.MappingNode:
		for i := 0; i+1 < len(node.Content); i += 2 {
			found[strings.ToLower(strings.TrimSpace(node.Content[i].Value))] = true
		}
	case yaml.SequenceNode:
		for _, child := range node.Content {
			if child.Kind == yaml.ScalarNode {
				found[strings.ToLower(strings.TrimSpace(child.Value))] = true
			}
		}
	}
}

func collectQueryLabels(query string, found map[string]bool) {
	for label := range highCardinalityLabels {
		if labelAppears(query, label) {
			found[label] = true
		}
	}
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
