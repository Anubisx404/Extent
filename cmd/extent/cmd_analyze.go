package main

import (
	"flag"
	"fmt"
	"strings"

	"github.com/Anubisx404/Extent/internal/analyzer"
)

type analyzeOptions struct {
	JSON    bool
	Dynamic bool
}

func newAnalyzeFlagSet(o *analyzeOptions) *flag.FlagSet {
	fs := flag.NewFlagSet("analyze", flag.ContinueOnError)
	fs.BoolVar(&o.JSON, "json", false, "print JSON output")
	fs.BoolVar(&o.Dynamic, "dynamic", false, "attempt runtime reflection for registered framework routes")
	return fs
}

func runAnalyze(args []string) error {
	var o analyzeOptions
	fs := newAnalyzeFlagSet(&o)
	if help, err := parseFlags(fs, args); help || err != nil {
		return err
	}
	if err := validatePositional("analyze", fs.Args(), 1); err != nil {
		return err
	}
	root := firstArgOrDot(fs.Args())
	result, err := analyzer.AnalyzeWithOptions(root, analyzer.Options{Dynamic: o.Dynamic})
	if err != nil {
		return err
	}
	if o.JSON {
		return writeJSONSchema(schemaAnalyze, result)
	}
	fmt.Println("Service:", result.ServiceName)
	fmt.Println("Runtime:", strings.Join(result.Runtime, ", "))
	fmt.Println("Frameworks:", strings.Join(result.Frameworks, ", "))
	fmt.Println("Routes:", len(result.Routes))
	fmt.Println("Database libraries:", strings.Join(result.DatabaseLibraries, ", "))
	fmt.Println("Queues:", strings.Join(result.Queues, ", "))
	fmt.Println("Loggers:", strings.Join(result.Loggers, ", "))
	fmt.Println("External HTTP clients:", strings.Join(result.ExternalHTTPClients, ", "))
	fmt.Println("Test commands:", strings.Join(result.TestCommands, ", "))
	return nil
}
