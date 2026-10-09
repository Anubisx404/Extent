package main

import (
	"errors"
	"flag"
	"fmt"
	"strings"

	"github.com/Anubisx404/Extent/internal/deps"
	"github.com/Anubisx404/Extent/internal/scanner"
)

type depsOptions struct {
	Install bool
}

func newDepsFlagSet(o *depsOptions) *flag.FlagSet {
	fs := flag.NewFlagSet("deps", flag.ContinueOnError)
	fs.BoolVar(&o.Install, "install", false, "run the detected package-manager install command")
	return fs
}

func runDeps(args []string) error {
	var o depsOptions
	fs := newDepsFlagSet(&o)
	if help, err := parseFlags(fs, args); help || err != nil {
		return err
	}
	if err := validatePositional("deps", fs.Args(), 1); err != nil {
		return err
	}
	root := firstArgOrDot(fs.Args())
	result, err := scanner.Scan(root)
	if err != nil {
		return err
	}
	command, ok := deps.InstallCommand(result.PackageManagers)
	if !ok {
		return errors.New("no supported dependency install command found")
	}
	fmt.Println("install command:", strings.Join(command, " "))
	if o.Install {
		if _, err := deps.Install(result.Root, result.PackageManagers); err != nil {
			return err
		}
		fmt.Println("dependencies installed")
	}
	return nil
}
