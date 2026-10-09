package main

import (
	"flag"
	"fmt"
	"strings"

	"github.com/Anubisx404/Extent/internal/planner"
	"github.com/Anubisx404/Extent/internal/scanner"
)

func printPlan(plan planner.Plan) {
	fmt.Println("Detected:", strings.Join(plan.Detected, ", "))
	fmt.Println()
	fmt.Println("Proposed changes:")
	for _, change := range plan.Changes {
		fmt.Printf("- %s: %s\n", change.Path, change.Reason)
	}
	fmt.Println()
	fmt.Println("Next commands:")
	for _, cmd := range plan.NextCommands {
		fmt.Println("-", cmd)
	}
}

type planOptions struct {
	JSON bool
}

func newPlanFlagSet(o *planOptions) *flag.FlagSet {
	fs := flag.NewFlagSet("plan", flag.ContinueOnError)
	fs.BoolVar(&o.JSON, "json", false, "print JSON output")
	return fs
}

func runPlan(args []string) error {
	var o planOptions
	fs := newPlanFlagSet(&o)
	if help, err := parseFlags(fs, args); help || err != nil {
		return err
	}
	if err := validatePositional("plan", fs.Args(), 1); err != nil {
		return err
	}
	root := firstArgOrDot(fs.Args())
	result, err := scanner.Scan(root)
	if err != nil {
		return err
	}
	plan := planner.Build(result)
	if o.JSON {
		return writeJSONSchema(schemaPlan, plan)
	}
	printPlan(plan)
	return nil
}
