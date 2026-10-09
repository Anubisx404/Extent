package main

import (
	"flag"
	"fmt"

	"github.com/Anubisx404/Extent/internal/cardinality"
)

type cardinalityOptions struct {
	JSON       bool
	Prometheus string
}

func newCardinalityFlagSet(o *cardinalityOptions) *flag.FlagSet {
	fs := flag.NewFlagSet("cardinality", flag.ContinueOnError)
	fs.BoolVar(&o.JSON, "json", false, "print JSON output")
	fs.StringVar(&o.Prometheus, "prometheus", "", "Prometheus base URL for live label-cardinality analysis of http_server_duration_milliseconds_count")
	return fs
}

func runCardinality(args []string) error {
	var o cardinalityOptions
	fs := newCardinalityFlagSet(&o)
	if help, err := parseFlags(fs, args); help || err != nil {
		return err
	}
	if err := validatePositional("cardinality", fs.Args(), 1); err != nil {
		return err
	}
	root := firstArgOrDot(fs.Args())
	report := cardinality.AnalyzeWithOptions(root, cardinality.Options{PrometheusURL: o.Prometheus})
	if o.JSON {
		return writeJSONSchema(schemaCardinality, report)
	}
	fmt.Printf("Cardinality Safety Score: %d/100\n", report.Score)
	for _, finding := range report.Findings {
		fmt.Printf("- %s: %s - %s\n", finding.File, finding.Label, finding.Reason)
	}
	for _, suggestion := range report.Suggestions {
		fmt.Println("-", suggestion)
	}
	for _, warning := range report.Warnings {
		fmt.Println("warning:", warning)
	}
	return nil
}
