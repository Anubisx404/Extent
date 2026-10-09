package main

import (
	"flag"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Anubisx404/Extent/internal/baseline"
	"github.com/Anubisx404/Extent/internal/reporter"
)

type baselineOptions struct {
	Prometheus string
	Loki       string
	Tempo      string
	Service    string
	Last       string
	JSON       bool
}

func newBaselineFlagSet(o *baselineOptions) *flag.FlagSet {
	fs := flag.NewFlagSet("baseline", flag.ContinueOnError)
	fs.StringVar(&o.Prometheus, "prometheus", "http://localhost:9090", "Prometheus base URL")
	fs.StringVar(&o.Loki, "loki", "http://localhost:3100", "Loki base URL for log evidence")
	fs.StringVar(&o.Tempo, "tempo", "http://localhost:3200", "Tempo base URL for trace evidence")
	fs.StringVar(&o.Service, "service", "", "target service.name for scoped evidence")
	fs.StringVar(&o.Last, "last", "30m", "bounded evidence lookback")
	fs.BoolVar(&o.JSON, "json", false, "print JSON output")
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
	if strings.TrimSpace(o.Service) == "" {
		return usageError("baseline requires --service for target-scoped evidence")
	}
	root := firstArgOrDot(fs.Args())
	report := reporter.Build(reporter.Config{PrometheusURL: o.Prometheus, LokiURL: o.Loki, TempoURL: o.Tempo, IncludeEvidence: true, ServiceName: o.Service, Window: o.Last})
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
