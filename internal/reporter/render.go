package reporter

import (
	"fmt"
	"sort"
	"strings"
)

type namedMeasurement struct {
	key         string
	label       string
	measurement Measurement
}

func orderedMeasurements(m map[string]Measurement) []namedMeasurement {
	standard := []struct {
		key   string
		label string
	}{
		{key: "httpP95", label: "HTTP p95 latency"},
		{key: "dbP95", label: "DB p95 latency"},
		{key: "containerCPU", label: "Container CPU pressure"},
		{key: "hostCPU", label: "Host CPU pressure"},
		{key: "soakThroughput", label: "Soak throughput"},
		{key: "soakTotalRequests", label: "Soak total requests"},
		{key: "soakErrorRate", label: "Soak error rate"},
	}
	seen := map[string]bool{}
	var result []namedMeasurement
	for _, item := range standard {
		if val, ok := m[item.key]; ok {
			result = append(result, namedMeasurement{key: item.key, label: item.label, measurement: val})
			seen[item.key] = true
		}
	}
	var extraKeys []string
	for k := range m {
		if !seen[k] {
			extraKeys = append(extraKeys, k)
		}
	}
	sort.Strings(extraKeys)
	for _, k := range extraKeys {
		result = append(result, namedMeasurement{key: k, label: k, measurement: m[k]})
	}
	return result
}

func formatMeasurementValue(m Measurement) string {
	if m.Status != "measured" {
		return "-"
	}
	if m.Unit == "ratio" {
		return fmt.Sprintf("%.0f%%", m.Value*100)
	}
	if m.Unit == "requests" {
		return fmt.Sprintf("%.0f requests", m.Value)
	}
	if m.Unit != "" {
		return fmt.Sprintf("%.3f %s", m.Value, m.Unit)
	}
	return fmt.Sprintf("%.3f", m.Value)
}

func healthStatus(report Report) string {
	if metric, ok := report.Measurements["httpP95"]; ok && metric.Status != "measured" {
		return "yellow"
	}
	if report.OK {
		return "green"
	}
	if len(report.Warnings) > 0 {
		return "yellow"
	}
	return "unknown"
}

func spanContext(attributes map[string]string) string {
	parts := make([]string, 0, 6)
	for _, item := range []struct {
		label string
		keys  []string
	}{
		{label: "method", keys: []string{"http.request.method", "http.method"}},
		{label: "route", keys: []string{"http.route", "http.target"}},
		{label: "status", keys: []string{"http.response.status_code", "http.status_code"}},
		{label: "db", keys: []string{"db.system", "db.system.name"}},
		{label: "destination", keys: []string{"server.address"}},
		{label: "messaging", keys: []string{"messaging.system"}},
	} {
		for _, key := range item.keys {
			if value := strings.TrimSpace(attributes[key]); value != "" {
				parts = append(parts, item.label+"="+safeInline(value))
				break
			}
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return " (" + strings.Join(parts, ", ") + ")"
}

func formatMetricLabels(labels map[string]string) string {
	if len(labels) == 0 {
		return ""
	}
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+safeInline(labels[key]))
	}
	return " (" + strings.Join(parts, ", ") + ")"
}

func safeTableCell(value string) string {
	value = strings.ReplaceAll(value, "\r\n", " ")
	value = strings.ReplaceAll(value, "\r", " ")
	value = strings.ReplaceAll(value, "\n", " ")
	value = strings.ReplaceAll(value, "\t", " ")
	value = strings.ReplaceAll(value, "|", "\\|")
	value = strings.ReplaceAll(value, "`", "'")
	value = strings.TrimSpace(value)
	if value == "" {
		return "-"
	}
	return value
}

func safeInline(value string) string {
	value = strings.ReplaceAll(value, "\r\n", " ")
	value = strings.ReplaceAll(value, "\r", " ")
	value = strings.ReplaceAll(value, "\n", " ")
	value = strings.ReplaceAll(value, "\t", " ")
	value = strings.ReplaceAll(value, "`", "'")
	return strings.TrimSpace(value)
}
