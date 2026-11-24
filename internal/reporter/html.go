package reporter

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"html"
	"sort"
	"strings"
	"time"

	"github.com/Anubisx404/Extent/internal/evidence"
)

// appIcon is assets/app_icon.png downscaled to 96 px and converted to
// greyscale for the monochrome report. It is embedded so the report stays a
// single self-contained file that opens offline.
//
//go:embed brand/app_icon_96.png
var appIcon []byte

// reportCSS is the monochrome desktop dashboard stylesheet (design viewport
// 1920x1080, see docs/design/dashboard-spec.md). Status is carried by fill
// pattern and words, never by hue: solid = measured, hatched = missing,
// crossed = unavailable, dashed outline = not collected. Tabs are separate
// pages switched with :target, so the report needs no JavaScript.
const reportCSS = `:root{
--paper:oklch(0.965 0.008 85);--surface:oklch(0.985 0.005 85);--paper-2:oklch(0.93 0.009 85);
--ink:oklch(0.2 0.006 85);--ink-2:oklch(0.4 0.006 85);--ink-3:oklch(0.62 0.006 85);--rule:oklch(0.84 0.008 85);
--hatch:repeating-linear-gradient(135deg,var(--ink) 0 1.5px,transparent 1.5px 5px);
--display:"Bahnschrift","DIN Alternate","D-DIN","Barlow Semi Condensed","Arial Narrow",sans-serif;
--text:"Segoe UI Variable Text","Segoe UI","Helvetica Neue",Helvetica,sans-serif;
--mono:"Cascadia Mono","SF Mono",Consolas,ui-monospace,monospace;
--space-2xs:4px;--space-xs:8px;--space-sm:12px;--space-md:16px;--space-lg:24px;--space-xl:32px;--space-2xl:48px;
--t-xs:.6875rem;--t-sm:.8125rem;--t-md:1rem;--t-lg:1.25rem;--t-xl:1.75rem;--t-2xl:2.25rem;--t-3xl:3rem;
--bar:72px;--rail:264px}
*{box-sizing:border-box}
html{background:var(--paper)}
body{margin:0;min-width:1440px;color:var(--ink);background:var(--paper);font:400 var(--t-md)/1.5 var(--text)}
a{color:inherit;text-underline-offset:3px;text-decoration-thickness:1px}
a:hover{text-decoration-thickness:2px}
:focus-visible{outline:2px solid var(--ink);outline-offset:2px}
.skip{position:absolute;left:-9999px}.skip:focus{left:var(--space-md);top:var(--space-md);background:var(--ink);color:var(--paper);padding:var(--space-xs) var(--space-sm);z-index:9}
code,.mono{font-family:var(--mono);font-size:.86em}
.label{font:600 var(--t-xs)/1.3 var(--text);letter-spacing:.08em;text-transform:uppercase;color:var(--ink-2)}
.fig{font:600 var(--t-xs)/1.3 var(--mono);letter-spacing:.04em;text-transform:uppercase;color:var(--ink-2);margin:0}
.dash{max-width:1920px;margin:0 auto;display:grid;grid-template-columns:var(--rail) minmax(0,1fr);grid-template-rows:var(--bar) auto;min-height:100vh;background:var(--paper)}
.bar{grid-column:1/-1;display:grid;grid-template-columns:var(--rail) minmax(0,1fr) auto;background:var(--ink);color:var(--paper);position:sticky;top:0;z-index:3}
.brand{display:flex;align-items:center;gap:var(--space-sm);padding:0 var(--space-lg)}
.brand img{width:40px;height:40px;image-rendering:pixelated;border:1px solid var(--ink-3)}
.brand b{font:600 var(--t-lg)/1 var(--display);letter-spacing:.24em}
.brand span{display:block;font:var(--t-xs)/1.3 var(--text);letter-spacing:.08em;text-transform:uppercase;opacity:.75;margin-top:4px}
.bar-facts{display:flex;gap:var(--space-2xl);align-items:center;margin:0;padding:0 var(--space-xl);border-left:1px solid var(--ink-2)}
.bar-facts dt{font:600 var(--t-xs)/1.2 var(--text);letter-spacing:.08em;text-transform:uppercase;opacity:.7}
.bar-facts dd{margin:2px 0 0;font:500 var(--t-md)/1.2 var(--display);letter-spacing:.02em;white-space:nowrap}
.health{display:flex;align-items:center;gap:var(--space-sm);padding:0 var(--space-xl);border-left:1px solid var(--ink-2)}
.health .sw{width:28px;height:28px;border:2px solid var(--paper)}
.health-green .sw{background:var(--paper)}
.health-yellow .sw{background:repeating-linear-gradient(135deg,var(--paper) 0 2px,transparent 2px 6px)}
.health-unknown .sw{border-style:dashed}
.health b{display:block;font:600 var(--t-xl)/1 var(--display);text-transform:uppercase;letter-spacing:.06em}
.rail{position:sticky;top:var(--bar);height:calc(100vh - var(--bar));align-self:start;display:flex;flex-direction:column;background:var(--surface);border-right:1px solid var(--ink);overflow-y:auto}
.rail ol{list-style:none;margin:0;padding:0}
.rail a{display:grid;grid-template-columns:32px minmax(0,1fr) 12px;align-items:center;height:48px;padding:0 var(--space-lg);border-bottom:1px solid var(--rule);text-decoration:none;font:500 var(--t-md)/1 var(--text)}
.rail a .i{font:600 var(--t-xs)/1 var(--mono);color:var(--ink-2)}
.rail a:hover{background:var(--paper-2)}
%NAV_RULES%{background:var(--ink);color:var(--paper)}
%NAV_RULES_I%{color:var(--paper)}
%NAV_RULES_ST%{--ink:var(--paper);--hatch:repeating-linear-gradient(135deg,var(--paper) 0 1.5px,transparent 1.5px 5px);border-color:var(--paper)}
.rail .key{padding:var(--space-lg);margin-top:auto;border-top:1px solid var(--rule)}
.rail .key .legend{flex-direction:column;gap:var(--space-xs)}
.rail .foot{margin:0;padding:var(--space-lg);border-top:1px solid var(--rule);font:var(--t-xs)/1.5 var(--text);letter-spacing:.06em;text-transform:uppercase;color:var(--ink-2)}
main{padding:var(--space-xl);min-width:0}
.page{scroll-margin-top:calc(var(--bar) + var(--space-xl))}
.page{display:none}
.page:target,main:not(:has(.page:target)) #overview{display:block}
.page-head{display:grid;grid-template-columns:auto minmax(0,1fr) auto;align-items:baseline;gap:var(--space-lg);padding-bottom:var(--space-md);border-bottom:2px solid var(--ink);margin-bottom:var(--space-lg)}
.page-head .n{font:600 var(--t-md)/1.4 var(--mono);color:var(--ink-2)}
.page-head h2{margin:0;font:600 var(--t-2xl)/1.05 var(--display)}
.page-head p{margin:var(--space-2xs) 0 0;color:var(--ink-2);max-width:80ch}
.alert{display:grid;grid-template-columns:220px minmax(0,1fr);border:2px solid var(--ink);margin-bottom:var(--space-lg);background:var(--surface)}
.alert-key{background:var(--hatch);border-right:2px solid var(--ink);padding:var(--space-sm);display:flex;align-items:center}
.alert-key p{display:inline-block;margin:0;background:var(--surface);border:1px solid var(--ink);padding:var(--space-2xs) var(--space-xs);font:600 var(--t-sm)/1.3 var(--display);letter-spacing:.06em;text-transform:uppercase}
.alert-body{min-width:0;align-self:center;padding:var(--space-sm) var(--space-lg)}
.alert ul{margin:0;padding:0 0 0 var(--space-md);font:var(--t-sm)/1.6 var(--mono);max-height:8.2em;overflow-y:auto}
.alert li{white-space:nowrap;overflow:hidden;text-overflow:ellipsis}
.alert details{border:0;background:none;margin:var(--space-2xs) 0 0}
.alert details>summary{padding:var(--space-2xs) 0;font-weight:500;color:var(--ink-2)}
.alert details[open]>summary{border-bottom:0}
.alert pre{margin:0;padding:var(--space-xs) 0 0;font:var(--t-xs)/1.5 var(--mono);white-space:pre-wrap;overflow-wrap:anywhere}
.board{display:grid;grid-template-columns:repeat(12,minmax(0,1fr));gap:1px;background:var(--ink);border:1px solid var(--ink)}
.board+.board{margin-top:var(--space-lg)}
.panel{background:var(--surface);padding:var(--space-md) var(--space-lg) var(--space-lg);min-width:0;display:flex;flex-direction:column;gap:var(--space-sm)}
.panel h2{margin:0;font:600 var(--t-lg)/1.2 var(--display);letter-spacing:.01em}
.panel h3{margin:var(--space-sm) 0 0}
.panel-head{display:flex;justify-content:space-between;align-items:baseline;gap:var(--space-md)}
.more{margin-top:auto;align-self:flex-start;font:600 var(--t-sm)/1.3 var(--text);text-decoration:none;border-bottom:1px solid var(--ink)}
.more:hover{border-bottom-width:2px}
.s12{grid-column:span 12}.s9{grid-column:span 9}.s8{grid-column:span 8}.s7{grid-column:span 7}.s6{grid-column:span 6}.s5{grid-column:span 5}.s4{grid-column:span 4}.s3{grid-column:span 3}
.path{margin:0;font:600 var(--t-3xl)/1 var(--display);letter-spacing:-.01em;overflow-wrap:anywhere}
.path.none{font-size:var(--t-xl);color:var(--ink-2)}
.lede{margin:0;max-width:72ch;color:var(--ink-2)}
svg{display:block;width:100%;height:auto;overflow:visible}
.signal .cap-sm{margin:auto 0}.cap-xs{max-width:440px}.cap-sm{max-width:560px}.cap-md{max-width:760px}.cap-lg{max-width:960px}
svg text{font-family:var(--text);fill:var(--ink)}
svg .num{font-family:var(--display);font-weight:600}
svg .mono{font-family:var(--mono)}
svg .dim{fill:var(--ink-2)}
.legend{display:flex;flex-wrap:wrap;gap:var(--space-xs) var(--space-md);margin:0;padding:0;list-style:none;font:var(--t-sm)/1.3 var(--text);color:var(--ink-2)}
.legend li{display:flex;align-items:center;gap:6px}
.st{display:inline-block;width:12px;height:12px;border:1.5px solid var(--ink);flex:none;vertical-align:-1px}
.st-measured{background:var(--ink)}
.st-missing{background:var(--hatch)}
.st-unavailable{background:linear-gradient(45deg,transparent 45%,var(--ink) 45% 55%,transparent 55%),linear-gradient(-45deg,transparent 45%,var(--ink) 45% 55%,transparent 55%)}
.st-not-collected{border-style:dashed;border-color:var(--ink-2)}
.st-other{background:var(--paper-2)}
.st-none{border-color:transparent}
.chip{display:inline-flex;align-items:center;gap:6px;font:600 var(--t-xs)/1.4 var(--text);letter-spacing:.06em;text-transform:uppercase;white-space:nowrap}
.fixes{list-style:none;margin:0;padding:0;display:flex;flex-direction:column}
.fixes li{display:grid;grid-template-columns:44px minmax(0,1fr);gap:var(--space-xs);padding:var(--space-sm) 0;border-top:1px solid var(--rule)}
.fixes li:first-child{border-top:0;padding-top:0}
.fixes.cols{display:grid;grid-template-columns:repeat(auto-fit,minmax(360px,1fr));gap:var(--space-xl)}
.fixes.cols li{border-top:0;padding:0}
.fixes.vocab li{grid-template-columns:140px minmax(0,1fr)}
.fixes .n{font:600 var(--t-xl)/1 var(--display)}
.fixes strong{display:block;font-weight:600}
.fixes p{margin:2px 0 0;font-size:var(--t-sm);color:var(--ink-2)}
.fixes p.do{color:var(--ink)}
.stats{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:var(--space-sm) var(--space-md);margin:0}
.stats.four{grid-template-columns:repeat(4,minmax(0,1fr))}
.stats.codes{grid-template-columns:48px minmax(0,1fr)}
.stats.codes dd{font:var(--t-sm)/1.4 var(--text)}
.stats div{border-top:1px solid var(--ink);padding-top:var(--space-2xs)}
.stats dt{font:600 var(--t-xs)/1.3 var(--text);letter-spacing:.06em;text-transform:uppercase;color:var(--ink-2)}
.stats dd{margin:0;font:600 var(--t-xl)/1.1 var(--display)}
.pair{display:grid;grid-template-columns:1fr 1fr;gap:var(--space-lg)}
.state-note{display:flex;gap:var(--space-xs);align-items:center;font-size:var(--t-sm);color:var(--ink-2);margin:0}
.state-note.big{font-size:var(--t-md);padding:var(--space-lg) 0}
h3{margin:var(--space-lg) 0 var(--space-xs);font:600 var(--t-sm)/1.3 var(--text);letter-spacing:.06em;text-transform:uppercase;color:var(--ink-2)}
.table-wrap{overflow-x:auto;border:1px solid var(--ink);background:var(--surface)}
table{width:100%;border-collapse:collapse;font-size:var(--t-sm)}
thead th{background:var(--ink);color:var(--paper);font:600 var(--t-xs)/1.3 var(--text);letter-spacing:.08em;text-transform:uppercase;text-align:left;padding:var(--space-xs) var(--space-md);white-space:nowrap}
tbody td{padding:var(--space-sm) var(--space-md);border-top:1px solid var(--rule);vertical-align:top}
tbody tr:first-child td{border-top:0}
td.num{text-align:right;font-family:var(--mono);white-space:nowrap;font-variant-numeric:tabular-nums}
thead th.num{text-align:right}
td.nowrap{white-space:nowrap}
td code{overflow-wrap:break-word}
.muted{color:var(--ink-2)}
details{border:1px solid var(--ink);background:var(--surface)}
details>summary{cursor:pointer;padding:var(--space-sm) var(--space-md);font:600 var(--t-sm)/1.3 var(--text);letter-spacing:.04em}
details[open]>summary{border-bottom:1px solid var(--ink)}
details .table-wrap{border:0}
pre{margin:0;font:var(--t-sm)/1.55 var(--mono);overflow-x:auto}
details pre{padding:var(--space-md);font-size:var(--t-xs)}
.cmd{border:1px solid var(--ink);padding:var(--space-md);background:var(--paper)}
.flow{display:grid;grid-template-columns:44px repeat(4,minmax(0,1fr));border:1px solid var(--ink)}
.flow>div{padding:var(--space-md);border-left:1px solid var(--ink);position:relative}
.flow>div:first-child{border-left:0;display:flex;align-items:center;justify-content:center;padding:var(--space-md) 0}
.flow>div:first-child span{writing-mode:vertical-rl;transform:rotate(180deg);font:600 var(--t-sm)/1 var(--text);letter-spacing:.08em;text-transform:uppercase;color:var(--ink-2)}
.flow h3,.layers h3{margin:2px 0 var(--space-sm);color:var(--ink);font-size:var(--t-sm)}
.flow ol,.layers ul{list-style:none;margin:0;padding:0;display:flex;flex-direction:column;gap:var(--space-xs)}
.flow li{border:1px solid var(--ink);padding:var(--space-xs) var(--space-sm);background:var(--surface)}
.flow li code{display:block;font-weight:600;font-size:var(--t-sm)}
.flow li span{font-size:var(--t-sm);color:var(--ink-2)}
.flow li.here,.layers li.here{outline:2px solid var(--ink);outline-offset:2px;border-color:var(--ink);font-weight:600}
.here::after{content:"this report";display:block;margin-top:2px;font:600 var(--t-xs)/1.3 var(--text);letter-spacing:.08em;text-transform:uppercase}
.layers{display:grid;grid-template-columns:repeat(5,minmax(0,1fr));border:1px solid var(--ink)}
.layers>div{padding:var(--space-md) var(--space-sm);border-left:1px solid var(--ink);min-width:0}
.layers>div:first-child{border-left:0}
.layers li{font:var(--t-sm)/1.3 var(--mono);padding:var(--space-2xs) var(--space-xs);border:1px solid var(--rule)}
.layers li span{display:block;font:var(--t-xs)/1.3 var(--text);color:var(--ink-2)}
.shared{margin:0;border:1px solid var(--ink);border-top:0;padding:var(--space-sm) var(--space-md);font-size:var(--t-sm);color:var(--ink-2)}
.shared code{color:var(--ink)}
@media print{body{min-width:0}.dash{display:block}.bar{position:static}.rail,.skip{display:none}.page{display:block!important;break-before:page}.board{break-inside:avoid}}
`

