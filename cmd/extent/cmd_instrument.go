package main

import (
	"flag"
	"fmt"

	"github.com/Anubisx404/Extent/internal/instrumenter"
)

type instrumentOptions struct {
	JSON         bool
	Mode         string
	DryRun       bool
	Apply        bool
	Undo         bool
	ShowDiff     bool
	Experimental bool
	Force        bool
	Entrypoint   string
}

func newInstrumentFlagSet(o *instrumentOptions) *flag.FlagSet {
	fs := flag.NewFlagSet("instrument", flag.ContinueOnError)
	fs.BoolVar(&o.JSON, "json", false, "print JSON output")
	fs.StringVar(&o.Mode, "mode", "bootstrap", "instrumentation mode: zero-code, bootstrap, deep")
	fs.BoolVar(&o.DryRun, "dry-run", false, "show intended changes without writing")
	fs.BoolVar(&o.Apply, "apply", false, "apply changes explicitly")
	fs.BoolVar(&o.Undo, "undo", false, "remove generated Extent instrumentation files")
	fs.BoolVar(&o.ShowDiff, "show-diff", false, "show a simple generated-file diff where available")
	fs.BoolVar(&o.Experimental, "experimental", false, "acknowledge experimental deep-mode limitations")
	fs.BoolVar(&o.Force, "force", false, "replace conflicting Extent instrumentation outputs with exact backup")
	fs.StringVar(&o.Entrypoint, "entrypoint", "", "explicit project-relative entrypoint when detection is ambiguous")
	return fs
}

func runInstrument(args []string) error {
	var o instrumentOptions
	fs := newInstrumentFlagSet(&o)
	if help, err := parseFlags(fs, args); help || err != nil {
		return err
	}
	if err := validateEnum("mode", o.Mode, []string{"zero-code", "bootstrap", "deep"}); err != nil {
		return err
	}
	if err := validateInstrumentFlags(o.DryRun, o.Apply, o.Undo); err != nil {
		return err
	}
	if o.ShowDiff && (o.Apply || o.Undo) {
		return safetyError("--show-diff cannot be combined with --apply or --undo")
	}
	if err := validatePositional("instrument", fs.Args(), 1); err != nil {
		return err
	}
	root := firstArgOrDot(fs.Args())
	if o.Mode == "deep" && !o.Undo && !o.Experimental {
		return usageError("deep instrumentation requires --experimental")
	}
	preview := !o.Apply || o.DryRun || o.ShowDiff
	result, err := instrumenter.Instrument(root, instrumenter.Options{
		Mode:       o.Mode,
		DryRun:     preview,
		Undo:       o.Undo,
		ShowDiff:   o.ShowDiff,
		Force:      o.Force,
		Entrypoint: o.Entrypoint,
	})
	if err != nil {
		return err
	}
	if o.JSON {
		return writeJSONSchema(schemaInstrument, result)
	}
	if len(result.ChangedFiles) == 0 && len(result.Messages) == 0 {
		fmt.Println("no instrumentation changes needed")
		return nil
	}
	if result.Diff != "" {
		fmt.Print(result.Diff)
	}
	for _, message := range result.Messages {
		fmt.Println(message)
	}
	if len(result.ChangedFiles) > 0 {
		if preview && !o.Undo {
			fmt.Println("planned instrumentation files:")
		} else {
			fmt.Println("instrumented files:")
		}
		for _, path := range result.ChangedFiles {
			fmt.Println("-", path)
		}
	}
	return nil
}
