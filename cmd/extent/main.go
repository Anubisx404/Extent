package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Anubisx404/Extent/internal/buildinfo"
	"github.com/Anubisx404/Extent/internal/fileops"
	"github.com/Anubisx404/Extent/recipes"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(exitCode(err))
	}
}

type exitCategory struct {
	err  error
	code int
}

func (e exitCategory) Error() string         { return e.err.Error() }
func (e exitCategory) Unwrap() error         { return e.err }
func usageError(message string) error        { return exitCategory{errors.New(message), 2} }
func safetyError(message string) error       { return exitCategory{errors.New(message), 3} }
func unavailableError(message string) error  { return exitCategory{errors.New(message), 4} }
func verificationError(message string) error { return exitCategory{errors.New(message), 5} }

// Exit codes are a stable part of the CLI contract. Scripts and CI rely on them,
// so codes must not be renumbered; new categories take new numbers.
//
//	0  success (no error returned)
//	1  internal or unclassified failure (any error not created by the helpers below)
//	2  usage error: bad flags, unknown command or action, invalid enum or range,
//	   missing required flag, extra positional arguments, invalid extent.yaml
//	3  safety refusal: conflicting mutation flags (--dry-run/--apply/--undo,
//	   --show-diff with --apply or --undo), or any error matching
//	   fileops.ErrUnsafePath (symlink escape, "..", absolute or reserved paths)
//	4  unavailable: a required local dependency or service (Docker, Compose
//	   daemon, stack lifecycle) could not be used
//	5  verification failed: telemetry checks, report warnings, or baseline
//	   evidence did not pass; JSON output is still written before exiting
//
// Wrapped errors are classified by the first exitCategory found in the chain.
func exitCode(err error) int {
	var e exitCategory
	if errors.As(err, &e) {
		return e.code
	}
	if errors.Is(err, fileops.ErrUnsafePath) {
		return 3
	}
	return 1
}

func validatePositional(command string, args []string, max int) error {
	if len(args) > max {
		return usageError(fmt.Sprintf("%s accepts at most %d positional argument(s)", command, max))
	}
	return nil
}
func validateEnum(name, value string, allowed []string) error {
	for _, candidate := range allowed {
		if value == candidate {
			return nil
		}
	}
	return usageError(fmt.Sprintf("invalid --%s %q (allowed: %s)", name, value, strings.Join(allowed, ", ")))
}
func validateRequests(n int) error {
	if n < 1 || n > 1000 {
		return usageError("--requests must be between 1 and 1000")
	}
	return nil
}

func validateRecipeSelections(selected []string) error {
	if len(selected) == 0 {
		return nil
	}
	registry, err := recipes.All()
	if err != nil {
		return err
	}
	allowed := map[string]bool{}
	for _, recipe := range registry {
		allowed[recipe.Name] = true
		allowed[recipe.Runtime+"/"+recipe.Name] = true
	}
	for _, name := range selected {
		name = strings.TrimSpace(name)
		if !allowed[name] {
			return usageError(fmt.Sprintf("unknown recipe %q", name))
		}
	}
	return nil
}

func validateInstrumentFlags(dryRun, apply, undo bool) error {
	if dryRun && apply || apply && undo || dryRun && undo {
		return safetyError("--dry-run, --apply, and --undo are mutually exclusive")
	}
	return nil
}

func parseFlags(fs *flag.FlagSet, args []string) (bool, error) {
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return true, nil
		}
		return false, usageError(err.Error())
	}
	return false, nil
}

func firstArgOrDot(args []string) string {
	if len(args) == 0 || strings.TrimSpace(args[0]) == "" {
		return "."
	}
	return args[0]
}

// defaultSoakRate is the request rate used by smoke and report soak runs when
// --duration is set and --rate is not given explicitly.
const defaultSoakRate = 50

// unsetRate is the default for --rate. An explicit --rate 0 means unlimited.
const unsetRate = -1

// flagWasSet reports whether the named flag was given explicitly on the command line.
func flagWasSet(fs *flag.FlagSet, name string) bool {
	set := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			set = true
		}
	})
	return set
}

// resolveRate turns the --rate flag into the rate passed to the load generator.
// Unset rates are capped to defaultSoakRate for duration runs and are unlimited
// otherwise; an explicit value is used as given (0 is unlimited).
func resolveRate(fs *flag.FlagSet, rate float64, duration time.Duration) (float64, error) {
	if !flagWasSet(fs, "rate") {
		if duration > 0 {
			return defaultSoakRate, nil
		}
		return 0, nil
	}
	if rate < 0 {
		return 0, usageError("--rate cannot be negative")
	}
	return rate, nil
}

