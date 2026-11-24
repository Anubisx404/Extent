package reporter

// Figures and small formatting helpers for the HTML dashboard (html.go).

import (
	"encoding/base64"
	"fmt"
	"html"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/Anubisx404/Extent/internal/evidence"
)

func iconDataURI() string {
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(appIcon)
}

// warningHeadline shortens a warning for the one-line banner: the query
// description and the final cause, without the repeated encoded URL. The full
// text stays in the disclosure below it.
func warningHeadline(warning string) string {
	head, rest, found := strings.Cut(warning, ": ")
	if !found || len(warning) <= 110 {
		return warning
	}
	cause := rest
	if i := strings.LastIndex(rest, ": "); i >= 0 {
		cause = rest[i+2:]
	}
	if host := hostOf(rest); host != "" {
		return head + " — " + host + ": " + cause
	}
	return head + ": " + cause
}

func hostOf(s string) string {
	for _, scheme := range []string{"http://", "https://"} {
		if i := strings.Index(s, scheme); i >= 0 {
			hostPath := s[i+len(scheme):]
			if j := strings.IndexAny(hostPath, "/?\" "); j >= 0 {
				hostPath = hostPath[:j]
			}
			return hostPath
		}
	}
	return ""
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// chip renders a status as a pattern swatch plus its word.
func chip(status string) string {
	if strings.TrimSpace(status) == "" {
		status = "unknown"
	}
	class := strings.ReplaceAll(strings.ToLower(status), " ", "-")
	switch class {
	case "measured", "missing", "unavailable", "not-collected":
	default:
		class = "other"
	}
	return fmt.Sprintf("<span class=\"chip chip-%s\"><span class=\"st st-%s\" aria-hidden=\"true\"></span>%s</span>", class, class, html.EscapeString(status))
}

func statusLegend() string {
	return "<ul class=\"legend\" aria-label=\"Status patterns\"><li><span class=\"st st-measured\"></span>measured</li><li><span class=\"st st-missing\"></span>missing</li><li><span class=\"st st-unavailable\"></span>unavailable</li><li><span class=\"st st-not-collected\"></span>not collected</li></ul>\n"
}

func sourceHint(source, status string) string {
	flag := map[string]string{"tempo": "--tempo http://localhost:3200", "loki": "--loki http://localhost:3100"}[source]
	name := map[string]string{"tempo": "Tempo", "loki": "Loki"}[source]
	if status == "unavailable" {
		return name + " could not be queried. Start the stack (extent stack up) and re-run."
	}
	return name + " was not queried. Re-run with " + flag + "."
}

// sourceStatus reports how a backend contributed evidence. A source with no
// provenance entry was never queried, which differs from one that answered
// with nothing ("missing") or failed ("unavailable").
func sourceStatus(result evidence.Result, source string) string {
	status := ""
	for _, item := range result.Provenance {
		if item.Source != source {
			continue
		}
		if item.Status == "unavailable" {
			return "unavailable"
		}
		if status == "" || item.Status == "measured" {
			status = item.Status
		}
	}
	if status == "" {
		return "not collected"
	}
	return status
}

func latestRetrieval(result evidence.Result) time.Time {
	var latest time.Time
	for _, item := range result.Provenance {
		if item.RetrievedAt.After(latest) {
			latest = item.RetrievedAt
		}
	}
	return latest
}

func rerunCommand(report Report, window string) string {
	service := report.ServiceName
	if service == "" {
		service = "<service.name>"
	} else if strings.IndexFunc(service, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("._-", r))
	}) >= 0 {
		service = "'" + strings.ReplaceAll(service, "'", `'\''`) + "'"
	}
	return strings.Join([]string{"extent report", "  --service " + service, "  --last " + window, "  --tempo http://localhost:3200", "  --loki http://localhost:3100", "  --format html"}, "\n")
}

func baselineValue(status string, value float64, unit string) string {
	if status != "measured" {
		if status == "" {
			return "-"
		}
		return status
	}
	return formatUnit(value, unit)
}

// formatUnit prints a value in its own unit: 316.5 ms, 0.201 s, 29%.
func formatUnit(value float64, unit string) string {
	switch unit {
	case "ratio":
		return fmt.Sprintf("%.0f%%", value*100)
	case "", "count":
		return fmt.Sprintf("%.0f", value)
	case "milliseconds", "ms":
		return fmt.Sprintf("%.1f ms", value)
	case "seconds", "s":
		return fmt.Sprintf("%.3f s", value)
	}
	return fmt.Sprintf("%.3f %s", value, unit)
}

