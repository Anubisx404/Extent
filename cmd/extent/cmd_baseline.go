package main

import (
	"flag"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/Anubisx404/Extent/internal/baseline"
	"github.com/Anubisx404/Extent/internal/reporter"
)

type baselineOptions struct {
	Prometheus  string
	Loki        string
	Tempo       string
	Service     string
	Last        string
	JSON        bool
	Local       bool
	Soak        time.Duration
	URL         string
	Method      string
	Timeout     time.Duration
	Concurrency int
	Rate        float64
}

func newBaselineFlagSet(o *baselineOptions) *flag.FlagSet {
	fs := flag.NewFlagSet("baseline", flag.ContinueOnError)
	fs.StringVar(&o.Prometheus, "prometheus", "http://localhost:9090", "Prometheus base URL")
	fs.StringVar(&o.Loki, "loki", "http://localhost:3100", "Loki base URL for log evidence")
	fs.StringVar(&o.Tempo, "tempo", "http://localhost:3200", "Tempo base URL for trace evidence")
	fs.StringVar(&o.Service, "service", "", "target service.name for scoped evidence")
	fs.StringVar(&o.Last, "last", "30m", "bounded evidence lookback")
	fs.BoolVar(&o.JSON, "json", false, "print JSON output")
	fs.BoolVar(&o.Local, "local", false, "capture local HTTP benchmark baseline without Prometheus, Tempo, or Loki (requires --url)")
	fs.DurationVar(&o.Soak, "soak", 0, "sustained soak duration to capture load profile (requires --url)")
	fs.StringVar(&o.URL, "url", "", "target application URL for baseline soak testing")
	fs.StringVar(&o.Method, "method", http.MethodGet, "HTTP method for baseline soak requests")
	fs.DurationVar(&o.Timeout, "timeout", 5*time.Second, "per-request HTTP timeout for baseline soak requests")
	fs.IntVar(&o.Concurrency, "concurrency", 1, "concurrent synthetic request workers for baseline soak testing")
	fs.Float64Var(&o.Rate, "rate", unsetRate, "rate limit in requests per second for baseline soak testing; 0 means unlimited. Unset defaults to 50 with --soak, unlimited otherwise")
	return fs
}

func runBaseline(args []string) error {
	var o baselineOptions
	fs := newBaselineFlagSet(&o)
	if help, err := parseFlags(fs, args); help || err != nil {
		return err
	}
	if err := validatePositional("baseline", fs.Args(), 1); err != nil {
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
			return usageError("baseline --local requires --url")
		}
		if o.Soak == 0 {
			o.Soak = 5 * time.Second
		}
		if strings.TrimSpace(o.Service) == "" {
			o.Service = defaultLocalServiceName(root, o.URL)
		}
	} else {
		if strings.TrimSpace(o.Service) == "" {
			return usageError("baseline requires --service for target-scoped evidence")
		}
		if o.Soak > 0 && strings.TrimSpace(o.URL) == "" {
			return usageError("baseline --soak requires --url")
		}
	}
	rate, err := resolveRate(fs, o.Rate, o.Soak)
	if err != nil {
		return err
	}
	report := reporter.Build(reporter.Config{
		PrometheusURL:   o.Prometheus,
		LokiURL:         o.Loki,
		TempoURL:        o.Tempo,
		IncludeEvidence: !o.Local,
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
	if !report.OK {
		return verificationError("baseline evidence collection failed: " + strings.Join(report.Warnings, "; "))
	}
	snapshot := reporter.SnapshotFromReport(report)
	if err := baseline.Save(root, snapshot); err != nil {
		return err
	}
	if o.JSON {
		return writeJSONSchema(schemaBaseline, snapshot)
	}
	fmt.Println("saved baseline:", filepath.Join(root, ".extent", "baseline.json"))
	return nil
}