// svgDefs are the fill patterns shared by every figure.
const svgDefs = `<svg width="0" height="0" style="position:absolute" aria-hidden="true" focusable="false"><defs>
<pattern id="hatch" width="5" height="5" patternUnits="userSpaceOnUse" patternTransform="rotate(45)"><rect width="1.6" height="5" fill="currentColor"/></pattern>
<pattern id="dots" width="4" height="4" patternUnits="userSpaceOnUse"><circle cx="2" cy="2" r="1" fill="currentColor"/></pattern>
<pattern id="hatch-fine" width="3.5" height="3.5" patternUnits="userSpaceOnUse" patternTransform="rotate(45)"><rect width="1" height="3.5" fill="currentColor"/></pattern>
</defs></svg>
`

type htmlSection struct {
	id     string
	title  string
	source string // status shown next to the tab; "" = none
}

// view holds what every tab needs to render.
type view struct {
	report            Report
	ev                evidence.Result
	window            string
	prom, tempo, loki string
	number            map[string]string
	b                 *strings.Builder
}

func (v *view) w(format string, args ...any) { fmt.Fprintf(v.b, format, args...) }
func (v *view) s(text string)                { v.b.WriteString(text) }

// RenderHTML renders the report as a single self-contained HTML dashboard:
// one page per tab, inline CSS and SVG, an embedded icon, and no scripts.
func RenderHTML(report Report, window string) string {
	if strings.TrimSpace(window) == "" {
		window = "30m"
	}
	ev := report.Evidence
	tempo := sourceStatus(ev, "tempo")
	if tempo == "not collected" && len(ev.Traces)+len(ev.Spans) > 0 {
		tempo = "measured" // data without provenance (hand-built report): show it
	}
	loki := sourceStatus(ev, "loki")
	if loki == "not collected" && len(ev.Logs)+len(ev.LogAnomalies) > 0 {
		loki = "measured"
	}
	prom := promStatus(report)

	sections := []htmlSection{{"overview", "Overview", ""}, {"measurements", "Measurements", prom}, {"traces", "Traces", tempo},
		{"findings", "Findings", tempo}, {"logs", "Logs", loki}}
	if report.SoakResult != nil {
		sections = append(sections, htmlSection{"soak", "Soak", "measured"})
	}
	if report.Comparison != nil {
		sections = append(sections, htmlSection{"comparison", "Before vs after", ""})
	}
	sections = append(sections, htmlSection{"methods", "Methods", ""}, htmlSection{"architecture", "Architecture", ""})
	number := map[string]string{}
	for i, s := range sections {
		number[s.id] = fmt.Sprintf("%02d", i+1)
	}

	var b strings.Builder
	v := &view{report: report, ev: ev, window: window, prom: prom, tempo: tempo, loki: loki, number: number, b: &b}
	esc := html.EscapeString
	icon := iconDataURI()

	// The nav highlight follows the URL hash; Overview is current with no hash.
	var on []string
	for _, s := range sections {
		on = append(on, fmt.Sprintf(".dash:has(#%[1]s:target) .rail a[href=\"#%[1]s\"]", s.id))
	}
	on = append(on, ".dash:not(:has(.page:target)) .rail a[href=\"#overview\"]")
	suffix := func(sfx string) string {
		parts := make([]string, len(on))
		for i, sel := range on {
			parts[i] = sel + sfx
		}
		return strings.Join(parts, ",\n")
	}
	css := strings.NewReplacer("%NAV_RULES%", suffix(""), "%NAV_RULES_I%", suffix(" .i"), "%NAV_RULES_ST%", suffix(" .st:not(.st-none)")).Replace(reportCSS)

	v.s("<!doctype html>\n<html lang=\"en\">\n<head>\n<meta charset=\"utf-8\">\n")
	v.s("<meta name=\"viewport\" content=\"width=1920\">\n")
	v.s("<meta name=\"color-scheme\" content=\"light\">\n")
	v.s("<title>Extent Bottleneck Report</title>\n")
	v.w("<link rel=\"icon\" href=\"%s\">\n", icon)
	v.s("<style>\n" + css + "</style>\n</head>\n<body>\n")
	v.s(svgDefs)
	v.s("<a class=\"skip\" href=\"#overview\">Skip to overview</a>\n<div class=\"dash\">\n")

	// Top bar: identity, scope, health.
	health := healthStatus(report)
	v.s("<header class=\"bar\">\n")
	v.w("<div class=\"brand\"><img src=\"%s\" alt=\"\" width=\"40\" height=\"40\"><div><b>EXTENT</b><span>Bottleneck report</span></div></div>\n", icon)
	v.s("<dl class=\"bar-facts\">\n")
	if report.ServiceName != "" {
		v.w("<div><dt>Target service</dt><dd>%s</dd></div>\n", esc(report.ServiceName))
	}
	v.w("<div><dt>Window</dt><dd>last %s</dd></div>\n", esc(window))
	if at := latestRetrieval(ev); !at.IsZero() {
		v.w("<div><dt>Evidence retrieved</dt><dd>%s</dd></div>\n", esc(at.UTC().Format("2006-01-02 15:04Z")))
	}
	if report.OK {
		v.s("<div><dt>Exit code</dt><dd>0</dd></div>\n")
	} else {
		v.s("<div><dt>Exit code</dt><dd>5 (warnings)</dd></div>\n")
	}
	v.s("</dl>\n")
	v.w("<div class=\"health health-%s\"><span class=\"sw\" aria-hidden=\"true\"></span><div><span class=\"label\" style=\"color:inherit;opacity:.75\">Health status:</span> <b><span class=\"status-pill status-%s\">%s</span></b></div></div>\n",
		esc(health), esc(health), esc(health))
	v.s("</header>\n")

	// Nav rail.
	v.s("<aside class=\"rail\">\n<nav aria-label=\"Report tabs\"><ol>\n")
	for _, s := range sections {
		mark := "<span class=\"st st-none\" aria-hidden=\"true\"></span>"
		if s.source != "" {
			mark = fmt.Sprintf("<span class=\"st st-%s\" title=\"%s\" aria-hidden=\"true\"></span>", statusClass(s.source), esc(s.source))
		}
		v.w("<li><a href=\"#%s\"><span class=\"i\">%s</span>%s%s</a></li>\n", s.id, number[s.id], esc(s.title), mark)
	}
	v.s("</ol></nav>\n<div class=\"key\"><p class=\"label\">Key</p>\n")
	v.s(statusLegend())
	v.s("</div>\n<p class=\"foot\">extent.report/v1 · exit 0 = ok, 5 = warnings · self-contained, no scripts</p>\n</aside>\n")

	v.s("<main>\n<h1 class=\"skip\">Extent Bottleneck Report</h1>\n")
	v.overview()
	v.measurements()
	v.traces()
	v.findings()
	v.logs()
	if report.SoakResult != nil {
		v.soak()
	}
	if report.Comparison != nil {
		v.comparison()
	}
	v.methods()
	v.architecture()
	v.s("</main>\n</div>\n</body>\n</html>\n")
	return b.String()
}

