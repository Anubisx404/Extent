package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Anubisx404/Extent/internal/analyzer"
	"github.com/Anubisx404/Extent/internal/baseline"
	"github.com/Anubisx404/Extent/internal/cardinality"
	"github.com/Anubisx404/Extent/internal/reporter"
	"github.com/Anubisx404/Extent/internal/scanner"
)

func gatherApplicationData(root, baselinePath string, report reporter.Report) map[string]any {
	data := map[string]any{
		"report": map[string]any{
			"serviceName":        report.ServiceName,
			"summary":            report.Summary,
			"slowestPath":        report.SlowestPath,
			"slowestPathLatency": report.SlowestPathLatency,
			"dbLatency":          report.DBLatency,
			"dbShare":            report.DBShare,
			"containerCpu":       report.ContainerCPU,
			"hostCpu":            report.HostCPU,
			"evidence":           report.Evidence,
			"recommendations":    report.Recommendations,
			"comparison":         report.Comparison,
			"warnings":           report.Warnings,
			"measurements":       report.Measurements,
			"soakResult":         report.SoakResult,
		},
	}
	if scan, err := scanner.Scan(root); err == nil {
		data["scan"] = scan
	} else {
		data["scanError"] = err.Error()
	}
	if analysis, err := analyzer.Analyze(root); err == nil {
		data["analysis"] = analysis
	} else {
		data["analysisError"] = err.Error()
	}
	data["cardinality"] = cardinality.Analyze(root)
	if baselinePath != "" {
		if snapshot, err := baseline.Load(baselinePath); err == nil {
			data["baseline"] = snapshot
		} else {
			data["baselineError"] = err.Error()
		}
	}
	return data
}

type reportOptions struct {
	Prometheus  string
	Loki        string
	Tempo       string
	Service     string
	Format      string
	Output      string
	Last        string
	Compare     string
	IncludeData bool
	JSON        bool
	Local       bool
	Soak        time.Duration
	URL         string
	Method      string
	Timeout     time.Duration
	Concurrency int
	Rate        float64
}

func newReportFlagSet(o *reportOptions) *flag.FlagSet {
	fs := flag.NewFlagSet("report", flag.ContinueOnError)
	fs.StringVar(&o.Prometheus, "prometheus", "http://localhost:9090", "Prometheus base URL")
	fs.StringVar(&o.Loki, "loki", "", "Loki base URL for log evidence")
	fs.StringVar(&o.Tempo, "tempo", "", "Tempo base URL for trace evidence")
	fs.StringVar(&o.Service, "service", "", "target service.name for scoped evidence")
	fs.StringVar(&o.Format, "format", "text", "report format: text, markdown, html, json")
	fs.StringVar(&o.Output, "output", "", "write report output to a UTF-8 file instead of stdout")
	fs.StringVar(&o.Last, "last", "30m", "lookback window label for report output")
	fs.StringVar(&o.Compare, "compare", "", "compare against a baseline file or the word baseline")
	fs.BoolVar(&o.IncludeData, "include-data", false, "include raw gathered app data appendix")
	fs.BoolVar(&o.JSON, "json", false, "print JSON output")
	fs.BoolVar(&o.Local, "local", false, "run local HTTP benchmark report without Prometheus, Tempo, or Loki (requires --url)")
	fs.DurationVar(&o.Soak, "soak", 0, "sustained soak duration (requires --url)")
	fs.StringVar(&o.URL, "url", "", "target application URL for soak testing")
	fs.StringVar(&o.Method, "method", http.MethodGet, "HTTP method for soak requests")
	fs.DurationVar(&o.Timeout, "timeout", 5*time.Second, "per-request HTTP timeout for soak requests")
	fs.IntVar(&o.Concurrency, "concurrency", 1, "concurrent synthetic request workers for soak testing")
	fs.Float64Var(&o.Rate, "rate", unsetRate, "rate limit in requests per second for soak testing; 0 means unlimited. Unset defaults to 50 with --soak, unlimited otherwise")
	return fs
}

func defaultLocalServiceName(root, rawURL string) string {
	if abs, err := filepath.Abs(root); err == nil {
		if base := strings.TrimSpace(filepath.Base(abs)); base != "" && base != "." && base != string(filepath.Separator) {
			return base
		}
	}
	if parsed, err := url.Parse(rawURL); err == nil && parsed.Host != "" {
		return parsed.Host
	}
	return "local-service"
}