// formatChange prints a signed change in the measurement's unit; ratios are
// percentage points.
func formatChange(change float64, unit string) string {
	switch unit {
	case "ratio":
		return fmt.Sprintf("%+.0f pts", change*100)
	case "milliseconds", "ms":
		return fmt.Sprintf("%+.1f ms", change)
	case "seconds", "s":
		return fmt.Sprintf("%+.3f s", change)
	case "", "count":
		return fmt.Sprintf("%+.0f", change)
	}
	return fmt.Sprintf("%+.3f %s", change, unit)
}

func bytesPerSecond(v float64) string {
	switch {
	case v <= 0:
		return "0 B/s"
	case v >= 1<<20:
		return fmt.Sprintf("%.1f MB/s", v/(1<<20))
	case v >= 1<<10:
		return fmt.Sprintf("%.1f KB/s", v/(1<<10))
	}
	return fmt.Sprintf("%.0f B/s", v)
}

func toMillis(m Measurement) (float64, bool) {
	if m.Status != "measured" || m.Value <= 0 {
		return 0, false
	}
	switch m.Unit {
	case "seconds":
		return m.Value * 1000, true
	case "milliseconds", "ms":
		return m.Value, true
	}
	return 0, false
}

// latencyRuler draws HTTP p95 (solid marker, above) and DB p95 (hollow
// marker, below) on a log10 millisecond scale.
func latencyRuler(measurements map[string]Measurement) string {
	httpMS, hasHTTP := toMillis(measurements["httpP95"])
	dbMS, hasDB := toMillis(measurements["dbP95"])
	hi := 3.0
	for _, v := range []float64{httpMS, dbMS} {
		if v > 0 {
			hi = math.Max(hi, math.Ceil(math.Log10(v*1.5)))
		}
	}
	hi = math.Min(hi, 6)
	const x0, x1 = 12.0, 628.0
	x := func(v float64) float64 {
		l := math.Log10(math.Max(v, 1))
		return x0 + l/hi*(x1-x0)
	}
	var b strings.Builder
	b.WriteString("<svg viewBox=\"0 0 640 120\" role=\"img\" aria-label=\"Latency scale\">\n")
	if !hasHTTP {
		status := measurements["httpP95"].Status
		if status == "" {
			status = "not collected"
		}
		// The empty band takes the status pattern from the key.
		switch status {
		case "unavailable":
			fmt.Fprintf(&b, "<rect x=\"%.0f\" y=\"40\" width=\"%.0f\" height=\"24\" fill=\"none\" stroke=\"currentColor\" stroke-width=\"1.5\"/><path d=\"M%.0f 40 L%.0f 64 M%.0f 64 L%.0f 40\" stroke=\"currentColor\" stroke-width=\"1\"/>\n", x0, x1-x0, x0, x1, x0, x1)
		case "missing":
			fmt.Fprintf(&b, "<rect x=\"%.0f\" y=\"40\" width=\"%.0f\" height=\"24\" fill=\"url(#hatch-fine)\" style=\"color:var(--ink-3)\"/>\n", x0, x1-x0)
		default:
			fmt.Fprintf(&b, "<rect x=\"%.0f\" y=\"40\" width=\"%.0f\" height=\"24\" fill=\"none\" stroke=\"var(--ink-2)\" stroke-width=\"1.5\" stroke-dasharray=\"5 4\"/>\n", x0, x1-x0)
		}
		label := "no sample to place"
		if status == "unavailable" {
			label = "Prometheus could not be queried"
		}
		fmt.Fprintf(&b, "<text x=\"320\" y=\"30\" text-anchor=\"middle\" class=\"dim\" font-size=\"13\">HTTP p95 %s · %s</text>\n", html.EscapeString(status), label)
	}
	fmt.Fprintf(&b, "<line x1=\"%.0f\" y1=\"64\" x2=\"%.0f\" y2=\"64\" stroke=\"currentColor\" stroke-width=\"2\"/>\n", x0, x1)
	units := []string{"1 ms", "10 ms", "100 ms", "1 s", "10 s", "100 s", "1000 s"}
	for d := 0.0; d <= hi; d++ {
		xd := x0 + d/hi*(x1-x0)
		fmt.Fprintf(&b, "<line x1=\"%.1f\" y1=\"56\" x2=\"%.1f\" y2=\"72\" stroke=\"currentColor\" stroke-width=\"1.5\"/><text x=\"%.1f\" y=\"114\" text-anchor=\"middle\" class=\"mono dim\" font-size=\"11\">%s</text>\n", xd, xd, xd, units[int(d)])
		if d < hi {
			for m := 2; m <= 9; m++ {
				xm := x0 + (d+math.Log10(float64(m)))/hi*(x1-x0)
				fmt.Fprintf(&b, "<line x1=\"%.1f\" y1=\"60\" x2=\"%.1f\" y2=\"68\" stroke=\"currentColor\" stroke-width=\"1\" opacity=\".5\"/>\n", xm, xm)
			}
		}
	}
	anchor := func(px float64) string {
		switch {
		case px < 140:
			return "start"
		case px > 500:
			return "end"
		}
		return "middle"
	}
	if hasHTTP {
		px := x(httpMS)
		fmt.Fprintf(&b, "<rect x=\"%.0f\" y=\"60\" width=\"%.1f\" height=\"8\" fill=\"currentColor\" opacity=\".85\"/>\n", x0, px-x0)
		fmt.Fprintf(&b, "<path d=\"M%.1f 54 l-7 -12 h14 z\" fill=\"currentColor\"/><text x=\"%.1f\" y=\"34\" text-anchor=\"%s\" font-size=\"13\"><tspan class=\"num\" font-size=\"28\">%s</tspan> HTTP p95</text>\n",
			px, px, anchor(px), html.EscapeString(formatMillis(httpMS)))
	}
	if hasDB {
		px := x(dbMS)
		fmt.Fprintf(&b, "<path d=\"M%.1f 74 l-6 11 h12 z\" fill=\"var(--surface)\" stroke=\"currentColor\" stroke-width=\"1.5\"/><text x=\"%.1f\" y=\"85\" text-anchor=\"end\" font-size=\"12\" class=\"dim\">DB p95 %s</text>\n",
			px, px-10, html.EscapeString(formatMillis(dbMS)))
	}
	b.WriteString("</svg>\n")
	return b.String()
}