// promStatus is Prometheus's contribution: the HTTP p95 measurement status,
// or unavailable when any Prometheus evidence query failed.
func promStatus(report Report) string {
	prom := "not collected"
	if m, ok := report.Measurements["httpP95"]; ok && m.Status != "" {
		prom = m.Status
	}
	if sourceStatus(report.Evidence, "prometheus") == "unavailable" {
		prom = "unavailable"
	}
	return prom
}

func statusClass(status string) string {
	class := strings.ReplaceAll(strings.ToLower(status), " ", "-")
	switch class {
	case "measured", "missing", "unavailable", "not-collected":
		return class
	}
	return "other"
}

func (v *view) pageOpen(id, title, purpose, meta string) {
	v.w("<section id=\"%[1]s\" class=\"page\" aria-label=\"%[3]s\">\n<header class=\"page-head\"><span class=\"n\">%[2]s</span><div><h2>%[3]s</h2><p>%[4]s</p></div><span class=\"meta label\">%[5]s</span></header>\n",
		id, v.number[id], html.EscapeString(title), html.EscapeString(purpose), html.EscapeString(meta))
}

func (v *view) panelOpen(class, title, fig string) {
	v.w("<article class=\"panel %s\">\n<div class=\"panel-head\"><h2>%s</h2><p class=\"fig\">%s</p></div>\n", class, html.EscapeString(title), html.EscapeString(fig))
}

