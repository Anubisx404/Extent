package reporter

// Figures for the detail tabs of the HTML dashboard (html.go).

import (
	"fmt"
	"html"
	"math"
	"sort"
	"strings"

	"github.com/Anubisx404/Extent/internal/baseline"
	"github.com/Anubisx404/Extent/internal/evidence"
)

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return s
}

func trimLabel(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

// traceDurations draws one bar per returned trace on a shared linear scale;
// the slowest is solid, the rest ink-3.
func traceDurations(traces []evidence.TraceExample) string {
	rows := append([]evidence.TraceExample(nil), traces...)
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].DurationMS > rows[j].DurationMS })
	if len(rows) > 10 {
		rows = rows[:10]
	}
	top := math.Max(rows[0].DurationMS, 1)
	const barX, barW, rowH = 200.0, 300.0, 28.0
	h := float64(len(rows))*rowH + 4
	var b strings.Builder
	fmt.Fprintf(&b, "<svg viewBox=\"0 0 640 %.0f\" role=\"img\" aria-label=\"Trace root durations\">\n", h)
	for i, t := range rows {
		y := float64(i)*rowH + 2
		fill := "var(--ink-3)"
		if i == 0 {
			fill = "currentColor"
		}
		wd := math.Max(t.DurationMS/top*barW, 2)
		fmt.Fprintf(&b, "<text x=\"0\" y=\"%.0f\" font-size=\"12\" class=\"mono\"><title>%s</title>%s</text>", y+15, html.EscapeString(t.TraceID), html.EscapeString(trimLabel(t.RootTraceName, 26)))
		fmt.Fprintf(&b, "<rect x=\"%.0f\" y=\"%.0f\" width=\"%.1f\" height=\"16\" fill=\"%s\" stroke=\"currentColor\" stroke-width=\"1\"/>", barX, y+3, wd, fill)
		fmt.Fprintf(&b, "<text x=\"640\" y=\"%.0f\" text-anchor=\"end\" font-size=\"12\"><tspan class=\"num\" font-size=\"15\">%s</tspan></text>\n", y+16, html.EscapeString(formatMillis(t.DurationMS)))
	}
	b.WriteString("</svg>\n")
	return b.String()
}

// dbRepeats draws each database finding as one mark per repetition of its
// normalized statement, so an N+1 pattern reads as a long row.
func dbRepeats(findings []evidence.DBFinding) string {
	if len(findings) == 0 {
		return ""
	}
	const maxMarks = 48
	h := float64(len(findings))*46 + 2
	var b strings.Builder
	fmt.Fprintf(&b, "<svg viewBox=\"0 0 640 %.0f\" role=\"img\" aria-label=\"Database statement repeats\">\n", h)
	for i, f := range findings {
		y := float64(i) * 46
		fmt.Fprintf(&b, "<text x=\"0\" y=\"%.0f\" font-size=\"12\" class=\"mono\"><title>%s</title>%s</text>", y+12, html.EscapeString(f.NormalizedStatement), html.EscapeString(trimLabel(f.NormalizedStatement, 60)))
		marks := f.RepeatCount
		if marks > maxMarks {
			marks = maxMarks
		}
		for k := 0; k < marks; k++ {
			fmt.Fprintf(&b, "<rect x=\"%d\" y=\"%.0f\" width=\"6\" height=\"18\" fill=\"currentColor\"/>", k*9, y+20)
		}
		if f.RepeatCount > maxMarks {
			fmt.Fprintf(&b, "<text x=\"%d\" y=\"%.0f\" font-size=\"11\" class=\"dim\">+%d</text>", maxMarks*9+2, y+34, f.RepeatCount-maxMarks)
		}
		fmt.Fprintf(&b, "<text x=\"640\" y=\"%.0f\" text-anchor=\"end\" font-size=\"12\"><tspan class=\"num\" font-size=\"17\">%d×</tspan> · %s</text>\n", y+35, f.RepeatCount, html.EscapeString(formatMillis(f.TotalDurationMS)))
	}
	b.WriteString("</svg>\n")
	return b.String()
}