func formatMillis(ms float64) string {
	if ms >= 1000 {
		return fmt.Sprintf("%.2f s", ms/1000)
	}
	return fmt.Sprintf("%.1f ms", ms)
}

// signalPath draws the telemetry pipeline Extent generates (service → OTel
// Collector → Prometheus, Tempo, Loki) with each backend in its evidence state.
func signalPath(report Report, tempo, loki string) string {
	ev := report.Evidence
	prom := "not collected"
	if m, ok := report.Measurements["httpP95"]; ok {
		prom = m.Status
	}
	if s := sourceStatus(ev, "prometheus"); s == "unavailable" {
		prom = s
	}
	measured := 0
	for _, m := range report.Measurements {
		if m.Status == "measured" {
			measured++
		}
	}
	promLine := fmt.Sprintf("%d/%d measurements", measured, len(report.Measurements))
	tempoLine := fmt.Sprintf("%d traces · %d spans", len(ev.Traces), len(ev.Spans))
	lokiLine := fmt.Sprintf("%d logs · %d anomalies", len(ev.Logs), len(ev.LogAnomalies))
	if prom == "unavailable" {
		promLine = "unavailable"
	}
	if tempo == "not collected" || tempo == "unavailable" {
		tempoLine = tempo
	}
	if loki == "not collected" || loki == "unavailable" {
		lokiLine = loki
	}
	service := report.ServiceName
	if service == "" {
		service = "no service"
	}
	if len(service) > 14 {
		service = service[:13] + "…"
	}
	var b strings.Builder
	b.WriteString("<svg viewBox=\"0 0 520 196\" role=\"img\" aria-label=\"Telemetry signal path\">\n")
	// source and collector
	fmt.Fprintf(&b, "<rect x=\"1\" y=\"74\" width=\"118\" height=\"48\" fill=\"var(--surface)\" stroke=\"currentColor\" stroke-width=\"2\"/><text x=\"11\" y=\"93\" font-size=\"11\" class=\"dim\">SERVICE</text><text x=\"11\" y=\"111\" font-size=\"14\" class=\"mono\">%s</text>\n", html.EscapeString(service))
	b.WriteString("<line x1=\"120\" y1=\"98\" x2=\"146\" y2=\"98\" stroke=\"currentColor\" stroke-width=\"1.5\"/><path d=\"M146 98 l-7 -4 v8 z\" fill=\"currentColor\"/>\n")
	b.WriteString("<rect x=\"148\" y=\"74\" width=\"104\" height=\"48\" fill=\"var(--surface)\" stroke=\"currentColor\" stroke-width=\"2\"/><text x=\"158\" y=\"93\" font-size=\"11\" class=\"dim\">OTLP :4318</text><text x=\"158\" y=\"111\" font-size=\"14\">Collector</text>\n")
	nodes := []struct {
		name, status, line string
		y                  float64
	}{{"Prometheus", prom, promLine, 6}, {"Tempo", tempo, tempoLine, 76}, {"Loki", loki, lokiLine, 146}}
	for _, n := range nodes {
		cy := n.y + 22
		dash := ""
		if n.status == "not collected" || n.status == "unavailable" {
			dash = " stroke-dasharray=\"4 4\""
		}
		fmt.Fprintf(&b, "<path d=\"M253 98 H270 V%.0f H288\" fill=\"none\" stroke=\"currentColor\" stroke-width=\"1.5\"%s/><path d=\"M288 %.0f l-7 -4 v8 z\" fill=\"currentColor\"/>\n", cy, dash, cy)
		border := "stroke-width=\"2\""
		if n.status == "not collected" {
			border = "stroke-width=\"1.5\" stroke-dasharray=\"5 4\""
		}
		fmt.Fprintf(&b, "<rect x=\"290\" y=\"%.0f\" width=\"228\" height=\"44\" fill=\"var(--surface)\" stroke=\"currentColor\" %s/>\n", n.y, border)
		b.WriteString(swatchSVG(298, n.y+8, 28, n.status))
		fmt.Fprintf(&b, "<text x=\"336\" y=\"%.0f\" font-size=\"14\">%s</text><text x=\"336\" y=\"%.0f\" font-size=\"12\" class=\"mono dim\">%s</text>\n", n.y+19, n.name, n.y+36, html.EscapeString(n.line))
	}
	b.WriteString("</svg>\n")
	return b.String()
}