func (v *view) more(id, label string) {
	v.w("<a class=\"more\" href=\"#%s\">Open %s →</a>\n", id, html.EscapeString(label))
}

// tableOpen writes a table head; a "num:" prefix right-aligns a column.
func (v *view) tableOpen(headers ...string) {
	v.s("<div class=\"table-wrap\">\n<table class=\"stack\">\n<thead>\n<tr>")
	for _, h := range headers {
		if strings.HasPrefix(h, "num:") {
			v.w("<th class=\"num\">%s</th>", strings.TrimPrefix(h, "num:"))
		} else {
			v.w("<th>%s</th>", h)
		}
	}
	v.s("</tr>\n</thead>\n<tbody>\n")
}

func (v *view) tableClose() { v.s("</tbody>\n</table>\n</div>\n") }

func (v *view) warnings() {
	report := v.report
	if len(report.Warnings) == 0 {
		return
	}
	v.w("<div class=\"alert\" role=\"note\"><div class=\"alert-key\"><p>%d warning%s · exit 5</p></div>\n<div class=\"alert-body\">\n<ul aria-label=\"Verification warnings\">\n", len(report.Warnings), plural(len(report.Warnings)))
	for _, warning := range report.Warnings {
		v.w("<li title=\"%s\">%s</li>\n", html.EscapeString(warning), html.EscapeString(warningHeadline(warning)))
	}
	v.s("</ul>\n<details><summary>Full warning text</summary>\n<pre>")
	for _, warning := range report.Warnings {
		v.s(html.EscapeString(warning) + "\n")
	}
	v.s("</pre>\n</details>\n</div></div>\n")
}

func (v *view) slowestPath() {
	if v.report.SlowestPath != "" {
		v.w("<p class=\"path\"><code>%s</code></p>\n", html.EscapeString(v.report.SlowestPath))
	} else {
		v.s("<p class=\"path none\">No route reported</p>\n")
	}
}

func (v *view) fixesList(class string) {
	v.w("<ol class=\"fixes%s\">\n", class)
	if len(v.report.Recommendations) > 0 {
		for i, item := range v.report.Recommendations {
			v.w("<li><span class=\"n\">%02d</span><div><strong>%s</strong><p>%s</p><p class=\"do\">→ %s</p></div></li>\n", i+1, html.EscapeString(item.Title), html.EscapeString(item.Evidence), html.EscapeString(item.Suggestion))
		}
	} else {
		for i, suggestion := range suggestions(v.report) {
			v.w("<li><span class=\"n\">%02d</span><div><p class=\"do\">%s</p></div></li>\n", i+1, html.EscapeString(suggestion))
		}
	}
	v.s("</ol>\n")
}

func (v *view) cpu() {
	v.s("<div class=\"pair\">\n")
	v.s(waffle("Container", v.report.Measurements["containerCPU"]))
	v.s(waffle("Host", v.report.Measurements["hostCPU"]))
	v.s("</div>\n")
}

func (v *view) saturation(full bool) {
	ev := v.ev
	if sat := ev.Saturation; sat != nil {
		v.s(poolSlots(sat.DBPoolActive, sat.DBPoolIdle, sat.DBPoolWaiting))
		v.s("<dl class=\"stats\">\n")
		if full {
			v.w("<div><dt>Pool active</dt><dd>%.0f</dd></div>\n", sat.DBPoolActive)
			v.w("<div><dt>Pool idle</dt><dd>%.0f</dd></div>\n", sat.DBPoolIdle)
			v.w("<div><dt>Pool waiting</dt><dd>%.0f</dd></div>\n", sat.DBPoolWaiting)
		}
		v.w("<div><dt>HTTP 429</dt><dd>%.0f</dd></div>\n", sat.RateLimit429Count)
		v.w("<div><dt>Queue lag</dt><dd>%.0f</dd></div>\n", sat.QueueLag)
		v.w("<div><dt>Mem growth</dt><dd>%s</dd></div>\n", html.EscapeString(bytesPerSecond(sat.MemoryGrowthBytesSec)))
		v.s("</dl>\n")
		return
	}
	status := sourceStatus(ev, "prometheus")
	if status == "measured" {
		status = "missing"
	}
	v.w("<p class=\"state-note\">%s %s</p>\n", chip(status), map[string]string{
		"missing":       "Prometheus returned no saturation series for this service.",
		"unavailable":   "The saturation queries failed.",
		"not collected": "Saturation is collected with evidence. Re-run with --tempo or --loki.",
	}[status])
}