// logLevels draws log event counts per level, plus the anomaly count, as
// rows of marks.
func logLevels(logs []evidence.LogEvent, anomalies int) string {
	counts := map[string]int{}
	for _, e := range logs {
		switch strings.ToLower(e.Level) {
		case "error", "err", "fatal", "critical":
			counts["error"]++
		case "warn", "warning":
			counts["warn"]++
		case "", "info", "debug":
			counts["info"]++
		default:
			counts["other"]++
		}
	}
	rows := []struct {
		label string
		n     int
		fill  string
	}{{"error", counts["error"], "currentColor"}, {"warn", counts["warn"], "url(#hatch)"}, {"info", counts["info"], "var(--ink-3)"}, {"other", counts["other"], "var(--surface)"}, {"anomalies", anomalies, "currentColor"}}
	const maxMarks = 30
	var b strings.Builder
	fmt.Fprintf(&b, "<svg viewBox=\"0 0 360 %d\" role=\"img\" aria-label=\"Log events by level\">\n", len(rows)*26+2)
	for i, r := range rows {
		y := float64(i) * 26
		fmt.Fprintf(&b, "<text x=\"0\" y=\"%.0f\" font-size=\"12\">%s</text>", y+14, r.label)
		marks := r.n
		if marks > maxMarks {
			marks = maxMarks
		}
		for k := 0; k < marks; k++ {
			fmt.Fprintf(&b, "<rect x=\"%d\" y=\"%.0f\" width=\"5\" height=\"16\" fill=\"%s\" stroke=\"currentColor\" stroke-width=\"1\"/>", 84+k*7, y+2, r.fill)
		}
		if r.n == 0 {
			fmt.Fprintf(&b, "<line x1=\"84\" y1=\"%.0f\" x2=\"116\" y2=\"%.0f\" stroke=\"var(--ink-3)\"/>", y+10, y+10)
		}
		fmt.Fprintf(&b, "<text x=\"360\" y=\"%.0f\" text-anchor=\"end\" class=\"num\" font-size=\"15\">%d</text>\n", y+15, r.n)
	}
	b.WriteString("</svg>\n")
	return b.String()
}