// swatchSVG draws a status pattern square.
func swatchSVG(x, y, size float64, status string) string {
	switch status {
	case "measured":
		return fmt.Sprintf("<rect x=\"%.0f\" y=\"%.0f\" width=\"%.0f\" height=\"%.0f\" fill=\"currentColor\"/>\n", x, y, size, size)
	case "missing":
		return fmt.Sprintf("<rect x=\"%.0f\" y=\"%.0f\" width=\"%.0f\" height=\"%.0f\" fill=\"url(#hatch)\" stroke=\"currentColor\" stroke-width=\"1.5\"/>\n", x, y, size, size)
	case "unavailable":
		return fmt.Sprintf("<rect x=\"%.0f\" y=\"%.0f\" width=\"%.0f\" height=\"%.0f\" fill=\"none\" stroke=\"currentColor\" stroke-width=\"1.5\"/><path d=\"M%.0f %.0f l%.0f %.0f M%.0f %.0f l%.0f -%.0f\" stroke=\"currentColor\" stroke-width=\"1.5\"/>\n",
			x, y, size, size, x, y, size, size, x, y+size, size, size)
	}
	return fmt.Sprintf("<rect x=\"%.0f\" y=\"%.0f\" width=\"%.0f\" height=\"%.0f\" fill=\"none\" stroke=\"currentColor\" stroke-width=\"1.5\" stroke-dasharray=\"4 3\"/>\n", x, y, size, size)
}