func (v *view) overview() {
	ev, tempo, loki := v.ev, v.tempo, v.loki
	v.s("<section id=\"overview\" class=\"page\" aria-label=\"Overview\">\n")
	v.warnings()
	v.s("<div class=\"board\">\n")
	v.panelOpen("s7", "Slowest path", "Fig. 1 — p95 on a log scale")
	v.slowestPath()
	v.s("<div class=\"cap-sm\">" + latencyRuler(v.report.Measurements) + "</div>\n")
	v.w("<p class=\"lede\">%s</p>\n", html.EscapeString(v.report.Summary))
	v.more("measurements", "Measurements")
	v.s("</article>\n")

	v.panelOpen("s5 signal", "Signal path", "Fig. 2 — where evidence came from")
	v.s("<div class=\"cap-sm\">" + signalPath(v.report, tempo, loki) + "</div>\n")
	v.more("architecture", "Architecture")
	v.s("</article>\n")

	v.panelOpen("s3", "CPU pressure", "Fig. 4 — 1 cell = 1%")
	v.cpu()
	v.s("</article>\n")

	v.panelOpen("s4", "Saturation", "Fig. 5 — DB pool slots")
	v.saturation(false)
	v.s("</article>\n")

	v.panelOpen("s5", "Evidence", "Fig. 6 — 1 mark = 1 item")
	v.s("<div class=\"cap-xs\">" + tallies([]tally{
		{"Traces", len(ev.Traces), tempo},
		{"Spans", len(ev.Spans), tempo},
		{"Metric samples", len(ev.Metrics), sourceStatus(ev, "prometheus")},
		{"Log events", len(ev.Logs), loki},
		{"Anomalies", len(ev.LogAnomalies), loki},
		{"DB findings", len(ev.DBFindings), tempo},
		{"External HTTP", len(ev.ExternalHTTP), tempo},
		{"Queue findings", len(ev.QueueFindings), tempo},
	}) + "</div>\n")
	v.more("methods", "Methods")
	v.s("</article>\n")

	v.panelOpen("s12", "Suggested fixes", "Derived from this evidence")
	v.fixesList(" cols")
	v.more("findings", "Findings")
	v.s("</article>\n</div>\n</section>\n")
}

func (v *view) measurements() {
	report, ev := v.report, v.ev
	measured := 0
	for _, m := range report.Measurements {
		if m.Status == "measured" {
			measured++
		}
	}
	v.pageOpen("measurements", "Measurements", "Prometheus latency, CPU and saturation signals for the target service.",
		fmt.Sprintf("Prometheus · last %s · %d/%d measured", v.window, measured, len(report.Measurements)))
	v.s("<div class=\"board\">\n")
	v.panelOpen("s8", "Latency", "Fig. 1 — p95 on a log scale")
	v.slowestPath()
	v.s("<div class=\"cap-lg\">" + latencyRuler(report.Measurements) + "</div>\n")
	v.s("<h3>Measurements</h3>\n")
	v.tableOpen("Metric", "Status", "Value / Unit", "Scope", "Source")
	for _, m := range orderedMeasurements(report.Measurements) {
		v.w("<tr><td data-label=\"Metric\">%s</td><td data-label=\"Status\">%s</td><td data-label=\"Value\" class=\"mono\">%s</td><td data-label=\"Scope\" class=\"mono\">%s</td><td data-label=\"Source\" class=\"mono\">%s</td></tr>\n",
			html.EscapeString(m.label), chip(m.measurement.Status), html.EscapeString(formatMeasurementValue(m.measurement)), html.EscapeString(m.measurement.Scope), html.EscapeString(m.measurement.Source))
	}
	if len(report.Measurements) == 0 {
		v.s("<tr><td colspan=\"5\" class=\"muted\">No measurements were taken.</td></tr>\n")
	}
	v.tableClose()
	if len(ev.Metrics) > 0 {
		v.s("<h3>Metric Evidence</h3>\n")
		v.tableOpen("Metric Name", "num:Value / Unit", "Labels")
		for _, metric := range ev.Metrics {
			unit := metric.Unit
			if unit == "" {
				unit = "value"
			}
			v.w("<tr><td data-label=\"Metric\"><code>%s</code></td><td data-label=\"Value\" class=\"num\">%.3f %s</td><td data-label=\"Labels\" class=\"mono\">%s</td></tr>\n",
				html.EscapeString(metric.Name), metric.Value, html.EscapeString(unit), html.EscapeString(formatMetricLabels(metric.Metric)))
		}
		v.tableClose()
	}
	v.s("</article>\n")
	v.panelOpen("s4", "CPU pressure", "Fig. 4 — 1 cell = 1%")
	v.cpu()
	v.s("<h3>Saturation · Fig. 5</h3>\n")
	v.saturation(true)
	v.s("</article>\n</div>\n</section>\n")
}

func (v *view) traces() {
	ev, tempo := v.ev, v.tempo
	meta := "Tempo · " + tempo
	if tempo == "measured" || tempo == "missing" {
		meta = fmt.Sprintf("Tempo · %d traces · %d spans", len(ev.Traces), len(ev.Spans))
	}
	v.pageOpen("traces", "Traces", "Trace examples for the target service and the anatomy of the slowest one.", meta)
	if tempo != "measured" && tempo != "missing" {
		v.w("<p class=\"state-note big\">%s %s</p>\n</section>\n", chip(tempo), sourceHint("tempo", tempo))
		return
	}
	v.s("<div class=\"board\">\n")
	v.panelOpen("s7", "Span anatomy", "Fig. 3 — share of the slowest trace")
	if len(ev.Spans) > 0 {
		v.s(spanAnatomy(ev))
	} else {
		v.w("<p class=\"state-note\">%s No span details were returned for the target traces.</p>\n", chip(tempo))
	}
	v.s("</article>\n")
	v.panelOpen("s5", "Trace durations", "Fig. 7 — root duration per trace")
	if len(ev.Traces) > 0 {
		v.s(traceDurations(ev.Traces))
		v.tableOpen("Trace ID", "Service", "Root Operation", "num:Duration")
		for _, trace := range ev.Traces {
			v.w("<tr><td data-label=\"Trace ID\"><code>%s</code></td><td data-label=\"Service\">%s</td><td data-label=\"Root operation\"><code>%s</code></td><td data-label=\"Duration\" class=\"num\">%s</td></tr>\n",
				html.EscapeString(trace.TraceID), html.EscapeString(trace.RootServiceName), html.EscapeString(trace.RootTraceName), formatMillis(trace.DurationMS))
		}
		v.tableClose()
	} else {
		v.s("<p class=\"muted\">No target-service traces were returned.</p>\n")
	}
	v.s("</article>\n</div>\n")
	if len(ev.Spans) > 0 {
		v.w("<h3>Span details</h3>\n<details open>\n<summary>Trace Span Details (%d)</summary>\n", len(ev.Spans))
		v.tableOpen("Span Name", "Kind", "num:Duration", "Trace ID", "Span ID", "Context")
		for _, span := range ev.Spans {
			v.w("<tr><td data-label=\"Span\"><code>%s</code></td><td data-label=\"Kind\">%s</td><td data-label=\"Duration\" class=\"num\">%s</td><td data-label=\"Trace ID\"><code>%s</code></td><td data-label=\"Span ID\"><code>%s</code></td><td data-label=\"Context\" class=\"mono\">%s</td></tr>\n",
				html.EscapeString(span.Name), html.EscapeString(span.Kind), formatMillis(span.DurationMS), html.EscapeString(span.TraceID), html.EscapeString(span.SpanID), html.EscapeString(orDash(spanContext(span.Attributes))))
		}
		v.tableClose()
		v.s("</details>\n")
	}
	v.s("</section>\n")
}

