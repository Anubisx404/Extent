package main

import (
	"flag"
	"fmt"

	"github.com/Anubisx404/Extent/internal/buildinfo"
)

type versionOptions struct {
	JSON bool
}

func newVersionFlagSet(o *versionOptions) *flag.FlagSet {
	fs := flag.NewFlagSet("version", flag.ContinueOnError)
	fs.BoolVar(&o.JSON, "json", false, "print JSON build metadata")
	return fs
}

func runVersion(args []string) error {
	var o versionOptions
	fs := newVersionFlagSet(&o)
	if help, err := parseFlags(fs, args); help || err != nil {
		return err
	}
	if err := validatePositional("version", fs.Args(), 0); err != nil {
		return err
	}
	info := buildinfo.Current()
	if o.JSON {
		return writeJSONSchema(schemaVersion, info)
	}
	fmt.Println(info.Version)
	return nil
}