// spanAnatomy shows each span of the slowest returned trace as a share of
// that trace's longest span. Tempo evidence has no start offsets, so this is a
// duration comparison, not a timeline.
func spanAnatomy(result evidence.Result) string {
	slowest, slowestDur := "", -1.0
	for _, trace := range result.Traces {
		if trace.DurationMS > slowestDur {
			slowest, slowestDur = trace.TraceID, trace.DurationMS
		}
	}
	var spans []evidence.SpanEvidence
	for _, span := range result.Spans {
		if span.TraceID == slowest {
			spans = append(spans, span)
		}
	}
	scope := ""
	if len(spans) == 0 {
		spans = append(spans, result.Spans...)
		scope = "all returned spans"
	} else {
		scope = fmt.Sprintf("trace %s · %d of %d returned spans", shortID(slowest), len(spans), len(result.Spans))
	}
	sort.SliceStable(spans, func(i, j int) bool { return spans[i].DurationMS > spans[j].DurationMS })
	if len(spans) > 8 {
		spans = spans[:8]
	}
	root := spans[0].DurationMS
	if root <= 0 {
		root = 1
	}
	const labelW, barX, barW, rowH = 210.0, 214.0, 330.0, 30.0
	h := float64(len(spans))*rowH + 44
	var b strings.Builder
	fmt.Fprintf(&b, "<svg viewBox=\"0 0 640 %.0f\" role=\"img\" aria-label=\"Span durations as share of the longest span\">\n", h)
	fmt.Fprintf(&b, "<text x=\"0\" y=\"12\" font-size=\"11\" class=\"mono dim\">%s · not a timeline</text>\n", html.EscapeString(scope))
	for _, q := range []float64{0, .25, .5, .75, 1} {
		xq := barX + q*barW
		fmt.Fprintf(&b, "<line x1=\"%.1f\" y1=\"22\" x2=\"%.1f\" y2=\"%.0f\" stroke=\"var(--rule)\" stroke-width=\"1\"/><text x=\"%.1f\" y=\"%.0f\" text-anchor=\"middle\" font-size=\"11\" class=\"mono dim\">%.0f%%</text>\n", xq, xq, h-16, xq, h-2, q*100)
	}
	for i, span := range spans {
		y := 26 + float64(i)*rowH
		name := span.Name
		if len(name) > 30 {
			name = name[:29] + "…"
		}
		share := math.Min(span.DurationMS/root, 1)
		fill := "currentColor"
		switch {
		case i == 0:
			fill = "currentColor"
		case span.Attributes["db.system"] != "" || span.Attributes["db.system.name"] != "":
			fill = "url(#dots)"
		default:
			fill = "var(--ink-3)"
		}
		fmt.Fprintf(&b, "<text x=\"0\" y=\"%.0f\" font-size=\"12\" class=\"mono\"><title>%s</title>%s</text>\n", y+15, html.EscapeString(span.Name), html.EscapeString(name))
		fmt.Fprintf(&b, "<rect x=\"%.0f\" y=\"%.0f\" width=\"%.1f\" height=\"16\" fill=\"%s\" stroke=\"currentColor\" stroke-width=\"1\"/>\n", barX, y+3, math.Max(share*barW, 2), fill)
		fmt.Fprintf(&b, "<text x=\"640\" y=\"%.0f\" text-anchor=\"end\" font-size=\"12\"><tspan class=\"num\" font-size=\"15\">%.1f</tspan> ms · %.0f%%</text>\n", y+16, span.DurationMS, share*100)
	}
	b.WriteString("</svg>\n")
	b.WriteString("<ul class=\"legend\"><li><span class=\"st st-measured\"></span>longest span</li><li><span class=\"st\" style=\"background:radial-gradient(circle,var(--ink) 1px,transparent 1.2px) 0 0/4px 4px\"></span>database span</li><li><span class=\"st\" style=\"background:var(--ink-3)\"></span>other span</li></ul>\n")
	return b.String()
}

func shortID(id string) string {
	if len(id) > 12 {
		return id[:12] + "…"
	}
	return id
}

