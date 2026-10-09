package main

import (
	"flag"
	"fmt"
	"strings"

	"github.com/Anubisx404/Extent/internal/scanner"
)

func printScan(result scanner.Result) {
	fmt.Println("Project:", result.Root)
	fmt.Println("Runtimes:", strings.Join(result.Runtimes, ", "))
	fmt.Println("Frameworks:", strings.Join(result.Frameworks, ", "))
	fmt.Println("Package managers:", strings.Join(result.PackageManagers, ", "))
	fmt.Println("Database libraries:", strings.Join(result.DatabaseLibraries, ", "))
	fmt.Println("Compose files:", strings.Join(result.ComposeFiles, ", "))
	fmt.Println("Entrypoints:", strings.Join(result.Entrypoints, ", "))
}

type scanOptions struct {
	JSON bool
}

func newScanFlagSet(o *scanOptions) *flag.FlagSet {
	fs := flag.NewFlagSet("scan", flag.ContinueOnError)
	fs.BoolVar(&o.JSON, "json", false, "print JSON output")
	return fs
}

func runScan(args []string) error {
	var o scanOptions
	fs := newScanFlagSet(&o)
	if help, err := parseFlags(fs, args); help || err != nil {
		return err
	}
	if err := validatePositional("scan", fs.Args(), 1); err != nil {
		return err
	}
	root := firstArgOrDot(fs.Args())
	result, err := scanner.Scan(root)
	if err != nil {
		return err
	}
	if o.JSON {
		return writeJSONSchema(schemaScan, result)
	}
	printScan(result)
	return nil
}