// dumbbell draws each measurement's change against the baseline on one
// shared scale: the hollow dot is the baseline (0%), the solid dot this run,
// as a percentage of the baseline value.
func dumbbell(keys []string, deltas map[string]baseline.Delta) string {
	if len(keys) == 0 {
		return ""
	}
	rel := map[string]float64{}
	lo, hi := 0.0, 0.0
	for _, key := range keys {
		d := deltas[key]
		if d.Comparable && d.Before.Status == "measured" && d.After.Status == "measured" && d.Before.Value != 0 {
			r := (d.After.Value - d.Before.Value) / math.Abs(d.Before.Value)
			rel[key] = r
			lo, hi = math.Min(lo, r), math.Max(hi, r)
		}
	}
	// The scale covers the observed changes plus a little room on the
	// empty side, so a one-sided change does not waste half the axis.
	lo, hi = math.Floor(lo*10)/10, math.Ceil(hi*10)/10
	if lo == 0 {
		lo = -math.Max(hi/5, 0.1)
	}
	if hi == 0 {
		hi = math.Max(-lo/5, 0.1)
	}
	const x0, x1, rowH = 130.0, 390.0, 38.0
	x := func(r float64) float64 { return x0 + (r-lo)/(hi-lo)*(x1-x0) }
	mid := x(0)
	h := float64(len(keys))*rowH + 34
	var b strings.Builder
	fmt.Fprintf(&b, "<svg viewBox=\"0 0 640 %.0f\" role=\"img\" aria-label=\"Change against the baseline per measurement\">\n", h)
	for _, t := range []float64{lo, 0, hi / 2, hi} {
		fmt.Fprintf(&b, "<line x1=\"%.1f\" y1=\"4\" x2=\"%.1f\" y2=\"%.0f\" stroke=\"%s\" stroke-width=\"1\"/><text x=\"%.1f\" y=\"%.0f\" text-anchor=\"middle\" font-size=\"11\" class=\"mono dim\">%+.0f%%</text>\n",
			x(t), x(t), h-20, map[bool]string{true: "currentColor", false: "var(--rule)"}[t == 0], x(t), h-4, t*100)
	}
	for i, key := range keys {
		d := deltas[key]
		y := float64(i)*rowH + 22
		fmt.Fprintf(&b, "<text x=\"0\" y=\"%.0f\" font-size=\"12\" class=\"mono\">%s</text>", y+4, html.EscapeString(key))
		r, ok := rel[key]
		if !ok {
			fmt.Fprintf(&b, "<rect x=\"%.0f\" y=\"%.0f\" width=\"%.0f\" height=\"14\" fill=\"none\" stroke=\"var(--ink-2)\" stroke-dasharray=\"4 3\"/><text x=\"640\" y=\"%.0f\" text-anchor=\"end\" font-size=\"12\" class=\"dim\">not comparable</text>\n", x0, y-7, x1-x0, y+4)
			continue
		}
		fmt.Fprintf(&b, "<line x1=\"%.1f\" y1=\"%.0f\" x2=\"%.1f\" y2=\"%.0f\" stroke=\"currentColor\" stroke-width=\"3\"/>", mid, y, x(r), y)
		fmt.Fprintf(&b, "<circle cx=\"%.1f\" cy=\"%.0f\" r=\"6\" fill=\"var(--surface)\" stroke=\"currentColor\" stroke-width=\"2\"/><circle cx=\"%.1f\" cy=\"%.0f\" r=\"6\" fill=\"currentColor\"/>", mid, y, x(r), y)
		fmt.Fprintf(&b, "<text x=\"640\" y=\"%.0f\" text-anchor=\"end\" font-size=\"12\">%s → <tspan class=\"num\" font-size=\"15\">%s</tspan> <tspan class=\"dim\">(%s, %+.0f%%)</tspan></text>\n",
			y+4, html.EscapeString(formatUnit(d.Before.Value, d.Before.Unit)), html.EscapeString(formatUnit(d.After.Value, d.After.Unit)), html.EscapeString(formatChange(d.Change, d.After.Unit)), r*100)
	}
	b.WriteString("</svg>\n<ul class=\"legend\"><li><svg width=\"14\" height=\"14\" style=\"width:14px\"><circle cx=\"7\" cy=\"7\" r=\"5\" fill=\"none\" stroke=\"currentColor\" stroke-width=\"2\"/></svg>baseline</li><li><svg width=\"14\" height=\"14\" style=\"width:14px\"><circle cx=\"7\" cy=\"7\" r=\"5\" fill=\"currentColor\"/></svg>this run</li></ul>\n")
	return b.String()
}