func (v *view) findings() {
	ev, tempo := v.ev, v.tempo
	v.pageOpen("findings", "Findings", "Database, external HTTP and queue spans worth acting on, and the fixes they suggest.",
		fmt.Sprintf("Tempo · %d DB · %d HTTP · %d queue", len(ev.DBFindings), len(ev.ExternalHTTP), len(ev.QueueFindings)))
	v.s("<div class=\"board\">\n")
	v.panelOpen("s7", "Span findings", "Fig. 8 — 1 mark = 1 repeat")
	if tempo != "measured" && tempo != "missing" {
		v.w("<p class=\"state-note\">%s Findings are derived from Tempo spans, which are not available for this report.</p>\n", chip(tempo))
	} else {
		v.s(dbRepeats(ev.DBFindings))
		v.s("<h3>Database Findings</h3>\n")
		if len(ev.DBFindings) > 0 {
			v.tableOpen("System", "Normalized Statement", "num:Repeat Count", "num:Total Duration", "Trace ID")
			for _, finding := range ev.DBFindings {
				v.w("<tr><td data-label=\"System\">%s</td><td data-label=\"Statement\"><code>%s</code></td><td data-label=\"Repeat count\" class=\"num\">%d</td><td data-label=\"Total duration\" class=\"num\">%s</td><td data-label=\"Trace ID\"><code>%s</code></td></tr>\n",
					html.EscapeString(finding.System), html.EscapeString(finding.NormalizedStatement), finding.RepeatCount, formatMillis(finding.TotalDurationMS), html.EscapeString(finding.TraceID))
			}
			v.tableClose()
		} else {
			v.s("<p class=\"muted\">No database spans or slow/repeated database operations were observed.</p>\n")
		}
		v.s("<h3>External HTTP Findings</h3>\n")
		if len(ev.ExternalHTTP) > 0 {
			v.tableOpen("Destination", "num:Duration", "Trace ID")
			for _, finding := range ev.ExternalHTTP {
				v.w("<tr><td data-label=\"Destination\"><code>%s</code></td><td data-label=\"Duration\" class=\"num\">%s</td><td data-label=\"Trace ID\"><code>%s</code></td></tr>\n",
					html.EscapeString(finding.Destination), formatMillis(finding.DurationMS), html.EscapeString(finding.TraceID))
			}
			v.tableClose()
		} else {
			v.s("<p class=\"muted\">No external HTTP spans were observed.</p>\n")
		}
		v.s("<h3>Queue Findings</h3>\n")
		if len(ev.QueueFindings) > 0 {
			v.tableOpen("System", "Name", "num:Duration", "Trace ID")
			for _, finding := range ev.QueueFindings {
				v.w("<tr><td data-label=\"System\">%s</td><td data-label=\"Name\"><code>%s</code></td><td data-label=\"Duration\" class=\"num\">%s</td><td data-label=\"Trace ID\"><code>%s</code></td></tr>\n",
					html.EscapeString(finding.System), html.EscapeString(finding.Name), formatMillis(finding.DurationMS), html.EscapeString(finding.TraceID))
			}
			v.tableClose()
		} else {
			v.s("<p class=\"muted\">No queue or messaging spans were observed.</p>\n")
		}
	}
	v.s("</article>\n")
	v.panelOpen("s5", "Suggested fixes", "Derived from this evidence")
	v.fixesList("")
	v.s("</article>\n</div>\n</section>\n")
}

func (v *view) logs() {
	ev, loki := v.ev, v.loki
	meta := "Loki · " + loki
	if loki == "measured" || loki == "missing" {
		meta = fmt.Sprintf("Loki · %d events · %d anomalies", len(ev.Logs), len(ev.LogAnomalies))
	}
	v.pageOpen("logs", "Logs", "Error and warning anomalies and log events correlated with the target service.", meta)
	if loki != "measured" && loki != "missing" {
		v.w("<p class=\"state-note big\">%s %s</p>\n<div class=\"cmd\"><pre>%s</pre></div>\n</section>\n", chip(loki), sourceHint("loki", loki), html.EscapeString(rerunCommand(v.report, v.window)))
		return
	}
	v.s("<div class=\"board\">\n")
	v.panelOpen("s4", "Log levels", "Fig. 9 — 1 mark = 1 event")
	v.s(logLevels(ev.Logs, len(ev.LogAnomalies)))
	v.s("</article>\n")
	v.panelOpen("s8", "Error/Warning Anomalies", "Grouped by problem")
	if len(ev.LogAnomalies) > 0 {
		v.tableOpen("Service", "Problem", "Level", "Trace ID", "Message")
		for _, anomaly := range ev.LogAnomalies {
			v.w("<tr><td data-label=\"Service\">%s</td><td data-label=\"Problem\">%s</td><td data-label=\"Level\">%s</td><td data-label=\"Trace ID\"><code>%s</code></td><td data-label=\"Message\">%s</td></tr>\n",
				html.EscapeString(anomaly.ServiceName), html.EscapeString(anomaly.Problem), html.EscapeString(anomaly.Level), html.EscapeString(anomaly.TraceID), html.EscapeString(anomaly.Message))
		}
		v.tableClose()
	} else {
		v.s("<p class=\"muted\">None observed.</p>\n")
	}
	v.s("</article>\n</div>\n")
	if len(ev.Logs) > 0 {
		v.w("<h3>Log events</h3>\n<details open>\n<summary>Log Events (%d)</summary>\n", len(ev.Logs))
		v.tableOpen("Level", "Service", "Trace ID", "Span ID", "Request ID", "Message")
		for _, event := range ev.Logs {
			level := event.Level
			if level == "" {
				level = "info"
			}
			reqID := event.RequestID
			if reqID == "" {
				reqID = "-"
			}
			v.w("<tr><td data-label=\"Level\">%s</td><td data-label=\"Service\">%s</td><td data-label=\"Trace ID\"><code>%s</code></td><td data-label=\"Span ID\"><code>%s</code></td><td data-label=\"Request ID\"><code>%s</code></td><td data-label=\"Message\">%s</td></tr>\n",
				html.EscapeString(level), html.EscapeString(event.ServiceName), html.EscapeString(event.TraceID), html.EscapeString(event.SpanID), html.EscapeString(reqID), html.EscapeString(event.Message))
		}
		v.tableClose()
		v.s("</details>\n")
	} else {
		v.s("<p class=\"muted\">No target-service log events were returned.</p>\n")
	}
	v.s("</section>\n")
}

