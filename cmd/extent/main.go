package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"extent/internal/analyzer"
	"extent/internal/baseline"
	"extent/internal/cardinality"
	"extent/internal/contract"
	"extent/internal/deps"
	"extent/internal/gitops"
	"extent/internal/instrumenter"
	"extent/internal/planner"
	"extent/internal/reporter"
	"extent/internal/scanner"
	"extent/internal/scorer"
	"extent/internal/smoke"
	"extent/internal/stack"
	"extent/internal/templates"
	"extent/internal/verifier"
)

const version = "0.1.0"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		printUsage()
		return nil
	}

	switch args[0] {
	case "version":
		fmt.Println(version)
		return nil
	case "doctor":
		return runVerify(args[1:])
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
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func runScan(args []string) error {
	fs := flag.NewFlagSet("scan", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "print JSON output")
	if err := fs.Parse(args); err != nil {
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
	if err := fs.Parse(args); err != nil {
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
	profile := fs.String("profile", "full", "observability profile: minimal, full, high-cardinality-safe, low-resource, report-heavy")
	if err := fs.Parse(args); err != nil {
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
	analysis, _ := analyzer.Analyze(absRoot)
	contractYAML := contract.Render(analysis)

	if *branch != "" {
		if err := gitops.EnsureBranch(absRoot, *branch); err != nil {
			return err
		}
	}

	written, err := templates.WriteLGTM(absRoot, plan, templates.WriteOptions{Overwrite: *force, Profile: *profile, Contract: contractYAML})
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
	if err := fs.Parse(args); err != nil {
		return err
	}
	root := firstArgOrDot(fs.Args())
	result, err := analyzer.Analyze(root)
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
		return errors.New("stack requires one of: up, down, status")
	}
	action := args[0]
	root := "."
	if len(args) > 1 {
		root = args[1]
	}
	switch action {
	case "up":
		return stack.Up(root)
	case "down":
		return stack.Down(root)
	case "status":
		return stack.Status(root)
	default:
		return fmt.Errorf("unknown stack action %q", action)
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
	if err := fs.Parse(args); err != nil {
		return err
	}
	root := firstArgOrDot(fs.Args())
	_ = apply
	result, err := instrumenter.Instrument(root, instrumenter.Options{Mode: *mode, DryRun: *dryRun, Undo: *undo, ShowDiff: *showDiff, RunDeepCodemods: *apply})
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
	fmt.Println("instrumented files:")
	for _, path := range result.ChangedFiles {
		fmt.Println("-", path)
	}
	return nil
}

func runDeps(args []string) error {
	fs := flag.NewFlagSet("deps", flag.ContinueOnError)
	install := fs.Bool("install", false, "run the detected package-manager install command")
	if err := fs.Parse(args); err != nil {
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
	requests := fs.Int("requests", 3, "number of app requests to send")
	jsonOut := fs.Bool("json", false, "print JSON output")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*targetURL) == "" {
		return errors.New("smoke requires --url")
	}
	report := smoke.Run(smoke.Config{URL: *targetURL, PrometheusURL: *promURL, Requests: *requests})
	if *jsonOut {
		return writeJSON(report)
	}
	for _, check := range report.Checks {
		status := "FAIL"
		if check.OK {
			status = "OK"
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
	if !report.OK {
		return errors.New("telemetry smoke verification failed")
	}
	return nil
}

func runReport(args []string) error {
	fs := flag.NewFlagSet("report", flag.ContinueOnError)
	promURL := fs.String("prometheus", "http://localhost:9090", "Prometheus base URL")
	lokiURL := fs.String("loki", "", "Loki base URL for log evidence")
	tempoURL := fs.String("tempo", "", "Tempo base URL for trace evidence")
	format := fs.String("format", "text", "report format: text, markdown, html, json")
	last := fs.String("last", "30m", "lookback window label for report output")
	compare := fs.String("compare", "", "compare against a baseline file or the word baseline")
	includeData := fs.Bool("include-data", false, "include raw gathered app data appendix")
	jsonOut := fs.Bool("json", false, "print JSON output")
	if err := fs.Parse(args); err != nil {
		return err
	}
	root := firstArgOrDot(fs.Args())
	baselinePath := ""
	if *compare == "baseline" {
		baselinePath = filepath.Join(root, ".extent", "baseline.json")
	} else if strings.TrimSpace(*compare) != "" {
		baselinePath = *compare
	}
	report := reporter.Build(reporter.Config{PrometheusURL: *promURL, LokiURL: *lokiURL, TempoURL: *tempoURL, IncludeEvidence: *lokiURL != "" || *tempoURL != "", BaselinePath: baselinePath})
	if *includeData {
		report.ApplicationData = gatherApplicationData(root, baselinePath, report)
	}
	if *jsonOut || *format == "json" {
		return writeJSON(report)
	}
	if *format == "markdown" {
		fmt.Print(reporter.RenderMarkdown(report, *last))
		return nil
	}
	if *format == "html" {
		fmt.Print(reporter.RenderHTML(report, *last))
		return nil
	}
	fmt.Println(report.Summary)
	if len(report.Warnings) > 0 {
		fmt.Println()
		fmt.Println("Warnings:")
		for _, warning := range report.Warnings {
			fmt.Println("-", warning)
		}
	}
	if !report.OK {
		return errors.New("report completed with warnings")
	}
	return nil
}

func gatherApplicationData(root, baselinePath string, report reporter.Report) map[string]any {
	data := map[string]any{
		"report": map[string]any{
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
	lokiURL := fs.String("loki", "", "Loki base URL for log evidence")
	tempoURL := fs.String("tempo", "", "Tempo base URL for trace evidence")
	jsonOut := fs.Bool("json", false, "print JSON output")
	if err := fs.Parse(args); err != nil {
		return err
	}
	root := firstArgOrDot(fs.Args())
	report := reporter.Build(reporter.Config{PrometheusURL: *promURL, LokiURL: *lokiURL, TempoURL: *tempoURL, IncludeEvidence: *lokiURL != "" || *tempoURL != ""})
	snapshot := baseline.Snapshot{
		ServiceName:      filepath.Base(root),
		TraceCoverage:    boolFloat(len(report.Evidence.Traces) > 0),
		LogCorrelation:   logCorrelation(report),
		DBSpanCount:      int(report.DBLatency),
		SlowDBOperations: int(report.DBShare * 10),
		NPlusOneFindings: int(report.DBShare * 3),
		HealthScore:      100 - len(report.Warnings)*10,
	}
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
	if err := fs.Parse(args); err != nil {
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

func boolFloat(value bool) float64 {
	if value {
		return 1
	}
	return 0
}

func logCorrelation(report reporter.Report) float64 {
	if len(report.Evidence.LogAnomalies) == 0 {
		return 0
	}
	correlated := 0
	for _, anomaly := range report.Evidence.LogAnomalies {
		if anomaly.TraceID != "" {
			correlated++
		}
	}
	return float64(correlated) / float64(len(report.Evidence.LogAnomalies))
}

func runScore(args []string) error {
	fs := flag.NewFlagSet("score", flag.ContinueOnError)
	promURL := fs.String("prometheus", "http://localhost:9090", "Prometheus base URL")
	jsonOut := fs.Bool("json", false, "print JSON output")
	if err := fs.Parse(args); err != nil {
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
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "print JSON output")
	targetURL := fs.String("url", "", "application URL to request for end-to-end telemetry verification")
	promURL := fs.String("prometheus", "http://localhost:9090", "Prometheus base URL")
	lokiURL := fs.String("loki", "http://localhost:3100", "Loki base URL")
	tempoURL := fs.String("tempo", "http://localhost:3200", "Tempo base URL")
	grafanaURL := fs.String("grafana", "", "Grafana base URL for live datasource correlation validation")
	requests := fs.Int("requests", 3, "number of synthetic app requests when --url is set")
	if err := fs.Parse(args); err != nil {
		return err
	}
	root := firstArgOrDot(fs.Args())
	report := verifier.VerifyConfig(verifier.Config{Root: root, URL: *targetURL, PrometheusURL: *promURL, LokiURL: *lokiURL, TempoURL: *tempoURL, GrafanaURL: *grafanaURL, Requests: *requests})
	if *jsonOut {
		return writeJSON(report)
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
		return errors.New("verification failed")
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
	fmt.Print(`Extent ` + version + `

Usage:
  extent scan [--json] [repo]
  extent analyze [--json] [repo]
  extent plan [--json] [repo]
  extent apply [--branch name] [--force] [--profile name] [repo]
  extent instrument [--mode zero-code|bootstrap|deep] [--dry-run] [--apply] [--undo] [--show-diff] [--json] [repo]
  extent deps [--install] [repo]
  extent smoke --url app-url [--prometheus url] [--requests n] [--json]
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
