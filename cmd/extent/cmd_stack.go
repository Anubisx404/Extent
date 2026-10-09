package main

import (
	"flag"
	"fmt"
	"strings"
	"time"

	"github.com/Anubisx404/Extent/internal/stack"
)

type stackUpOptions struct {
	Wait        bool
	Timeout     time.Duration
	ProjectName string
}

func newStackUpFlagSet(o *stackUpOptions) *flag.FlagSet {
	fs := flag.NewFlagSet("stack up", flag.ContinueOnError)
	fs.BoolVar(&o.Wait, "wait", true, "wait for service health checks")
	fs.DurationVar(&o.Timeout, "timeout", 2*time.Minute, "bounded startup/readiness timeout")
	fs.StringVar(&o.ProjectName, "project-name", "", "explicit deterministic Compose project name")
	return fs
}

type stackStatusOptions struct {
	JSON bool
}

func newStackStatusFlagSet(o *stackStatusOptions) *flag.FlagSet {
	fs := flag.NewFlagSet("stack status", flag.ContinueOnError)
	fs.BoolVar(&o.JSON, "json", false, "print structured component state and health")
	return fs
}

func newStackDownFlagSet() *flag.FlagSet {
	return flag.NewFlagSet("stack down", flag.ContinueOnError)
}

func runStack(args []string) error {
	if len(args) == 0 {
		return usageError("stack requires one of: up, down, status")
	}
	if args[0] == "-h" || args[0] == "--help" {
		for _, line := range usageLines {
			if strings.HasPrefix(line, "stack ") {
				fmt.Println("Usage: extent " + line)
			}
		}
		return nil
	}
	switch args[0] {
	case "up":
		var o stackUpOptions
		fs := newStackUpFlagSet(&o)
		if help, err := parseFlags(fs, args[1:]); help || err != nil {
			return err
		}
		if err := validatePositional("stack up", fs.Args(), 1); err != nil {
			return err
		}
		if o.Timeout <= 0 || o.Timeout > 30*time.Minute {
			return usageError("--timeout must be greater than zero and at most 30m")
		}
		root := firstArgOrDot(fs.Args())
		if err := stack.UpConfigured(root, stack.UpOptions{Wait: o.Wait, Timeout: o.Timeout, ProjectName: o.ProjectName}); err != nil {
			return unavailableError(err.Error())
		}
		fmt.Println(stack.GrafanaHint(root))
		return nil
	case "down":
		fs := newStackDownFlagSet()
		if help, err := parseFlags(fs, args[1:]); help || err != nil {
			return err
		}
		if err := validatePositional("stack down", fs.Args(), 1); err != nil {
			return err
		}
		if err := stack.Down(firstArgOrDot(fs.Args())); err != nil {
			return unavailableError(err.Error())
		}
		return nil
	case "status":
		var o stackStatusOptions
		fs := newStackStatusFlagSet(&o)
		if help, err := parseFlags(fs, args[1:]); help || err != nil {
			return err
		}
		if err := validatePositional("stack status", fs.Args(), 1); err != nil {
			return err
		}
		report, err := stack.Inspect(firstArgOrDot(fs.Args()))
		if err != nil {
			return unavailableError(err.Error())
		}
		if o.JSON {
			return writeJSONSchema(schemaStackStatus, report)
		}
		fmt.Println("Compose project:", report.Project)
		if len(report.Components) == 0 {
			fmt.Println("No stack components are running.")
		}
		for _, component := range report.Components {
			fmt.Printf("- %s: state=%s", component.Service, component.State)
			if component.Health != "" {
				fmt.Printf(" health=%s", component.Health)
			}
			if component.ExitCode != 0 {
				fmt.Printf(" exit=%d", component.ExitCode)
			}
			fmt.Println()
		}
		return nil
	default:
		return usageError(fmt.Sprintf("unknown stack action %q", args[0]))
	}
}