func (v *view) soak() {
	soak := v.report.SoakResult
	concurrency := soak.Concurrency
	if concurrency <= 0 {
		concurrency = 1
	}
	v.pageOpen("soak", "Sustained Soak Performance", "Synthetic traffic Extent sent to the target URL while the report ran.",
		fmt.Sprintf("Soak · %d requests", soak.TotalRequests))
	v.s("<div class=\"board\">\n")
	v.panelOpen("s12", "Soak traffic", "Fig. 10 — responses by status class")
	v.s("<dl class=\"stats four\">\n")
	v.w("<div><dt>Duration</dt><dd>%s</dd></div>\n", html.EscapeString(soak.DurationElapsed.Round(time.Millisecond).String()))
	v.w("<div><dt>Concurrency</dt><dd>%d</dd></div>\n", concurrency)
	v.w("<div><dt>Requests</dt><dd>%d</dd></div>\n", soak.TotalRequests)
	v.w("<div><dt>Throughput</dt><dd>%.1f rps</dd></div>\n", soak.RPS)
	v.s("</dl>\n")
	if soak.Latency != nil {
		v.s("<dl class=\"stats four\" style=\"margin-top:var(--space-sm)\">\n")
		v.w("<div><dt>Client p50</dt><dd>%.1f ms</dd></div>\n", soak.Latency.P50MS)
		v.w("<div><dt>Client p95</dt><dd>%.1f ms</dd></div>\n", soak.Latency.P95MS)
		v.w("<div><dt>Client p99</dt><dd>%.1f ms</dd></div>\n", soak.Latency.P99MS)
		v.w("<div><dt>Client max</dt><dd>%.1f ms</dd></div>\n", soak.Latency.MaxMS)
		v.s("</dl>\n")
	}
	if soak.FailedRequests > 0 {
		v.w("<p class=\"state-note\">%d transport request failure(s) occurred during soak.</p>\n", soak.FailedRequests)
	}
	v.s(statusStrip(soak.StatusDistribution))
	v.s("</article>\n")
	if len(soak.StatusDistribution) > 0 {
		codes := make([]int, 0, len(soak.StatusDistribution))
		total := 0
		for code, count := range soak.StatusDistribution {
			codes = append(codes, code)
			total += count
		}
		sort.Ints(codes)
		v.panelOpen("s6", "Status codes", "Counts")
		v.tableOpen("Status Code", "num:Count", "num:Share")
		for _, code := range codes {
			v.w("<tr><td data-label=\"Status code\">%d</td><td data-label=\"Count\" class=\"num\">%d</td><td data-label=\"Share\" class=\"num\">%.1f%%</td></tr>\n",
				code, soak.StatusDistribution[code], float64(soak.StatusDistribution[code])/float64(total)*100)
		}
		v.tableClose()
		v.s("</article>\n")
	}
	v.panelOpen("s6", "Against the baseline", "Load summary")
	if v.report.Comparison != nil && v.report.Comparison.LoadSummary != "" {
		v.w("<p><strong>Baseline Comparison:</strong> %s</p>\n", html.EscapeString(v.report.Comparison.LoadSummary))
	} else {
		v.s("<p class=\"muted\">No baseline to compare against. Run with --compare baseline.</p>\n")
	}
	v.s("</article>\n</div>\n</section>\n")
}