// waffle draws a ratio measurement as a 10×10 grid: one filled cell per
// percentage point.
func waffle(label string, m Measurement) string {
	var b strings.Builder
	b.WriteString("<figure style=\"margin:0\">\n<svg viewBox=\"0 0 110 110\" role=\"img\" aria-label=\"" + html.EscapeString(label) + " CPU\">\n")
	filled := -1
	if m.Status == "measured" {
		filled = int(math.Round(math.Max(0, math.Min(m.Value, 1)) * 100))
	}
	for i := 0; i < 100; i++ {
		col, row := i%10, 9-i/10
		x, y := float64(col)*11, float64(row)*11
		switch {
		case filled < 0:
			fmt.Fprintf(&b, "<rect x=\"%.0f\" y=\"%.0f\" width=\"9\" height=\"9\" fill=\"none\" stroke=\"var(--ink-3)\" stroke-width=\"1\" stroke-dasharray=\"2 2\"/>", x, y)
		case i < filled:
			fmt.Fprintf(&b, "<rect x=\"%.0f\" y=\"%.0f\" width=\"9\" height=\"9\" fill=\"currentColor\"/>", x, y)
		default:
			fmt.Fprintf(&b, "<rect x=\"%.0f\" y=\"%.0f\" width=\"9\" height=\"9\" fill=\"none\" stroke=\"var(--rule)\" stroke-width=\"1\"/>", x, y)
		}
	}
	if m.Status == "unavailable" {
		// the key's crossed box, drawn over the empty grid
		b.WriteString("<rect x=\"0\" y=\"0\" width=\"108\" height=\"108\" fill=\"none\" stroke=\"currentColor\" stroke-width=\"1.5\"/><path d=\"M0 0 L108 108 M0 108 L108 0\" stroke=\"currentColor\" stroke-width=\"1.5\"/>")
	}
	b.WriteString("\n</svg>\n")
	value := m.Status
	if value == "" {
		value = "not collected"
	}
	style := "font:600 var(--t-sm)/1.2 var(--text);color:var(--ink-2)"
	if m.Status == "measured" {
		value = fmt.Sprintf("%.0f%%", m.Value*100)
		style = "font:600 var(--t-lg)/1 var(--display)"
	}
	fmt.Fprintf(&b, "<figcaption style=\"display:flex;justify-content:space-between;align-items:baseline;gap:8px;margin-top:6px\"><span class=\"label\">%s</span><b style=\"%s\">%s</b></figcaption>\n</figure>\n", html.EscapeString(label), style, html.EscapeString(value))
	return b.String()
}

// poolSlots draws DB connection pool occupancy: active (solid) and idle
// (hollow) slots inside the pool, waiting requests (hatched) beyond it.
func poolSlots(active, idle, waiting float64) string {
	a, i, wt := int(math.Round(active)), int(math.Round(idle)), int(math.Round(waiting))
	total := a + i + wt
	if total == 0 {
		return "<p class=\"state-note\">No DB pool series were reported.</p>\n"
	}
	const maxSlots = 30
	scale := 1.0
	if total > maxSlots {
		scale = float64(maxSlots) / float64(total)
	}
	sa, si, sw := int(math.Ceil(float64(a)*scale)), int(math.Ceil(float64(i)*scale)), int(math.Ceil(float64(wt)*scale))
	slot := 16.0
	width := float64(sa+si+sw)*(slot+4) + 20
	var b strings.Builder
	fmt.Fprintf(&b, "<svg viewBox=\"0 0 %.0f 58\" role=\"img\" aria-label=\"DB pool: %d active, %d idle, %d waiting\" style=\"max-width:%.0fpx\">\n", width, a, i, wt, width*1.6)
	x := 0.0
	for k := 0; k < sa; k++ {
		fmt.Fprintf(&b, "<rect x=\"%.0f\" y=\"8\" width=\"%.0f\" height=\"%.0f\" fill=\"currentColor\"/>", x, slot, slot*1.6)
		x += slot + 4
	}
	for k := 0; k < si; k++ {
		fmt.Fprintf(&b, "<rect x=\"%.0f\" y=\"8\" width=\"%.0f\" height=\"%.0f\" fill=\"none\" stroke=\"currentColor\" stroke-width=\"1.5\"/>", x, slot, slot*1.6)
		x += slot + 4
	}
	if sw > 0 {
		fmt.Fprintf(&b, "<line x1=\"%.0f\" y1=\"0\" x2=\"%.0f\" y2=\"%.0f\" stroke=\"currentColor\" stroke-width=\"2\"/>", x+6, x+6, 8+slot*1.6+8)
		x += 16
		for k := 0; k < sw; k++ {
			fmt.Fprintf(&b, "<rect x=\"%.0f\" y=\"8\" width=\"%.0f\" height=\"%.0f\" fill=\"url(#hatch)\" stroke=\"currentColor\" stroke-width=\"1.5\"/>", x, slot, slot*1.6)
			x += slot + 4
		}
	}
	b.WriteString("\n</svg>\n")
	fmt.Fprintf(&b, "<ul class=\"legend\"><li><span class=\"st st-measured\"></span>%d active</li><li><span class=\"st\"></span>%d idle</li><li><span class=\"st st-missing\"></span>%d waiting beyond the pool</li></ul>\n", a, i, wt)
	if scale < 1 {
		fmt.Fprintf(&b, "<p class=\"state-note\">Scaled: one slot ≈ %.1f connections.</p>\n", 1/scale)
	}
	return b.String()
}