// usageLines is the single source of truth for the CLI usage text. Each
// command's line must mention every flag its FlagSet registers; the usage test
// enforces this.
var usageLines = []string{
	"version [--json]",
	"scan [--json] [repo]",
	"analyze [--json] [--dynamic] [repo]",
	"plan [--json] [repo]",
	"apply [--branch name] [--force] [--profile name] [repo]",
	"instrument [--mode zero-code|bootstrap|deep] [--experimental] [--entrypoint path] [--force] [--dry-run|--apply|--undo] [--show-diff] [--json] [repo]",
	"deps [--install] [repo]",
	"smoke --url app-url --service service-name [--prometheus url] [--tempo url] [--loki url] [--requests n] [--duration d] [--concurrency n] [--rate rps] [--json]",
	"report --service service-name [--last 30m] [--format text|markdown|html|json] [--prometheus url] [--loki url] [--tempo url] [--compare baseline|path] [--include-data] [--soak d --url app-url] [--concurrency n] [--rate rps] [--json] [repo]",
	"baseline --service service-name [--last 30m] [--prometheus url] [--loki url] [--tempo url] [--json] [repo]",
	"cardinality [--prometheus url] [--json] [repo]",
	"score [--service service-name] [--prometheus url] [--tempo url] [--loki url] [--json]",
	"stack up [--wait=true|false] [--timeout 2m] [--project-name name] [repo]",
	"stack down [repo]",
	"stack status [--json] [repo]",
	"verify [--url app-url --service service-name] [--prometheus url] [--loki url] [--tempo url] [--grafana url] [--grafana-user user] [--grafana-password pass] [--grafana-token token] [--requests n] [--json] [repo]",
	"doctor [--url app-url --service service-name] [--prometheus url] [--loki url] [--tempo url] [--grafana url] [--grafana-user user] [--grafana-password pass] [--grafana-token token] [--requests n] [--json] [repo]",
}

// commandFlagSets maps each usage key (command, or "stack up" style subcommand)
// to a constructor for the FlagSet that command parses.
func commandFlagSets() map[string]func() *flag.FlagSet {
	return map[string]func() *flag.FlagSet{
		"version":      func() *flag.FlagSet { var o versionOptions; return newVersionFlagSet(&o) },
		"scan":         func() *flag.FlagSet { var o scanOptions; return newScanFlagSet(&o) },
		"analyze":      func() *flag.FlagSet { var o analyzeOptions; return newAnalyzeFlagSet(&o) },
		"plan":         func() *flag.FlagSet { var o planOptions; return newPlanFlagSet(&o) },
		"apply":        func() *flag.FlagSet { var o applyOptions; return newApplyFlagSet(&o) },
		"instrument":   func() *flag.FlagSet { var o instrumentOptions; return newInstrumentFlagSet(&o) },
		"deps":         func() *flag.FlagSet { var o depsOptions; return newDepsFlagSet(&o) },
		"smoke":        func() *flag.FlagSet { var o smokeOptions; return newSmokeFlagSet(&o) },
		"report":       func() *flag.FlagSet { var o reportOptions; return newReportFlagSet(&o) },
		"baseline":     func() *flag.FlagSet { var o baselineOptions; return newBaselineFlagSet(&o) },
		"cardinality":  func() *flag.FlagSet { var o cardinalityOptions; return newCardinalityFlagSet(&o) },
		"score":        func() *flag.FlagSet { var o scoreOptions; return newScoreFlagSet(&o) },
		"stack up":     func() *flag.FlagSet { var o stackUpOptions; return newStackUpFlagSet(&o) },
		"stack down":   newStackDownFlagSet,
		"stack status": func() *flag.FlagSet { var o stackStatusOptions; return newStackStatusFlagSet(&o) },
		"verify":       func() *flag.FlagSet { var o verifyOptions; return newVerifyFlagSet("verify", &o) },
		"doctor":       func() *flag.FlagSet { var o verifyOptions; return newVerifyFlagSet("doctor", &o) },
	}
}

func printUsage() {
	fmt.Print("Extent " + buildinfo.Current().Version + "\n\nUsage:\n")
	for _, line := range usageLines {
		fmt.Println("  extent " + line)
	}
}

// commands is the command registry: the dispatcher looks up args[0] here.
// Each entry's run receives the arguments after the command name.
var commands = map[string]func([]string) error{
	"version":     runVersion,
	"doctor":      runDoctor,
	"scan":        runScan,
	"analyze":     runAnalyze,
	"plan":        runPlan,
	"apply":       runApply,
	"instrument":  runInstrument,
	"deps":        runDeps,
	"smoke":       runSmoke,
	"report":      runReport,
	"baseline":    runBaseline,
	"cardinality": runCardinality,
	"score":       runScore,
	"stack":       runStack,
	"verify":      runVerify,
}

func run(args []string) error {
	if len(args) == 0 {
		printUsage()
		return nil
	}
	if args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		printUsage()
		return nil
	}
	cmd, ok := commands[args[0]]
	if !ok {
		return usageError(fmt.Sprintf("unknown command %q", args[0]))
	}
	return cmd(args[1:])
}