func (v *view) comparison() {
	c := v.report.Comparison
	v.pageOpen("comparison", "Before vs after", "This run against the saved baseline snapshot.", "Baseline snapshot")
	keys := make([]string, 0, len(c.Deltas))
	for key := range c.Deltas {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	v.s("<div class=\"board\">\n")
	v.panelOpen("s7", "Change per measurement", "Fig. 11 — change vs baseline, % of before")
	v.w("<p class=\"lede\">%s</p>\n", html.EscapeString(c.Summary))
	v.s(dumbbell(keys, c.Deltas))
	v.s("</article>\n")
	v.panelOpen("s5", "Deltas", "Same unit as the measurement")
	if len(keys) > 0 {
		v.tableOpen("Measurement", "num:Before", "num:After", "num:Change")
		for _, key := range keys {
			delta := c.Deltas[key]
			change := "not comparable"
			if delta.Comparable {
				change = formatChange(delta.Change, delta.After.Unit)
			}
			v.w("<tr><td data-label=\"Measurement\"><code>%s</code></td><td data-label=\"Before\" class=\"num\">%s</td><td data-label=\"After\" class=\"num\">%s</td><td data-label=\"Change\" class=\"num\">%s</td></tr>\n",
				html.EscapeString(key), html.EscapeString(baselineValue(delta.Before.Status, delta.Before.Value, delta.Before.Unit)), html.EscapeString(baselineValue(delta.After.Status, delta.After.Value, delta.After.Unit)), html.EscapeString(change))
		}
		v.tableClose()
	} else {
		v.s("<p class=\"muted\">The baseline has no comparable measurements.</p>\n")
	}
	if c.LoadSummary != "" {
		v.w("<p>%s</p>\n", html.EscapeString(c.LoadSummary))
	}
	v.s("</article>\n</div>\n</section>\n")
}

var reportFlags = [][3]string{
	{"--service", "", "target service.name for scoped evidence"},
	{"--last", "30m", "lookback window label for report output"},
	{"--local", "false", "run local HTTP benchmark report without Prometheus, Tempo, or Loki (requires --url)"},
	{"--prometheus", "http://localhost:9090", "Prometheus base URL"},
	{"--tempo", "", "Tempo base URL for trace evidence"},
	{"--loki", "", "Loki base URL for log evidence"},
	{"--format", "text", "report format: text, markdown, html, json"},
	{"--output", "", "write report output to a UTF-8 file instead of stdout"},
	{"--compare", "", "compare against a baseline file or the word baseline"},
	{"--include-data", "false", "include raw gathered app data appendix"},
	{"--json", "false", "print JSON output"},
	{"--soak", "0", "sustained soak duration (requires --url)"},
	{"--url", "", "target application URL for soak testing"},
	{"--method", "GET", "HTTP method for soak requests"},
	{"--timeout", "5s", "per-request HTTP timeout for soak requests"},
	{"--concurrency", "1", "concurrent synthetic request workers for soak testing"},
	{"--rate", "unset", "rate limit in requests per second for soak testing; 0 means unlimited. Unset defaults to 50 with --soak, unlimited otherwise"},
}

func (v *view) methods() {
	ev := v.ev
	v.pageOpen("methods", "Query Provenance", "How this report was made: every query, its status, and how to run it again.", "Methods")
	v.s("<div class=\"board\">\n")
	v.panelOpen("s8", "Queries", "Provenance")
	if len(ev.Provenance) > 0 {
		v.tableOpen("Source", "Scope", "Status", "Service", "Window", "Query", "Retrieved")
		for _, source := range ev.Provenance {
			retrieved := "-"
			if !source.RetrievedAt.IsZero() {
				retrieved = source.RetrievedAt.UTC().Format(time.RFC3339)
			}
			v.w("<tr><td data-label=\"Source\" class=\"mono\">%s</td><td data-label=\"Scope\" class=\"mono\">%s</td><td data-label=\"Status\">%s</td><td data-label=\"Service\" class=\"mono\">%s</td><td data-label=\"Window\" class=\"mono\">%s</td><td data-label=\"Query\"><code>%s</code></td><td data-label=\"Retrieved\" class=\"mono nowrap\">%s</td></tr>\n",
				html.EscapeString(source.Source), html.EscapeString(source.Scope), chip(source.Status), html.EscapeString(source.ServiceName), html.EscapeString(source.Window), html.EscapeString(source.Query), html.EscapeString(retrieved))
		}
		v.tableClose()
	} else {
		v.s("<p class=\"muted\">Only the Prometheus measurement queries ran. Tempo and Loki were not queried, so no evidence provenance was recorded.</p>\n")
	}
	for _, note := range ev.Notes {
		v.w("<p class=\"muted\">%s</p>\n", html.EscapeString(note))
	}
	v.s("<h3>Re-run with evidence</h3>\n")
	v.w("<div class=\"cmd\"><pre>%s</pre></div>\n", html.EscapeString(rerunCommand(v.report, v.window)))
	v.s("</article>\n")
	v.panelOpen("s4", "Status vocabulary", "Used on every tab")
	v.s("<ul class=\"fixes vocab\">\n")
	for _, row := range [][2]string{
		{"measured", "The backend answered with data for this service."},
		{"missing", "The backend answered, but returned nothing for this service."},
		{"unavailable", "The query failed: the backend could not be reached or errored."},
		{"not collected", "The source was never queried; its flag was not passed."},
	} {
		v.w("<li><span>%s</span><p>%s</p></li>\n", chip(row[0]), row[1])
	}
	v.s("</ul>\n<h3>Exit codes</h3>\n<dl class=\"stats codes\">\n<div><dt>0</dt><dd>Success.</dd></div>\n<div><dt>5</dt><dd>Report warnings: a query failed or evidence is incomplete.</dd></div>\n</dl>\n</article>\n")
	v.panelOpen("s12", "extent report flags", "Usage")
	v.tableOpen("Flag", "Default", "Meaning")
	for _, f := range reportFlags {
		def := f[1]
		if def == "" {
			def = "—"
		}
		v.w("<tr><td data-label=\"Flag\"><code>%s</code></td><td data-label=\"Default\" class=\"mono\">%s</td><td data-label=\"Meaning\">%s</td></tr>\n", f[0], html.EscapeString(def), html.EscapeString(f[2]))
	}
	v.tableClose()
	if v.report.ApplicationData != nil {
		if data, err := json.MarshalIndent(v.report.ApplicationData, "", "  "); err == nil {
			v.s("<details>\n<summary>Gathered Application Data</summary>\n")
			v.w("<pre><code>%s</code></pre>\n</details>\n", html.EscapeString(string(data)))
		}
	}
	v.s("</article>\n</div>\n</section>\n")
}

// architecture is the static map of Extent itself (docs/architecture.md),
// with this report's command and package drawn solid.
func (v *view) architecture() {
	v.pageOpen("architecture", "Architecture", "How Extent is built and where this report comes from. Every mutation is planned first and recorded so it can be previewed and undone.", "docs/architecture.md")
	v.s("<div class=\"board\">\n")
	v.panelOpen("s12", "Workflow", "Fig. A1 — commands in order")
	v.s("<div class=\"flow\">\n<div><span>Target repository</span></div>\n")
	for _, phase := range architectureWorkflow {
		v.w("<div><p class=\"fig\">%s</p><h3>%s</h3><ol>\n", phase.n, phase.name)
		for _, c := range phase.cmds {
			here := ""
			if c[0] == "report / baseline" {
				here = " class=\"here\""
			}
			v.w("<li%s><code>extent %s</code><span>%s</span></li>\n", here, c[0], c[1])
		}
		v.s("</ol></div>\n")
	}
	v.s("</div>\n<p class=\"state-note\">Phases run left to right; the double rule marks the command that produced this report.</p>\n</article>\n")

	v.panelOpen("s12", "Components", "Fig. A2 — packages by layer")
	v.s("<div class=\"layers\">\n")
	for _, layer := range architectureLayers {
		v.w("<div><h3>%s</h3><ul>\n", layer.name)
		for _, p := range layer.pkgs {
			here := ""
			if p[0] == "internal/reporter" {
				here = " class=\"here\""
			}
			v.w("<li%s>%s<span>%s</span></li>\n", here, p[0], p[1])
		}
		v.s("</ul></div>\n")
	}
	v.s("</div>\n<p class=\"shared\">Shared: <code>internal/fileops</code> transactional writes, backups and undo · <code>internal/state</code> .extent/state.json ownership · <code>internal/process</code> bounded subprocesses · <code>internal/observability</code> bounded query client</p>\n</article>\n")

	v.panelOpen("s6", "Runtime", "Fig. A3 — the local LGTM stack")
	v.s("<div class=\"cap-md\">" + runtimeFigure(v.prom, v.tempo, v.loki) + "</div>\n")
	v.s("</article>\n")

	v.panelOpen("s6", "Design goals", "Extent vs doing it by hand")
	v.tableOpen("Goal", "Extent", "Manual")
	for _, g := range architectureGoals {
		v.w("<tr><td data-label=\"Goal\" class=\"nowrap\"><strong>%s</strong></td><td data-label=\"Extent\">%s</td><td data-label=\"Manual\" class=\"muted\">%s</td></tr>\n", g[0], g[1], g[2])
	}
	v.tableClose()
	v.s("</article>\n</div>\n</section>\n")
}

var architectureWorkflow = []struct {
	n, name string
	cmds    [][2]string
}{
	{"Phase 1", "Discovery &amp; planning", [][2]string{{"scan", "Runtime and framework detection"}, {"analyze", "AST, routes, DB, queues"}, {"plan", "Rollout strategy and profiles"}}},
	{"Phase 2", "Provisioning &amp; instrumentation", [][2]string{{"apply", "extent.yaml and LGTM configs"}, {"instrument", "OTel bootstrap injection"}, {"deps", "SDK dependency sync"}}},
	{"Phase 3", "Stack &amp; verification", [][2]string{{"stack up", "Docker Compose LGTM stack"}, {"verify / doctor", "Config and endpoint checks"}, {"smoke", "Synthetic traffic and correlation"}}},
	{"Phase 4", "Analysis &amp; quality", [][2]string{{"cardinality", "Loki label risk score"}, {"score", "Telemetry quality rating"}, {"report / baseline", "Prometheus bottlenecks and diff"}}},
}

var architectureLayers = []struct {
	name string
	pkgs [][2]string
}{
	{"CLI", [][2]string{{"cmd/extent", "entrypoint and command handlers"}, {"recipes/", "framework recipe metadata"}}},
	{"Analysis &amp; contract", [][2]string{{"internal/scanner", "runtimes, frameworks"}, {"internal/analyzer", "deep repo analysis"}, {"internal/planner", "proposed changes"}, {"internal/contract", "observability contract"}, {"internal/config", "extent.yaml"}}},
	{"Instrumentation &amp; GitOps", [][2]string{{"internal/instrumenter", "bootstrap injection"}, {"internal/deps", "dependency installs"}, {"internal/codemods", "experimental AST patches"}, {"internal/gitops", "branches and git"}}},
	{"Stack provisioning", [][2]string{{"internal/templates", "Compose, Collector, LGTM files"}, {"internal/stack", "up, down, status, readiness"}}},
	{"Verification &amp; evidence", [][2]string{{"internal/verifier", "verify, doctor"}, {"internal/smoke", "traffic correlation"}, {"internal/evidence", "Tempo, Loki, Prometheus clients"}, {"internal/reporter", "bottleneck report synthesis"}, {"internal/recommendations", "remediation"}, {"internal/baseline", "before/after"}, {"internal/cardinality", "label safety"}, {"internal/scorer", "quality score"}}},
}

var architectureGoals = [][3]string{
	{"Structural integrity", "Generates a bounded OpenTelemetry bootstrap and local LGTM configuration with pinned dependencies for supported targets.", "Manual version selection and configuration."},
	{"Cross-domain sync", "Transactional changes with ownership checks, exact backups, idempotent apply, and conflict-safe undo.", "Manual coordination of SDKs and configuration files."},
	{"Source changes", "Syntax-aware Go import and lifecycle edits. Node and Python bootstrap edits are deliberately constrained. Deep codemods are experimental.", "Manual source editing."},
	{"Operational safety", "Built-in cardinality checks and telemetry quality scoring (extent score).", "Risk of cardinality explosion or broken trace context."},
	{"Feedback loop", "Service-scoped smoke checks correlate synthetic traffic with Prometheus metrics, Tempo traces, and Loki logs.", "Custom traffic generation and manual validation."},
}
