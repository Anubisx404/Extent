package main

import (
	"errors"
	"flag"
	"fmt"
	"strings"
	"time"

	"github.com/Anubisx404/Extent/internal/smoke"
)

type smokeOptions struct {
	URL         string
	Prometheus  string
	Tempo       string
	Loki        string
	Service     string
	Requests    int
	Duration    time.Duration
	Concurrency int
	Rate        float64
	JSON        bool
}

func newSmokeFlagSet(o *smokeOptions) *flag.FlagSet {
	fs := flag.NewFlagSet("smoke", flag.ContinueOnError)
	fs.StringVar(&o.URL, "url", "", "application URL to request")
	fs.StringVar(&o.Prometheus, "prometheus", "http://localhost:9090", "Prometheus base URL")
	fs.StringVar(&o.Tempo, "tempo", "http://localhost:3200", "Tempo base URL")
	fs.StringVar(&o.Loki, "loki", "http://localhost:3100", "Loki base URL")
	fs.StringVar(&o.Service, "service", "", "target service.name for correlated trace verification")
	fs.IntVar(&o.Requests, "requests", 3, "number of app requests to send (ignored with --duration)")
	fs.DurationVar(&o.Duration, "duration", 0, "sustained soak duration instead of a fixed request count")
	fs.IntVar(&o.Concurrency, "concurrency", 1, "concurrent synthetic request workers")
	fs.Float64Var(&o.Rate, "rate", unsetRate, "rate limit in requests per second; 0 means unlimited. Unset defaults to 50 with --duration, unlimited otherwise")
	fs.BoolVar(&o.JSON, "json", false, "print JSON output")
	return fs
}

func runSmoke(args []string) error {
	var o smokeOptions
	fs := newSmokeFlagSet(&o)
	if help, err := parseFlags(fs, args); help || err != nil {
		return err
	}
	if err := validatePositional("smoke", fs.Args(), 0); err != nil {
		return err
	}
	if o.Duration <= 0 {
		if err := validateRequests(o.Requests); err != nil {
			return err
		}
	}
	if o.Duration < 0 {
		return usageError("--duration cannot be negative")
	}
	if o.Concurrency < 1 {
		return usageError("--concurrency must be at least 1")
	}
	rate, err := resolveRate(fs, o.Rate, o.Duration)
	if err != nil {
		return err
	}
	if strings.TrimSpace(o.URL) == "" {
		return errors.New("smoke requires --url")
	}
	if strings.TrimSpace(o.Service) == "" {
		return usageError("smoke requires --service for target-specific verification")
	}
	report := smoke.Run(smoke.Config{
		URL:           o.URL,
		PrometheusURL: o.Prometheus,
		TempoURL:      o.Tempo,
		LokiURL:       o.Loki,
		ServiceName:   o.Service,
		Requests:      o.Requests,
		Duration:      o.Duration,
		Concurrency:   o.Concurrency,
		RateLimit:     rate,
	})
	if o.JSON {
		if err := writeJSONSchema(schemaSmoke, report); err != nil {
			return err
		}
		if !report.OK {
			return verificationError("telemetry smoke verification failed")
		}
		return nil
	}
	for _, check := range report.Checks {
		status := "FAIL"
		if check.OK {
			status = "OK"
		} else if check.Scope == "global" && check.Status != "" {
			status = strings.ToUpper(check.Status)
		}
		fmt.Printf("[%s] %s", status, check.Name)
		if check.Value != 0 {
			fmt.Printf(" - %.0f", check.Value)
		}
		if check.Detail != "" {
			fmt.Printf(" - %s", check.Detail)
		}
		fmt.Println()
	}
	if report.DurationElapsed > 0 || report.TotalRequests > 0 {
		fmt.Printf("Throughput: %.1f RPS (%d requests in %s)\n", report.RPS, report.TotalRequests, report.DurationElapsed.Round(time.Millisecond))
	}
	if !report.OK {
		return verificationError("telemetry smoke verification failed")
	}
	return nil
}
