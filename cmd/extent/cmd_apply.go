package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Anubisx404/Extent/internal/analyzer"
	"github.com/Anubisx404/Extent/internal/config"
	"github.com/Anubisx404/Extent/internal/contract"
	"github.com/Anubisx404/Extent/internal/gitops"
	"github.com/Anubisx404/Extent/internal/planner"
	"github.com/Anubisx404/Extent/internal/scanner"
	"github.com/Anubisx404/Extent/internal/templates"
)

type applyOptions struct {
	Branch  string
	Force   bool
	Profile string
}

func newApplyFlagSet(o *applyOptions) *flag.FlagSet {
	fs := flag.NewFlagSet("apply", flag.ContinueOnError)
	fs.StringVar(&o.Branch, "branch", "", "create or switch to a git branch before writing files")
	fs.BoolVar(&o.Force, "force", false, "overwrite generated observability files")
	fs.StringVar(&o.Profile, "profile", "", "override extent.yaml profile: minimal, full, high-cardinality-safe, low-resource, report-heavy")
	return fs
}

func runApply(args []string) error {
	var o applyOptions
	fs := newApplyFlagSet(&o)
	if help, err := parseFlags(fs, args); help || err != nil {
		return err
	}
	if o.Profile != "" {
		if err := validateEnum("profile", o.Profile, []string{"minimal", "full", "high-cardinality-safe", "low-resource", "report-heavy", "no-docker"}); err != nil {
			return err
		}
	}
	if err := validatePositional("apply", fs.Args(), 1); err != nil {
		return err
	}
	root := firstArgOrDot(fs.Args())
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return err
	}

	result, err := scanner.Scan(absRoot)
	if err != nil {
		return err
	}
	plan := planner.Build(result)
	analysis, err := analyzer.Analyze(absRoot)
	if err != nil {
		return err
	}
	resolved := contract.Build(analysis)
	contractYAML := contract.Render(analysis)
	contractOverwrite := false
	configPath := filepath.Join(absRoot, "extent.yaml")
	if data, readErr := os.ReadFile(configPath); readErr == nil {
		loaded, parseErr := config.Parse(data)
		if parseErr != nil {
			return usageError(fmt.Sprintf("invalid extent.yaml: %v", parseErr))
		}
		resolved = loaded
		contractYAML = string(data)
	} else if !os.IsNotExist(readErr) {
		return readErr
	}
	if o.Profile != "" && resolved.Profile.Name != o.Profile {
		resolved.Profile.Name = o.Profile
		data, marshalErr := config.Marshal(resolved)
		if marshalErr != nil {
			return usageError(marshalErr.Error())
		}
		contractYAML = string(data)
		contractOverwrite = true
	}
	if err := validateRecipeSelections(resolved.Profile.Recipes); err != nil {
		return err
	}

	if o.Branch != "" {
		if err := gitops.EnsureBranch(absRoot, o.Branch); err != nil {
			return err
		}
	}

	written, err := templates.WriteLGTM(absRoot, plan, templates.WriteOptions{
		Overwrite:         o.Force,
		ContractOverwrite: contractOverwrite,
		Profile:           resolved.Profile.Name,
		Contract:          contractYAML,
	})
	if err != nil {
		return err
	}
	fmt.Println("wrote observability files:")
	for _, path := range written {
		fmt.Println("-", path)
	}
	return nil
}