type tally struct {
	label  string
	count  int
	status string
}

// tallies draws each evidence count as a row of marks (one per item, capped)
// so absent sources read as a pattern, not a zero.
func tallies(rows []tally) string {
	const maxMarks = 24
	var b strings.Builder
	h := float64(len(rows))*22 + 4
	fmt.Fprintf(&b, "<svg viewBox=\"0 0 360 %.0f\" role=\"img\" aria-label=\"Evidence counts\">\n", h)
	for r, row := range rows {
		y := float64(r)*22 + 2
		fmt.Fprintf(&b, "<text x=\"0\" y=\"%.0f\" font-size=\"12\">%s</text>\n", y+13, html.EscapeString(row.label))
		switch row.status {
		case "not collected", "unavailable":
			b.WriteString(swatchSVG(118, y+2, 14, row.status))
			fmt.Fprintf(&b, "<text x=\"140\" y=\"%.0f\" font-size=\"11\" class=\"dim\">%s</text><text x=\"360\" y=\"%.0f\" text-anchor=\"end\" class=\"num\" font-size=\"15\">—</text>\n", y+13, row.status, y+14)
			continue
		}
		marks := row.count
		if marks > maxMarks {
			marks = maxMarks
		}
		for k := 0; k < marks; k++ {
			fmt.Fprintf(&b, "<rect x=\"%d\" y=\"%.0f\" width=\"6\" height=\"14\" fill=\"currentColor\"/>", 118+k*8, y+2)
		}
		if row.count > maxMarks {
			fmt.Fprintf(&b, "<text x=\"%d\" y=\"%.0f\" font-size=\"11\" class=\"dim\">+%d</text>", 118+maxMarks*8+2, y+13, row.count-maxMarks)
		}
		if row.count == 0 {
			fmt.Fprintf(&b, "<line x1=\"118\" y1=\"%.0f\" x2=\"150\" y2=\"%.0f\" stroke=\"var(--ink-3)\" stroke-width=\"1\"/>", y+9, y+9)
		}
		fmt.Fprintf(&b, "<text x=\"360\" y=\"%.0f\" text-anchor=\"end\" class=\"num\" font-size=\"15\">%d</text>\n", y+14, row.count)
	}
	b.WriteString("</svg>\n")
	return b.String()
}

// statusStrip draws soak responses as one 100% strip split by status class.
func statusStrip(dist map[int]int) string {
	if len(dist) == 0 {
		return "<p class=\"state-note\">No responses were recorded.</p>\n"
	}
	classes := map[string]int{}
	total := 0
	for code, count := range dist {
		classes[fmt.Sprintf("%dxx", code/100)] += count
		total += count
	}
	keys := make([]string, 0, len(classes))
	for k := range classes {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString("<svg viewBox=\"0 0 640 56\" role=\"img\" aria-label=\"Responses by status class\">\n")
	x := 0.0
	for _, k := range keys {
		wd := float64(classes[k]) / float64(total) * 640
		fill := "currentColor"
		switch k {
		case "4xx":
			fill = "url(#hatch)"
		case "5xx":
			fill = "var(--ink-3)"
		case "1xx", "3xx":
			fill = "var(--surface)"
		}
		fmt.Fprintf(&b, "<rect x=\"%.1f\" y=\"0\" width=\"%.1f\" height=\"24\" fill=\"%s\" stroke=\"currentColor\" stroke-width=\"1\"/><text x=\"%.1f\" y=\"44\" font-size=\"12\">%s · %d (%.0f%%)</text>\n", x, wd, fill, x+2, k, classes[k], float64(classes[k])/float64(total)*100)
		x += wd
	}
	b.WriteString("</svg>\n")
	return b.String()
}
