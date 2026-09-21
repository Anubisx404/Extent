package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Anubisx404/Extent/internal/analyzer"
	"github.com/Anubisx404/Extent/internal/baseline"
	"github.com/Anubisx404/Extent/internal/buildinfo"
	"github.com/Anubisx404/Extent/internal/cardinality"
	"github.com/Anubisx404/Extent/internal/config"
	"github.com/Anubisx404/Extent/internal/contract"
	"github.com/Anubisx404/Extent/internal/deps"
	"github.com/Anubisx404/Extent/internal/gitops"
	"github.com/Anubisx404/Extent/internal/instrumenter"
	"github.com/Anubisx404/Extent/internal/planner"
	"github.com/Anubisx404/Extent/internal/reporter"
	"github.com/Anubisx404/Extent/internal/scanner"
	"github.com/Anubisx404/Extent/internal/scorer"
	"github.com/Anubisx404/Extent/internal/smoke"
	"github.com/Anubisx404/Extent/internal/stack"
	"github.com/Anubisx404/Extent/internal/templates"
	"github.com/Anubisx404/Extent/internal/verifier"
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
func exitCode(err error) int {
	var e exitCategory
	if errors.As(err, &e) {
		return e.code
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

func run(args []string) error {
	if len(args) == 0 {
		printUsage()
		return nil
	}

	switch args[0] {
	case "version":
		return runVersion(args[1:])
	case "doctor":
		return runVerifyNamed("doctor", args[1:])
	case "scan":
		return runScan(args[1:])
	case "analyze":
		return runAnalyze(args[1:])
	case "plan":
		return runPlan(args[1:])
	case "apply":
		return runApply(args[1:])
	case "instrument":
		return runInstrument(args[1:])
	case "deps":
		return runDeps(args[1:])
	case "smoke":
		return runSmoke(args[1:])
	case "report":
		return runReport(args[1:])
	case "baseline":
		return runBaseline(args[1:])
	case "cardinality":
		return runCardinality(args[1:])
	case "score":
		return runScore(args[1:])
	case "stack":
		return runStack(args[1:])
	case "verify":
		return runVerify(args[1:])
	case "help", "-h", "--help":
		printUsage()
		return nil
	default:
		return usageError(fmt.Sprintf("unknown command %q", args[0]))
	}
}

func runVersion(args []string) error {
	fs := flag.NewFlagSet("version", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "print JSON build metadata")
	if help, err := parseFlags(fs, args); help || err != nil {
		return err
	}
	if err := validatePositional("version", fs.Args(), 0); err != nil {
		return err
	}
	info := buildinfo.Current()
	if *jsonOut {
		return writeJSON(info)
	}
	fmt.Println(info.Version)
	return nil
}

func runScan(args []string) error {
	fs := flag.NewFlagSet("scan", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "print JSON output")
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
	if *jsonOut {
		return writeJSON(result)
	}
	printScan(result)
	return nil
}

func runPlan(args []string) error {
	fs := flag.NewFlagSet("plan", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "print JSON output")
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
	if *jsonOut {
		return writeJSON(plan)
	}
	printPlan(plan)
	return nil
}

func runApply(args []string) error {
	fs := flag.NewFlagSet("apply", flag.ContinueOnError)
	branch := fs.String("branch", "", "create or switch to a git branch before writing files")
	force := fs.Bool("force", false, "overwrite generated observability files")
	profile := fs.String("profile", "", "override extent.yaml profile: minimal, full, high-cardinality-safe, low-resource, report-heavy")
	if help, err := parseFlags(fs, args); help || err != nil {
		return err
	}
	if *profile != "" {
		if err := validateEnum("profile", *profile, []string{"minimal", "full", "high-cardinality-safe", "low-resource", "report-heavy", "no-docker"}); err != nil {
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
	if *profile != "" && resolved.Profile.Name != *profile {
		resolved.Profile.Name = *profile
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

	if *branch != "" {
		if err := gitops.EnsureBranch(absRoot, *branch); err != nil {
			return err
		}
	}

	written, err := templates.WriteLGTM(absRoot, plan, templates.WriteOptions{
		Overwrite:         *force,
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

func runAnalyze(args []string) error {
	fs := flag.NewFlagSet("analyze", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "print JSON output")
	dynamic := fs.Bool("dynamic", false, "attempt runtime reflection for registered framework routes")
	if help, err := parseFlags(fs, args); help || err != nil {
		return err
	}
	if err := validatePositional("analyze", fs.Args(), 1); err != nil {
		return err
	}
	root := firstArgOrDot(fs.Args())
	result, err := analyzer.AnalyzeWithOptions(root, analyzer.Options{Dynamic: *dynamic})
	if err != nil {
		return err
	}
	if *jsonOut {
		return writeJSON(result)
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

func runStack(args []string) error {
	if len(args) == 0 {
		return usageError("stack requires one of: up, down, status")
	}
	if args[0] == "-h" || args[0] == "--help" {
		fmt.Println("Usage: extent stack up [--wait=true] [--timeout 2m] [--project-name name] [repo] | down [repo] | status [--json] [repo]")
		return nil
	}
	switch args[0] {
	case "up":
		fs := flag.NewFlagSet("stack up", flag.ContinueOnError)
		wait := fs.Bool("wait", true, "wait for service health checks")
		timeout := fs.Duration("timeout", 2*time.Minute, "bounded startup/readiness timeout")
		projectName := fs.String("project-name", "", "explicit deterministic Compose project name")
		if help, err := parseFlags(fs, args[1:]); help || err != nil {
			return err
		}
		if err := validatePositional("stack up", fs.Args(), 1); err != nil {
			return err
		}
		if *timeout <= 0 || *timeout > 30*time.Minute {
			return usageError("--timeout must be greater than zero and at most 30m")
		}
		root := firstArgOrDot(fs.Args())
		if err := stack.UpConfigured(root, stack.UpOptions{Wait: *wait, Timeout: *timeout, ProjectName: *projectName}); err != nil {
			return unavailableError(err.Error())
		}
		return nil
	case "down":
		fs := flag.NewFlagSet("stack down", flag.ContinueOnError)
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
		fs := flag.NewFlagSet("stack status", flag.ContinueOnError)
		jsonOut := fs.Bool("json", false, "print structured component state and health")
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
		if *jsonOut {
			return writeJSON(report)
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

func runInstrument(args []string) error {
	fs := flag.NewFlagSet("instrument", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "print JSON output")
	mode := fs.String("mode", "bootstrap", "instrumentation mode: zero-code, bootstrap, deep")
	dryRun := fs.Bool("dry-run", false, "show intended changes without writing")
	apply := fs.Bool("apply", false, "apply changes explicitly")
	undo := fs.Bool("undo", false, "remove generated Extent instrumentation files")
	showDiff := fs.Bool("show-diff", false, "show a simple generated-file diff where available")
	experimental := fs.Bool("experimental", false, "acknowledge experimental deep-mode limitations")
	force := fs.Bool("force", false, "replace conflicting Extent instrumentation outputs with exact backup")
	entrypoint := fs.String("entrypoint", "", "explicit project-relative entrypoint when detection is ambiguous")
	if help, err := parseFlags(fs, args); help || err != nil {
		return err
	}
	if err := validateEnum("mode", *mode, []string{"zero-code", "bootstrap", "deep"}); err != nil {
		return err
	}
	if err := validateInstrumentFlags(*dryRun, *apply, *undo); err != nil {
		return err
	}
	if *showDiff && (*apply || *undo) {
		return safetyError("--show-diff cannot be combined with --apply or --undo")
	}
	if err := validatePositional("instrument", fs.Args(), 1); err != nil {
		return err
	}
	root := firstArgOrDot(fs.Args())
	if *mode == "deep" && !*undo && !*experimental {
		return usageError("deep instrumentation requires --experimental")
	}
	preview := !*apply || *dryRun || *showDiff
	result, err := instrumenter.Instrument(root, instrumenter.Options{
		Mode:       *mode,
		DryRun:     preview,
		Undo:       *undo,
		ShowDiff:   *showDiff,
		Force:      *force,
		Entrypoint: *entrypoint,
	})
	if err != nil {
		return err
	}
	if *jsonOut {
		return writeJSON(result)
	}
	if len(result.ChangedFiles) == 0 {
		fmt.Println("no instrumentation changes needed")
		return nil
	}
	if result.Diff != "" {
		fmt.Print(result.Diff)
	}
	for _, message := range result.Messages {
		fmt.Println(message)
	}
	if preview && !*undo {
		fmt.Println("planned instrumentation files:")
	} else {
		fmt.Println("instrumented files:")
	}
	for _, path := range result.ChangedFiles {
		fmt.Println("-", path)
	}
	return nil
}

func runDeps(args []string) error {
	fs := flag.NewFlagSet("deps", flag.ContinueOnError)
	install := fs.Bool("install", false, "run the detected package-manager install command")
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
	if *install {
		if _, err := deps.Install(result.Root, result.PackageManagers); err != nil {
			return err
		}
		fmt.Println("dependencies installed")
	}
	return nil
}

func runSmoke(args []string) error {
	fs := flag.NewFlagSet("smoke", flag.ContinueOnError)
	targetURL := fs.String("url", "", "application URL to request")
	promURL := fs.String("prometheus", "http://localhost:9090", "Prometheus base URL")
	tempoURL := fs.String("tempo", "http://localhost:3200", "Tempo base URL")
	lokiURL := fs.String("loki", "http://localhost:3100", "Loki base URL")
	serviceName := fs.String("service", "", "target service.name for correlated trace verification")
	requests := fs.Int("requests", 3, "number of app requests to send")
	duration := fs.Duration("duration", 0, "sustained soak duration")
	concurrency := fs.Int("concurrency", 1, "concurrent synthetic request workers")
	rateLimit := fs.Float64("rate", 0, "rate limit in requests per second")
	jsonOut := fs.Bool("json", false, "print JSON output")
	if help, err := parseFlags(fs, args); help || err != nil {
		return err
	}
	if err := validatePositional("smoke", fs.Args(), 0); err != nil {
		return err
	}
	if *duration <= 0 {
		if err := validateRequests(*requests); err != nil {
			return err
		}
	}
	if *duration < 0 {
		return usageError("--duration cannot be negative")
	}
	if *concurrency < 1 {
		return usageError("--concurrency must be at least 1")
	}
	if *rateLimit < 0 {
		return usageError("--rate cannot be negative")
	}
	if strings.TrimSpace(*targetURL) == "" {
		return errors.New("smoke requires --url")
	}
	if strings.TrimSpace(*serviceName) == "" {
		return usageError("smoke requires --service for target-specific verification")
	}
	report := smoke.Run(smoke.Config{
		URL:           *targetURL,
		PrometheusURL: *promURL,
		TempoURL:      *tempoURL,
		LokiURL:       *lokiURL,
		ServiceName:   *serviceName,
		Requests:      *requests,
		Duration:      *duration,
		Concurrency:   *concurrency,
		RateLimit:     *rateLimit,
	})
	if *jsonOut {
		if err := writeJSON(report); err != nil {
			return err
		}
		if !report.OK {
			return verificationError("telemetry smoke verification failed")
		}
		return nil
	}
	for _, check := range report.Checks {
		status := "FAIL"
		if check.OK {
			status = "OK"
		} else if check.Scope == "global" && check.Status != "" {
			status = strings.ToUpper(check.Status)
		}
		fmt.Printf("[%s] %s", status, check.Name)
		if check.Value != 0 {
			fmt.Printf(" - %.0f", check.Value)
		}
		if check.Detail != "" {
			fmt.Printf(" - %s", check.Detail)
		}
		fmt.Println()
	}
	if report.DurationElapsed > 0 || report.TotalRequests > 0 {
		fmt.Printf("Throughput: %.1f RPS (%d requests in %s)\n", report.RPS, report.TotalRequests, report.DurationElapsed.Round(time.Millisecond))
	}
	if !report.OK {
		return verificationError("telemetry smoke verification failed")
	}
	return nil
}

func runReport(args []string) error {
	fs := flag.NewFlagSet("report", flag.ContinueOnError)
	promURL := fs.String("prometheus", "http://localhost:9090", "Prometheus base URL")
	lokiURL := fs.String("loki", "", "Loki base URL for log evidence")
	tempoURL := fs.String("tempo", "", "Tempo base URL for trace evidence")
	serviceName := fs.String("service", "", "target service.name for scoped evidence")
	format := fs.String("format", "text", "report format: text, markdown, html, json")
	last := fs.String("last", "30m", "lookback window label for report output")
	compare := fs.String("compare", "", "compare against a baseline file or the word baseline")
	includeData := fs.Bool("include-data", false, "include raw gathered app data appendix")
	jsonOut := fs.Bool("json", false, "print JSON output")
	soak := fs.Duration("soak", 0, "sustained soak duration")
	targetURL := fs.String("url", "", "target application URL for soak testing")
	concurrency := fs.Int("concurrency", 1, "concurrent synthetic request workers for soak testing")
	rateLimit := fs.Float64("rate", 0, "rate limit in requests per second for soak testing")
	if help, err := parseFlags(fs, args); help || err != nil {
		return err
	}
	if err := validatePositional("report", fs.Args(), 1); err != nil {
		return err
	}
	if err := validateEnum("format", *format, []string{"text", "markdown", "html", "json"}); err != nil {
		return err
	}
	if strings.TrimSpace(*serviceName) == "" {
		return usageError("report requires --service for target-scoped evidence")
	}
	if *soak > 0 && strings.TrimSpace(*targetURL) == "" {
		return usageError("report --soak requires --url")
	}
	if *soak < 0 {
		return usageError("--soak cannot be negative")
	}
	if *concurrency < 1 {
		return usageError("--concurrency must be at least 1")
	}
	if *rateLimit < 0 {
		return usageError("--rate cannot be negative")
	}
	root := firstArgOrDot(fs.Args())
	baselinePath := ""
	if *compare == "baseline" {
		baselinePath = filepath.Join(root, ".extent", "baseline.json")
	} else if strings.TrimSpace(*compare) != "" {
		baselinePath = *compare
	}
	report := reporter.Build(reporter.Config{
		PrometheusURL:   *promURL,
		LokiURL:         *lokiURL,
		TempoURL:        *tempoURL,
		IncludeEvidence: *lokiURL != "" || *tempoURL != "",
		BaselinePath:    baselinePath,
		ServiceName:     *serviceName,
		Window:          *last,
		SoakDuration:    *soak,
		TargetURL:       *targetURL,
		Concurrency:     *concurrency,
		RateLimit:       *rateLimit,
	})
	if *includeData {
		report.ApplicationData = gatherApplicationData(root, baselinePath, report)
	}
	if *jsonOut || *format == "json" {
		if err := writeJSON(report); err != nil {
			return err
		}
		if !report.OK {
			return verificationError("report completed with warnings")
		}
		return nil
	}
	if *format == "markdown" {
		fmt.Print(reporter.RenderMarkdown(report, *last))
		if !report.OK {
			return verificationError("report completed with warnings")
		}
		return nil
	}
	if *format == "html" {
		fmt.Print(reporter.RenderHTML(report, *last))
		if !report.OK {
			return verificationError("report completed with warnings")
		}
		return nil
	}
	fmt.Println(report.Summary)
	if report.SoakResult != nil {
		fmt.Printf("Soak Performance: %.1f RPS (%d requests in %s)\n", report.SoakResult.RPS, report.SoakResult.TotalRequests, report.SoakResult.DurationElapsed.Round(time.Millisecond))
	}
	if len(report.Warnings) > 0 {
		fmt.Println()
		fmt.Println("Warnings:")
		for _, warning := range report.Warnings {
			fmt.Println("-", warning)
		}
	}
	if !report.OK {
		return verificationError("report completed with warnings")
	}
	return nil
}

func gatherApplicationData(root, baselinePath string, report reporter.Report) map[string]any {
	data := map[string]any{
		"report": map[string]any{
			"serviceName":        report.ServiceName,
			"summary":            report.Summary,
			"slowestPath":        report.SlowestPath,
			"slowestPathLatency": report.SlowestPathLatency,
			"dbLatency":          report.DBLatency,
			"dbShare":            report.DBShare,
			"containerCpu":       report.ContainerCPU,
			"hostCpu":            report.HostCPU,
			"evidence":           report.Evidence,
			"recommendations":    report.Recommendations,
			"comparison":         report.Comparison,
			"warnings":           report.Warnings,
			"measurements":       report.Measurements,
			"soakResult":         report.SoakResult,
		},
	}
	if scan, err := scanner.Scan(root); err == nil {
		data["scan"] = scan
	} else {
		data["scanError"] = err.Error()
	}
	if analysis, err := analyzer.Analyze(root); err == nil {
		data["analysis"] = analysis
	} else {
		data["analysisError"] = err.Error()
	}
	data["cardinality"] = cardinality.Analyze(root)
	if baselinePath != "" {
		if snapshot, err := baseline.Load(baselinePath); err == nil {
			data["baseline"] = snapshot
		} else {
			data["baselineError"] = err.Error()
		}
	}
	return data
}

func runBaseline(args []string) error {
	fs := flag.NewFlagSet("baseline", flag.ContinueOnError)
	promURL := fs.String("prometheus", "http://localhost:9090", "Prometheus base URL")
	lokiURL := fs.String("loki", "http://localhost:3100", "Loki base URL for log evidence")
	tempoURL := fs.String("tempo", "http://localhost:3200", "Tempo base URL for trace evidence")
	serviceName := fs.String("service", "", "target service.name for scoped evidence")
	last := fs.String("last", "30m", "bounded evidence lookback")
	jsonOut := fs.Bool("json", false, "print JSON output")
	if help, err := parseFlags(fs, args); help || err != nil {
		return err
	}
	if err := validatePositional("baseline", fs.Args(), 1); err != nil {
		return err
	}
	if strings.TrimSpace(*serviceName) == "" {
		return usageError("baseline requires --service for target-scoped evidence")
	}
	root := firstArgOrDot(fs.Args())
	report := reporter.Build(reporter.Config{PrometheusURL: *promURL, LokiURL: *lokiURL, TempoURL: *tempoURL, IncludeEvidence: true, ServiceName: *serviceName, Window: *last})
	if !report.OK {
		return verificationError("baseline evidence collection failed: " + strings.Join(report.Warnings, "; "))
	}
	snapshot := reporter.SnapshotFromReport(report)
	if err := baseline.Save(root, snapshot); err != nil {
		return err
	}
	if *jsonOut {
		return writeJSON(snapshot)
	}
	fmt.Println("saved baseline:", filepath.Join(root, ".extent", "baseline.json"))
	return nil
}

func runCardinality(args []string) error {
	fs := flag.NewFlagSet("cardinality", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "print JSON output")
	if help, err := parseFlags(fs, args); help || err != nil {
		return err
	}
	if err := validatePositional("cardinality", fs.Args(), 1); err != nil {
		return err
	}
	root := firstArgOrDot(fs.Args())
	report := cardinality.Analyze(root)
	if *jsonOut {
		return writeJSON(report)
	}
	fmt.Printf("Cardinality Safety Score: %d/100\n", report.Score)
	for _, finding := range report.Findings {
		fmt.Printf("- %s: %s - %s\n", finding.File, finding.Label, finding.Reason)
	}
	for _, suggestion := range report.Suggestions {
		fmt.Println("-", suggestion)
	}
	return nil
}

func runScore(args []string) error {
	fs := flag.NewFlagSet("score", flag.ContinueOnError)
	promURL := fs.String("prometheus", "http://localhost:9090", "Prometheus base URL")
	jsonOut := fs.Bool("json", false, "print JSON output")
	if help, err := parseFlags(fs, args); help || err != nil {
		return err
	}
	if err := validatePositional("score", fs.Args(), 0); err != nil {
		return err
	}
	score := scorer.Build(scorer.Config{PrometheusURL: *promURL})
	if *jsonOut {
		return writeJSON(score)
	}
	fmt.Println(score.Summary)
	for _, dimension := range score.Dimensions {
		fmt.Printf("- %s: %d/100 - %s\n", dimension.Name, dimension.Score, dimension.Detail)
	}
	return nil
}

func runVerify(args []string) error {
	return runVerifyNamed("verify", args)
}

func runVerifyNamed(command string, args []string) error {
	fs := flag.NewFlagSet(command, flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "print JSON output")
	targetURL := fs.String("url", "", "application URL to request for end-to-end telemetry verification")
	promURL := fs.String("prometheus", "http://localhost:9090", "Prometheus base URL")
	lokiURL := fs.String("loki", "http://localhost:3100", "Loki base URL")
	tempoURL := fs.String("tempo", "http://localhost:3200", "Tempo base URL")
	grafanaURL := fs.String("grafana", "", "Grafana base URL for live datasource correlation validation")
	serviceName := fs.String("service", "", "target service.name for correlated telemetry verification")
	requests := fs.Int("requests", 3, "number of synthetic app requests when --url is set")
	if help, err := parseFlags(fs, args); help || err != nil {
		return err
	}
	if err := validatePositional(command, fs.Args(), 1); err != nil {
		return err
	}
	if err := validateRequests(*requests); err != nil {
		return err
	}
	root := firstArgOrDot(fs.Args())
	if *targetURL != "" && strings.TrimSpace(*serviceName) == "" {
		return usageError(command + " requires --service when --url is set")
	}
	report := verifier.VerifyConfig(verifier.Config{Root: root, URL: *targetURL, PrometheusURL: *promURL, LokiURL: *lokiURL, TempoURL: *tempoURL, GrafanaURL: *grafanaURL, ServiceName: *serviceName, Requests: *requests})
	if *jsonOut {
		if err := writeJSON(report); err != nil {
			return err
		}
		if !report.OK {
			return verificationError("verification failed")
		}
		return nil
	}
	for _, check := range report.Checks {
		status := "FAIL"
		if check.OK {
			status = "OK"
		}
		fmt.Printf("[%s] %s", status, check.Name)
		if check.Detail != "" {
			fmt.Printf(" - %s", check.Detail)
		}
		fmt.Println()
	}
	if !report.OK {
		return verificationError("verification failed")
	}
	return nil
}

func firstArgOrDot(args []string) string {
	if len(args) == 0 || strings.TrimSpace(args[0]) == "" {
		return "."
	}
	return args[0]
}

func writeJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func printUsage() {
	fmt.Print(`Extent ` + buildinfo.Current().Version + `

Usage:
  extent scan [--json] [repo]
  extent analyze [--json] [--dynamic] [repo]
  extent plan [--json] [repo]
  extent apply [--branch name] [--force] [--profile name] [repo]
  extent instrument [--mode zero-code|bootstrap|deep] [--experimental] [--entrypoint path] [--dry-run|--apply|--undo] [--show-diff] [--json] [repo]
  extent deps [--install] [repo]
  extent smoke --url app-url --service service-name [--prometheus url] [--tempo url] [--loki url] [--requests n] [--json]
  extent report [--last 30m] [--format text|markdown|html|json] [--prometheus url] [--loki url] [--tempo url] [--compare baseline] [--include-data] [--json] [repo]
  extent baseline [--prometheus url] [--loki url] [--tempo url] [--json] [repo]
  extent cardinality [--json] [repo]
  extent score [--prometheus url] [--json]
  extent stack up|down|status [repo]
  extent verify [--url app-url] [--prometheus url] [--loki url] [--tempo url] [--grafana url] [--requests n] [--json] [repo]
  extent doctor [--json] [repo]
  extent version
`)
}

func printScan(result scanner.Result) {
	fmt.Println("Project:", result.Root)
	fmt.Println("Runtimes:", strings.Join(result.Runtimes, ", "))
	fmt.Println("Frameworks:", strings.Join(result.Frameworks, ", "))
	fmt.Println("Package managers:", strings.Join(result.PackageManagers, ", "))
	fmt.Println("Database libraries:", strings.Join(result.DatabaseLibraries, ", "))
	fmt.Println("Compose files:", strings.Join(result.ComposeFiles, ", "))
	fmt.Println("Entrypoints:", strings.Join(result.Entrypoints, ", "))
}

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
