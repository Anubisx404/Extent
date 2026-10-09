package main

import (
	"flag"
	"fmt"
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
	Last        string
	Compare     string
	IncludeData bool
	JSON        bool
	Soak        time.Duration
	URL         string
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
	fs.StringVar(&o.Last, "last", "30m", "lookback window label for report output")
	fs.StringVar(&o.Compare, "compare", "", "compare against a baseline file or the word baseline")
	fs.BoolVar(&o.IncludeData, "include-data", false, "include raw gathered app data appendix")
	fs.BoolVar(&o.JSON, "json", false, "print JSON output")
	fs.DurationVar(&o.Soak, "soak", 0, "sustained soak duration (requires --url)")
	fs.StringVar(&o.URL, "url", "", "target application URL for soak testing")
	fs.IntVar(&o.Concurrency, "concurrency", 1, "concurrent synthetic request workers for soak testing")
	fs.Float64Var(&o.Rate, "rate", unsetRate, "rate limit in requests per second for soak testing; 0 means unlimited. Unset defaults to 50 with --soak, unlimited otherwise")
	return fs
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
	if strings.TrimSpace(o.Service) == "" {
		return usageError("report requires --service for target-scoped evidence")
	}
	if o.Soak > 0 && strings.TrimSpace(o.URL) == "" {
		return usageError("report --soak requires --url")
	}
	if o.Soak < 0 {
		return usageError("--soak cannot be negative")
	}
	if o.Concurrency < 1 {
		return usageError("--concurrency must be at least 1")
	}
	rate, err := resolveRate(fs, o.Rate, o.Soak)
	if err != nil {
		return err
	}
	root := firstArgOrDot(fs.Args())
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
		IncludeEvidence: o.Loki != "" || o.Tempo != "",
		BaselinePath:    baselinePath,
		ServiceName:     o.Service,
		Window:          o.Last,
		SoakDuration:    o.Soak,
		TargetURL:       o.URL,
		Concurrency:     o.Concurrency,
		RateLimit:       rate,
	})
	if o.IncludeData {
		report.ApplicationData = gatherApplicationData(root, baselinePath, report)
	}
	if o.JSON || o.Format == "json" {
		if err := writeJSONSchema(schemaReport, report); err != nil {
			return err
		}
		if !report.OK {
			return verificationError("report completed with warnings")
		}
		return nil
	}
	if o.Format == "markdown" {
		fmt.Print(reporter.RenderMarkdown(report, o.Last))
		if !report.OK {
			return verificationError("report completed with warnings")
		}
		return nil
	}
	if o.Format == "html" {
		fmt.Print(reporter.RenderHTML(report, o.Last))
		if !report.OK {
			return verificationError("report completed with warnings")
		}
		return nil
	}
	fmt.Println(report.Summary)
	if report.SoakResult != nil {
		fmt.Printf("Soak Performance: %.1f RPS (%d requests in %s)\n", report.SoakResult.RPS, report.SoakResult.TotalRequests, report.SoakResult.DurationElapsed.Round(time.Millisecond))
	}
	if len(report.Warnings) > 0 {
		fmt.Println()
		fmt.Println("Warnings:")
		for _, warning := range report.Warnings {
			fmt.Println("-", warning)
		}
	}
	if !report.OK {
		return verificationError("report completed with warnings")
	}
	return nil
}