// runtimeFigure draws the local stack Extent generates: the application sends
// OTLP to the Collector, which feeds Prometheus, Tempo and Loki (read by
// Grafana); extent report queries the three backends directly. Each backend
// carries this report's source status.
func runtimeFigure(prom, tempo, loki string) string {
	var b strings.Builder
	b.WriteString("<svg viewBox=\"0 0 600 330\" role=\"img\" aria-label=\"Runtime: application, collector, backends, Grafana and extent report\">\n")
	box := func(x, y, w, h float64, title, sub string, dashed bool) {
		dash := ""
		if dashed {
			dash = " stroke-dasharray=\"5 4\""
		}
		fmt.Fprintf(&b, "<rect x=\"%.0f\" y=\"%.0f\" width=\"%.0f\" height=\"%.0f\" fill=\"var(--surface)\" stroke=\"currentColor\" stroke-width=\"2\"%s/><text x=\"%.0f\" y=\"%.0f\" font-size=\"14\">%s</text><text x=\"%.0f\" y=\"%.0f\" font-size=\"11\" class=\"mono dim\">%s</text>\n",
			x, y, w, h, dash, x+10, y+21, title, x+10, y+38, sub)
	}
	arrow := func(x, y float64) {
		fmt.Fprintf(&b, "<path d=\"M%.0f %.0f l-7 -4 v8 z\" fill=\"currentColor\"/>", x, y)
	}
	box(0, 100, 116, 52, "Application", "OTLP export", false)
	b.WriteString("<line x1=\"116\" y1=\"126\" x2=\"144\" y2=\"126\" stroke=\"currentColor\" stroke-width=\"1.5\"/>")
	arrow(146, 126)
	box(148, 100, 120, 52, "Collector", ":4317 / :4318", false)
	backends := []struct {
		name, port, status string
		y                  float64
	}{{"Prometheus", ":9090", prom, 10}, {"Tempo", ":3200", tempo, 100}, {"Loki", ":3100", loki, 190}}
	b.WriteString("<path d=\"M268 126 H290 V36 M290 216 V126\" fill=\"none\" stroke=\"currentColor\" stroke-width=\"1.5\"/>")
	b.WriteString("<path d=\"M268 272 H310 V36\" fill=\"none\" stroke=\"currentColor\" stroke-width=\"1.5\" stroke-dasharray=\"4 4\"/>")
	for _, n := range backends {
		cy := n.y + 26
		fmt.Fprintf(&b, "<line x1=\"290\" y1=\"%.0f\" x2=\"338\" y2=\"%.0f\" stroke=\"currentColor\" stroke-width=\"1.5\"/>", cy, cy)
		arrow(340, cy)
		box(342, n.y, 150, 52, n.name, n.port, n.status == "not collected")
		b.WriteString(swatchSVG(462, n.y+14, 22, n.status))
		fmt.Fprintf(&b, "<title>%s: %s</title>", n.name, html.EscapeString(n.status))
		fmt.Fprintf(&b, "<line x1=\"492\" y1=\"%.0f\" x2=\"512\" y2=\"%.0f\" stroke=\"currentColor\" stroke-width=\"1\"/>", cy, cy)
	}
	b.WriteString("<line x1=\"512\" y1=\"36\" x2=\"512\" y2=\"216\" stroke=\"currentColor\" stroke-width=\"1\"/><line x1=\"512\" y1=\"126\" x2=\"518\" y2=\"126\" stroke=\"currentColor\"/>")
	arrow(522, 126)
	box(524, 100, 76, 52, "Grafana", ":3000", false)
	b.WriteString("<rect x=\"144\" y=\"242\" width=\"128\" height=\"60\" fill=\"none\" stroke=\"currentColor\" stroke-width=\"2\"/><rect x=\"148\" y=\"246\" width=\"120\" height=\"52\" fill=\"var(--surface)\" stroke=\"currentColor\" stroke-width=\"1.5\"/><text x=\"158\" y=\"267\" font-size=\"14\">extent report</text><text x=\"158\" y=\"284\" font-size=\"11\" class=\"mono dim\">this report</text>\n")
	b.WriteString("<line x1=\"0\" y1=\"318\" x2=\"28\" y2=\"318\" stroke=\"currentColor\" stroke-width=\"1.5\"/><text x=\"34\" y=\"322\" font-size=\"11\" class=\"dim\">telemetry</text><line x1=\"110\" y1=\"318\" x2=\"138\" y2=\"318\" stroke=\"currentColor\" stroke-width=\"1.5\" stroke-dasharray=\"4 4\"/><text x=\"144\" y=\"322\" font-size=\"11\" class=\"dim\">evidence queries</text><text x=\"600\" y=\"322\" text-anchor=\"end\" font-size=\"11\" class=\"dim\">swatch = this report's source status</text>\n")
	b.WriteString("</svg>\n")
	return b.String()
}
