package main

import (
	"flag"
	"fmt"

	"github.com/Anubisx404/Extent/internal/scorer"
)

type scoreOptions struct {
	Prometheus string
	Service    string
	Tempo      string
	Loki       string
	JSON       bool
}

func newScoreFlagSet(o *scoreOptions) *flag.FlagSet {
	fs := flag.NewFlagSet("score", flag.ContinueOnError)
	fs.StringVar(&o.Prometheus, "prometheus", "http://localhost:9090", "Prometheus base URL")
	fs.StringVar(&o.Service, "service", "", "target service.name; enables service identity and log/trace correlation dimensions")
	fs.StringVar(&o.Tempo, "tempo", "", "Tempo base URL; resolves a correlated trace ID as detail for the correlation dimension")
	fs.StringVar(&o.Loki, "loki", "", "Loki base URL; required to measure log/trace correlation with --service")
	fs.BoolVar(&o.JSON, "json", false, "print JSON output")
	return fs
}

func runScore(args []string) error {
	var o scoreOptions
	fs := newScoreFlagSet(&o)
	if help, err := parseFlags(fs, args); help || err != nil {
		return err
	}
	if err := validatePositional("score", fs.Args(), 0); err != nil {
		return err
	}
	score := scorer.Build(scorer.Config{PrometheusURL: o.Prometheus, ServiceName: o.Service, TempoURL: o.Tempo, LokiURL: o.Loki})
	if o.JSON {
		return writeJSONSchema(schemaScore, score)
	}
	fmt.Println(score.Summary)
	for _, dimension := range score.Dimensions {
		fmt.Printf("- %s: %d/100 - %s\n", dimension.Name, dimension.Score, dimension.Detail)
	}
	return nil
}