func renderReportText(report reporter.Report) string {
	var b strings.Builder
	b.WriteString(report.Summary + "\n")
	if report.SoakResult != nil {
		b.WriteString(fmt.Sprintf("Soak Performance: %.1f RPS (%d requests in %s)\n", report.SoakResult.RPS, report.SoakResult.TotalRequests, report.SoakResult.DurationElapsed.Round(time.Millisecond)))
		if report.SoakResult.Latency != nil {
			b.WriteString(fmt.Sprintf("Latency: p50=%.1fms p95=%.1fms p99=%.1fms (min=%.1fms mean=%.1fms max=%.1fms)\n",
				report.SoakResult.Latency.P50MS, report.SoakResult.Latency.P95MS, report.SoakResult.Latency.P99MS,
				report.SoakResult.Latency.MinMS, report.SoakResult.Latency.MeanMS, report.SoakResult.Latency.MaxMS))
		}
	}
	if len(report.Warnings) > 0 {
		b.WriteString("\nWarnings:\n")
		for _, warning := range report.Warnings {
			b.WriteString("- " + warning + "\n")
		}
	}
	return b.String()
}

func runReport(args []string) error {
	var o reportOptions
	fs := newReportFlagSet(&o)
	if help, err := parseFlags(fs, args); help || err != nil {
		return err
	}
	if err := validatePositional("report", fs.Args(), 1); err != nil {
		return err
	}
	if err := validateEnum("format", o.Format, []string{"text", "markdown", "html", "json"}); err != nil {
		return err
	}
	method, err := validateHTTPMethod(o.Method)
	if err != nil {
		return err
	}
	if o.Timeout <= 0 {
		return usageError("--timeout must be greater than zero")
	}
	if o.Soak < 0 {
		return usageError("--soak cannot be negative")
	}
	if o.Concurrency < 1 {
		return usageError("--concurrency must be at least 1")
	}
	root := firstArgOrDot(fs.Args())
	if o.Local {
		if strings.TrimSpace(o.URL) == "" {
			return usageError("report --local requires --url")
		}
		if o.Soak == 0 {
			o.Soak = 5 * time.Second
		}
		if strings.TrimSpace(o.Service) == "" {
			o.Service = defaultLocalServiceName(root, o.URL)
		}
	} else {
		if strings.TrimSpace(o.Service) == "" {
			return usageError("report requires --service for target-scoped evidence")
		}
		if o.Soak > 0 && strings.TrimSpace(o.URL) == "" {
			return usageError("report --soak requires --url")
		}
	}
	rate, err := resolveRate(fs, o.Rate, o.Soak)
	if err != nil {
		return err
	}
	baselinePath := ""
	if o.Compare == "baseline" {
		baselinePath = filepath.Join(root, ".extent", "baseline.json")
	} else if strings.TrimSpace(o.Compare) != "" {
		baselinePath = o.Compare
	}
	report := reporter.Build(reporter.Config{
		PrometheusURL:   o.Prometheus,
		LokiURL:         o.Loki,
		TempoURL:        o.Tempo,
		IncludeEvidence: !o.Local && (o.Loki != "" || o.Tempo != ""),
		BaselinePath:    baselinePath,
		ServiceName:     o.Service,
		Window:          o.Last,
		SoakDuration:    o.Soak,
		TargetURL:       o.URL,
		Concurrency:     o.Concurrency,
		RateLimit:       rate,
		Method:          method,
		RequestTimeout:  o.Timeout,
		SkipTelemetry:   o.Local,
	})
	if o.IncludeData {
		report.ApplicationData = gatherApplicationData(root, baselinePath, report)
	}

	var rendered string
	switch {
	case o.JSON || o.Format == "json":
		if strings.TrimSpace(o.Output) == "" {
			if err := writeJSONSchema(schemaReport, report); err != nil {
				return err
			}
			if !report.OK {
				return verificationError("report completed with warnings")
			}
			return nil
		}
		renderedBytes, err := json.MarshalIndent(struct {
			Schema string `json:"schema"`
			reporter.Report
		}{Schema: schemaReport, Report: report}, "", "  ")
		if err != nil {
			return err
		}
		rendered = string(renderedBytes) + "\n"
	case o.Format == "markdown":
		rendered = reporter.RenderMarkdown(report, o.Last)
	case o.Format == "html":
		rendered = reporter.RenderHTML(report, o.Last)
	default:
		rendered = renderReportText(report)
	}

	if strings.TrimSpace(o.Output) != "" {
		if err := os.WriteFile(o.Output, []byte(rendered), 0o644); err != nil {
			return err
		}
	} else {
		fmt.Print(rendered)
	}
	if !report.OK {
		return verificationError("report completed with warnings")
	}
	return nil
}
