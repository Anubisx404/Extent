package instrumenter

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/Anubisx404/Extent/internal/codemods"
	"github.com/Anubisx404/Extent/internal/fileops"
	"github.com/Anubisx404/Extent/internal/scanner"
	"golang.org/x/mod/modfile"
	"golang.org/x/mod/semver"
)

type Options struct {
	Force           bool
	Mode            string
	DryRun          bool
	Undo            bool
	ShowDiff        bool
	RunDeepCodemods bool
	Entrypoint      string
}

type Result struct {
	ChangedFiles []string `json:"changedFiles"`
	Messages     []string `json:"messages"`
	Diff         string   `json:"diff,omitempty"`
}

const marker = "extent:otel"

var nodeDependencyPins = map[string]string{
	"@opentelemetry/api":                        "1.9.1",
	"@opentelemetry/sdk-node":                   "0.220.0",
	"@opentelemetry/auto-instrumentations-node": "0.78.0",
	"@opentelemetry/exporter-trace-otlp-http":   "0.220.0",
	"@opentelemetry/exporter-metrics-otlp-http": "0.220.0",
	"@opentelemetry/exporter-logs-otlp-http":    "0.220.0",
	"@opentelemetry/sdk-logs":                   "0.220.0",
	"@opentelemetry/sdk-metrics":                "2.9.0",
	"@opentelemetry/resources":                  "2.9.0",
}
var nodeDatabasePins = map[string]string{"@opentelemetry/instrumentation-pg": "0.72.0", "@opentelemetry/instrumentation-mysql": "0.66.0", "@opentelemetry/instrumentation-mysql2": "0.66.0", "@opentelemetry/instrumentation-mongodb": "0.73.0", "@opentelemetry/instrumentation-redis": "0.68.0", "@opentelemetry/instrumentation-ioredis": "0.68.0"}
var pythonDependencyPins = map[string]string{"opentelemetry-api": "1.43.0", "opentelemetry-sdk": "1.43.0", "opentelemetry-exporter-otlp": "1.43.0", "opentelemetry-instrumentation": "0.64b0", "opentelemetry-instrumentation-fastapi": "0.64b0", "opentelemetry-instrumentation-flask": "0.64b0", "opentelemetry-instrumentation-logging": "0.64b0", "opentelemetry-instrumentation-requests": "0.64b0", "opentelemetry-instrumentation-sqlalchemy": "0.64b0", "opentelemetry-instrumentation-psycopg2": "0.64b0", "opentelemetry-instrumentation-psycopg": "0.64b0", "opentelemetry-instrumentation-asyncpg": "0.64b0", "opentelemetry-instrumentation-pymongo": "0.64b0", "opentelemetry-instrumentation-redis": "0.64b0", "opentelemetry-instrumentation-mysql": "0.64b0", "opentelemetry-instrumentation-pymysql": "0.64b0"}

var nodeDependencies = []string{
	"@opentelemetry/api",
	"@opentelemetry/sdk-node",
	"@opentelemetry/auto-instrumentations-node",
	"@opentelemetry/exporter-trace-otlp-http",
	"@opentelemetry/exporter-metrics-otlp-http",
	"@opentelemetry/exporter-logs-otlp-http",
	"@opentelemetry/sdk-logs",
	"@opentelemetry/sdk-metrics",
	"@opentelemetry/resources",
}

var pythonDependencies = []string{
	"opentelemetry-api",
	"opentelemetry-sdk",
	"opentelemetry-exporter-otlp",
	"opentelemetry-instrumentation",
	"opentelemetry-instrumentation-fastapi",
	"opentelemetry-instrumentation-flask",
	"opentelemetry-instrumentation-logging",
	"opentelemetry-instrumentation-requests",
}

var goDependencyPins = []struct {
	Path    string
	Version string
}{
	{Path: "go.opentelemetry.io/otel", Version: "v1.35.0"},
	{Path: "go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp", Version: "v1.35.0"},
	{Path: "go.opentelemetry.io/otel/sdk", Version: "v1.35.0"},
}

var nodeDatabaseInstrumentation = map[string][]string{
	"pg":       {"@opentelemetry/instrumentation-pg"},
	"mysql":    {"@opentelemetry/instrumentation-mysql"},
	"mysql2":   {"@opentelemetry/instrumentation-mysql2"},
	"mongodb":  {"@opentelemetry/instrumentation-mongodb"},
	"mongoose": {"@opentelemetry/instrumentation-mongodb"},
	"redis":    {"@opentelemetry/instrumentation-redis"},
	"ioredis":  {"@opentelemetry/instrumentation-ioredis"},
}

var pythonDatabaseInstrumentation = map[string][]string{
	"sqlalchemy":  {"opentelemetry-instrumentation-sqlalchemy"},
	"psycopg2":    {"opentelemetry-instrumentation-psycopg2"},
	"psycopg":     {"opentelemetry-instrumentation-psycopg"},
	"asyncpg":     {"opentelemetry-instrumentation-asyncpg"},
	"pymongo":     {"opentelemetry-instrumentation-pymongo"},
	"redis":       {"opentelemetry-instrumentation-redis"},
	"mysqlclient": {"opentelemetry-instrumentation-mysql"},
	"pymysql":     {"opentelemetry-instrumentation-pymysql"},
}

func Instrument(root string, opts Options) (Result, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return Result{}, err
	}
	if opts.Mode == "" {
		opts.Mode = "bootstrap"
	}
	if opts.Undo {
		if err := fileops.UndoKind(absRoot, operationKind(opts)); err != nil {
			return Result{}, err
		}
		return Result{Messages: []string{"undid " + operationKind(opts)}}, nil
	}
	if opts.RunDeepCodemods {
		return Result{}, errors.New("deep codemod execution is not transaction-protected")
	}
	plan, previews, err := Plan(absRoot, opts)
	if err != nil {
		return Result{}, err
	}
	result := Result{}
	if opts.DryRun || opts.ShowDiff {
		result.Diff = previews
		result.ChangedFiles = plannedPaths(plan)
		return result, nil
	}
	filtered := make([]fileops.Step, 0, len(plan.Steps))
	for _, step := range plan.Steps {
		before, readErr := os.ReadFile(filepath.Join(absRoot, filepath.FromSlash(step.Path)))
		if readErr == nil && bytes.Equal(before, step.Data) {
			continue
		}
		filtered = append(filtered, step)
	}
	plan.Steps = filtered
	if len(plan.Steps) == 0 {
		return result, nil
	}
	manifest, err := fileops.Apply(plan)
	if err != nil {
		return Result{}, err
	}
	if len(manifest.Operations) > 0 {
		for _, e := range manifest.Operations[len(manifest.Operations)-1].Entries {
			result.ChangedFiles = append(result.ChangedFiles, filepath.ToSlash(e.Path))
		}
	}
	return result, nil
}

func operationKind(opts Options) string {
	if opts.Mode == "" {
		return "bootstrap"
	}
	return "instrument/" + opts.Mode
}

func Plan(root string, opts Options) (fileops.Plan, string, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return fileops.Plan{}, "", err
	}
	if opts.Mode == "" {
		opts.Mode = "bootstrap"
	}
	switch opts.Mode {
	case "bootstrap", "deep", "zero-code":
	default:
		return fileops.Plan{}, "", fmt.Errorf("invalid instrumentation mode %q", opts.Mode)
	}
	if opts.Mode == "deep" {
		if !supportedRoot(absRoot) {
			return fileops.Plan{}, "", errors.New("no supported runtime detected")
		}
		return planBundle(absRoot, opts)
	}
	if !supportedRoot(absRoot) {
		return fileops.Plan{}, "", errors.New("no supported runtime detected")
	}
	steps := []fileops.Step{}
	add := func(rel, content string) error {
		old, err := os.ReadFile(filepath.Join(absRoot, filepath.FromSlash(rel)))
		action := fileops.Create
		if err == nil {
			if string(old) == content {
				return nil
			}
			action = fileops.Update
		} else if !os.IsNotExist(err) {
			return err
		}
		steps = append(steps, fileops.Step{Path: rel, Action: action, Data: []byte(content), Mode: 0644})
		return nil
	}
	if opts.Mode == "zero-code" {
		if err := add("extent.zero-code.env", zeroCodeContent(absRoot)); err != nil {
			return fileops.Plan{}, "", err
		}
	} else {
		if exists(filepath.Join(absRoot, "package.json")) {
			s, e := nodePlan(absRoot, opts)
			if e != nil {
				return fileops.Plan{}, "", e
			}
			steps = append(steps, s...)
		}
		if exists(filepath.Join(absRoot, "requirements.txt")) || exists(filepath.Join(absRoot, "main.py")) || exists(filepath.Join(absRoot, "app.py")) {
			s, e := pythonPlan(absRoot, opts)
			if e != nil {
				return fileops.Plan{}, "", e
			}
			steps = append(steps, s...)
		}
		if exists(filepath.Join(absRoot, "go.mod")) {
			s, e := goPlan(absRoot, opts)
			if e != nil {
				return fileops.Plan{}, "", e
			}
			steps = append(steps, s...)
		}
		if isDotnetProject(absRoot) {
			s, e := dotnetPlan(absRoot, opts)
			if e != nil {
				return fileops.Plan{}, "", e
			}
			steps = append(steps, s...)
		}
	}
	plan, err := fileops.NewPlan(absRoot, operationKind(opts), steps)
	if err != nil {
		return fileops.Plan{}, "", err
	}
	return plan, preview(absRoot, plan), nil
}

func plannedPaths(p fileops.Plan) []string {
	r := make([]string, 0, len(p.Steps))
	for _, s := range p.Steps {
		r = append(r, s.Path)
	}
	return r
}
func preview(root string, p fileops.Plan) string {
	var b strings.Builder
	for _, s := range p.Steps {
		old, _ := os.ReadFile(filepath.Join(root, filepath.FromSlash(s.Path)))
		b.WriteString(diffFor(s.Path, string(old), string(s.Data)))
	}
	return b.String()
}
func planBundle(root string, opts Options) (fileops.Plan, string, error) {
	steps := []fileops.Step{}
	files := codemods.BuildBundle().Files
	rels := make([]string, 0, len(files))
	for rel := range files {
		rels = append(rels, rel)
	}
	sort.Strings(rels)
	for _, rel := range rels {
		data := files[rel]
		_, e := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		a := fileops.Create
		if e == nil {
			if !opts.Force {
				return fileops.Plan{}, "", fmt.Errorf("deep bundle target exists: %s", rel)
			}
			a = fileops.Update
		}
		steps = append(steps, fileops.Step{Path: rel, Action: a, Data: []byte(data), Mode: 0644})
	}
	p, e := fileops.NewPlan(root, operationKind(opts), steps)
	if e != nil {
		return fileops.Plan{}, "", e
	}
	return p, preview(root, p), nil
}

func zeroCodeContent(root string) string {
	var b strings.Builder
	b.WriteString("# Generated by Extent for zero-code instrumentation.\n")
	if exists(filepath.Join(root, "package.json")) {
		b.WriteString("NODE_OPTIONS=--require @opentelemetry/auto-instrumentations-node/register\n")
	}
	if exists(filepath.Join(root, "requirements.txt")) || exists(filepath.Join(root, "main.py")) || exists(filepath.Join(root, "app.py")) {
		b.WriteString("OTEL_PYTHON_LOG_CORRELATION=true\n# Run with: opentelemetry-instrument python <entrypoint>\n")
	}
	if exists(filepath.Join(root, "go.mod")) {
		b.WriteString("# Go: configure OTEL_EXPORTER_OTLP_ENDPOINT and initialize the generated observability package.\n")
	}
	if isDotnetProject(root) {
		b.WriteString("# .NET: configure OTEL_EXPORTER_OTLP_ENDPOINT or use OpenTelemetry.AutoInstrumentation.\n")
	}
	return b.String()
}

func nodePlan(root string, opts Options) ([]fileops.Step, error) {
	path := filepath.Join(root, "package.json")
	data, e := os.ReadFile(path)
	if e != nil {
		return nil, e
	}
	var pkg map[string]any
	if e = json.Unmarshal(data, &pkg); e != nil {
		return nil, e
	}
	before := string(data)
	if err := validateNodePins(pkg); err != nil {
		return nil, err
	}
	addNodeDependenciesPinned(pkg)
	out, e := json.MarshalIndent(pkg, "", "  ")
	if e != nil {
		return nil, e
	}
	out = append(out, '\n')
	steps := []fileops.Step{}
	if string(out) != before {
		steps = append(steps, fileops.Step{Path: "package.json", Action: fileops.Update, Data: out, Mode: 0644})
	}
	esm, _ := pkg["type"].(string)
	name, boot, inj := "extent.instrumentation.js", nodeCJSBootstrap, `require("./extent.instrumentation"); // extent:otel`
	if esm == "module" {
		name, boot, inj = "extent.instrumentation.mjs", nodeESMBootstrap, `import "./extent.instrumentation.mjs"; // extent:otel`
	}
	if old, e := os.ReadFile(filepath.Join(root, name)); e != nil || string(old) != boot {
		a := fileops.Create
		if e == nil {
			a = fileops.Update
		}
		steps = append(steps, fileops.Step{Path: name, Action: a, Data: []byte(boot), Mode: 0644})
	}
	ep, e := selectEntrypoint(root, opts, []string{"server.js", "index.js", "src/server.js", "src/index.js"})
	if e != nil {
		return nil, e
	}
	if ep != "" {
		old, _ := os.ReadFile(filepath.Join(root, ep))
		if !strings.Contains(string(old), marker) {
			steps = append(steps, fileops.Step{Path: ep, Action: fileops.Update, Data: injectNodePreamble(old, inj), Mode: 0644})
		}
	}
	return steps, nil
}

func injectNodePreamble(source []byte, injection string) []byte {
	text := string(source)
	if strings.HasPrefix(text, "#!") {
		if newline := strings.IndexByte(text, '\n'); newline >= 0 {
			return []byte(text[:newline+1] + injection + "\n" + text[newline+1:])
		}
		return []byte(text + "\n" + injection + "\n")
	}
	return []byte(injection + "\n" + text)
}

func pythonPlan(root string, opts Options) ([]fileops.Step, error) {
	steps := []fileops.Step{}
	add := func(rel string, data []byte) error {
		old, e := os.ReadFile(filepath.Join(root, rel))
		if e == nil && bytes.Equal(old, data) {
			return nil
		}
		a := fileops.Create
		if e == nil {
			a = fileops.Update
		}
		steps = append(steps, fileops.Step{Path: rel, Action: a, Data: data, Mode: 0644})
		return nil
	}
	if e := add("extent_instrumentation.py", []byte(pythonBootstrap)); e != nil {
		return nil, e
	}
	req := filepath.Join(root, "requirements.txt")
	old, e := os.ReadFile(req)
	if e == nil {
		if err := validatePythonPins(old); err != nil {
			return nil, err
		}
		lines := mergeRequirements(string(old), append(pythonDependencies, pythonDBInstrumentationFor(req)...))
		if lines != string(old) {
			steps = append(steps, fileops.Step{Path: "requirements.txt", Action: fileops.Update, Data: []byte(lines), Mode: 0644})
		}
	}
	ep, e := selectEntrypoint(root, opts, []string{"main.py", "app.py"})
	if e != nil {
		return nil, e
	}
	if ep != "" {
		old, _ := os.ReadFile(filepath.Join(root, ep))
		if !strings.Contains(string(old), marker) {
			text := string(old)
			lines := strings.Split(text, "\n")
			idx := 0
			for idx < len(lines) && (strings.HasPrefix(lines[idx], "#!") || strings.HasPrefix(lines[idx], "# -*-") || strings.HasPrefix(strings.TrimSpace(lines[idx]), "from __future__")) {
				idx++
			}
			lines = append(lines[:idx], append([]string{"import extent_instrumentation  # extent:otel"}, lines[idx:]...)...)
			old = []byte(strings.Join(lines, "\n"))
			steps = append(steps, fileops.Step{Path: ep, Action: fileops.Update, Data: old, Mode: 0644})
		}
	}
	return steps, nil
}
func goPlan(root string, opts Options) ([]fileops.Step, error) {
	goModPath := filepath.Join(root, "go.mod")
	module := goModulePath(goModPath)
	if strings.TrimSpace(module) == "" {
		return nil, errors.New("go.mod has no valid module path")
	}
	goModBefore, err := os.ReadFile(goModPath)
	if err != nil {
		return nil, err
	}
	goModAfter, err := pinGoDependencies(goModBefore)
	if err != nil {
		return nil, err
	}
	steps := []fileops.Step{}
	if !bytes.Equal(goModBefore, goModAfter) {
		steps = append(steps, fileops.Step{Path: "go.mod", Action: fileops.Update, Data: goModAfter, Mode: 0644})
	}
	rel := "internal/observability/otel.go"
	if old, e := os.ReadFile(filepath.Join(root, rel)); e != nil || string(old) != goBootstrap {
		a := fileops.Create
		if e == nil {
			a = fileops.Update
		}
		steps = append(steps, fileops.Step{Path: rel, Action: a, Data: []byte(goBootstrap), Mode: 0644})
	}
	ep, e := selectEntrypoint(root, opts, goEntrypoints(root))
	if e != nil {
		return nil, e
	}
	if ep != "" {
		old, _ := os.ReadFile(filepath.Join(root, ep))
		imp := module + "/internal/observability"
		if !strings.Contains(string(old), strconv.Quote(imp)) || !strings.Contains(string(old), "extentotel.Shutdown()") {
			tmp := filepath.Join(root, ep)
			if e := addGoObservabilityLifecyclePure(tmp, imp, &old); e != nil {
				return nil, e
			}
			steps = append(steps, fileops.Step{Path: ep, Action: fileops.Update, Data: old, Mode: 0644})
		}
	}
	return steps, nil
}

func pinGoDependencies(data []byte) ([]byte, error) {
	parsed, err := modfile.Parse("go.mod", data, nil)
	if err != nil {
		return nil, fmt.Errorf("parse go.mod: %w", err)
	}
	existing := make(map[string]string, len(parsed.Require))
	for _, requirement := range parsed.Require {
		existing[requirement.Mod.Path] = requirement.Mod.Version
	}
	for _, pin := range goDependencyPins {
		if version, ok := existing[pin.Path]; ok {
			if version != pin.Version {
				return nil, fmt.Errorf("incompatible Go OpenTelemetry requirement %s %s; expected %s", pin.Path, version, pin.Version)
			}
			continue
		}
		if err := parsed.AddRequire(pin.Path, pin.Version); err != nil {
			return nil, fmt.Errorf("pin %s: %w", pin.Path, err)
		}
	}
	formatted, err := parsed.Format()
	if err != nil {
		return nil, fmt.Errorf("format go.mod: %w", err)
	}
	return formatted, nil
}

func addGoObservabilityLifecyclePure(path, imp string, out *[]byte) error {
	if strings.TrimSpace(imp) == "" {
		return errors.New("empty Go import path")
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, *out, parser.ParseComments)
	if err != nil {
		return err
	}
	quotedImport := strconv.Quote(imp)
	foundImport := false
	for _, existing := range f.Imports {
		if existing.Path.Value == quotedImport {
			existing.Name = ast.NewIdent("extentotel")
			foundImport = true
			break
		}
	}
	if !foundImport {
		spec := &ast.ImportSpec{Name: ast.NewIdent("extentotel"), Path: &ast.BasicLit{Kind: token.STRING, Value: quotedImport}}
		for _, declaration := range f.Decls {
			if imports, ok := declaration.(*ast.GenDecl); ok && imports.Tok == token.IMPORT {
				imports.Specs = append(imports.Specs, spec)
				foundImport = true
				break
			}
		}
		if !foundImport {
			f.Decls = append([]ast.Decl{&ast.GenDecl{Tok: token.IMPORT, Specs: []ast.Spec{spec}}}, f.Decls...)
		}
	}
	foundMain := false
	for _, declaration := range f.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if ok && function.Recv == nil && function.Name.Name == "main" && function.Body != nil {
			shutdown := &ast.DeferStmt{Call: &ast.CallExpr{Fun: &ast.SelectorExpr{X: ast.NewIdent("extentotel"), Sel: ast.NewIdent("Shutdown")}}}
			function.Body.List = append([]ast.Stmt{shutdown}, function.Body.List...)
			foundMain = true
			break
		}
	}
	if !foundMain {
		return errors.New("Go entrypoint has no main function")
	}
	var output bytes.Buffer
	if err := format.Node(&output, fset, f); err != nil {
		return err
	}
	*out = output.Bytes()
	return nil
}
func goEntrypoints(root string) []string {
	r := []string{"main.go"}
	for _, d := range []string{"cmd"} {
		ents, _ := os.ReadDir(filepath.Join(root, d))
		for _, e := range ents {
			if e.IsDir() {
				r = append(r, "cmd/"+e.Name()+"/main.go")
			}
		}
	}
	return r
}
func selectEntrypoint(root string, opts Options, c []string) (string, error) {
	if opts.Entrypoint != "" {
		for _, x := range c {
			if filepath.ToSlash(filepath.Clean(opts.Entrypoint)) == x && exists(filepath.Join(root, x)) {
				return x, nil
			}
		}
		return "", fmt.Errorf("invalid entrypoint %q", opts.Entrypoint)
	}
	found := []string{}
	for _, x := range c {
		if exists(filepath.Join(root, x)) {
			found = append(found, x)
		}
	}
	if len(found) > 1 {
		return "", fmt.Errorf("ambiguous entrypoints: %s", strings.Join(found, ", "))
	}
	if len(found) == 1 {
		return found[0], nil
	}
	return "", nil
}

func addNodeDependenciesPinned(pkg map[string]any) {
	deps, _ := pkg["dependencies"].(map[string]any)
	if deps == nil {
		deps = map[string]any{}
		pkg["dependencies"] = deps
	}
	devDeps, _ := pkg["devDependencies"].(map[string]any)
	for _, d := range nodeDependencies {
		if _, exists := dependencyConstraint(deps, devDeps, d); !exists {
			deps[d] = nodeDependencyPins[d]
		}
	}
	for app, ds := range nodeDatabaseInstrumentation {
		if _, ok := dependencyConstraint(deps, devDeps, app); ok {
			for _, d := range ds {
				if _, ok := dependencyConstraint(deps, devDeps, d); !ok {
					deps[d] = nodeDatabasePins[d]
				}
			}
		}
	}
}

func goModulePath(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "module" {
			return fields[1]
		}
	}
	return ""
}

func diffFor(path, before, after string) string {
	var b strings.Builder
	b.WriteString("--- " + path + "\n")
	b.WriteString("+++ " + path + "\n")
	if before != "" {
		b.WriteString("-" + before + "\n")
	}
	if after != "" {
		for _, line := range strings.Split(strings.TrimRight(after, "\n"), "\n") {
			b.WriteString("+" + line + "\n")
		}
	}
	return b.String()
}

func supportedRoot(root string) bool {
	return exists(filepath.Join(root, "package.json")) || exists(filepath.Join(root, "requirements.txt")) || exists(filepath.Join(root, "main.py")) || exists(filepath.Join(root, "app.py")) || exists(filepath.Join(root, "go.mod")) || isDotnetProject(root)
}
func validateNodePins(pkg map[string]any) error {
	deps, _ := pkg["dependencies"].(map[string]any)
	devDeps, _ := pkg["devDependencies"].(map[string]any)
	pins := make(map[string]string, len(nodeDependencyPins)+len(nodeDatabasePins))
	for name, version := range nodeDependencyPins {
		pins[name] = version
	}
	for name, version := range nodeDatabasePins {
		pins[name] = version
	}
	for name, expected := range pins {
		if constraint, ok := dependencyConstraint(deps, devDeps, name); ok && !nodeConstraintAllows(constraint, expected) {
			return fmt.Errorf("incompatible OpenTelemetry dependency %s=%s; expected a constraint containing %s", name, constraint, expected)
		}
	}
	return nil
}

func dependencyConstraint(deps, devDeps map[string]any, name string) (string, bool) {
	for _, collection := range []map[string]any{deps, devDeps} {
		if value, ok := collection[name]; ok {
			constraint, valid := value.(string)
			return constraint, valid
		}
	}
	return "", false
}

func nodeConstraintAllows(constraint, expected string) bool {
	constraint = strings.TrimSpace(constraint)
	want := "v" + strings.TrimPrefix(expected, "v")
	if !semver.IsValid(want) {
		return false
	}
	if constraint == expected || constraint == "v"+expected || constraint == "="+expected {
		return true
	}
	if strings.HasPrefix(constraint, "^") || strings.HasPrefix(constraint, "~") {
		kind := constraint[0]
		lower := "v" + strings.TrimPrefix(strings.TrimSpace(constraint[1:]), "v")
		if !semver.IsValid(lower) || semver.Compare(want, lower) < 0 {
			return false
		}
		wantParts := semverParts(want)
		lowerParts := semverParts(lower)
		if kind == '~' {
			return wantParts[0] == lowerParts[0] && wantParts[1] == lowerParts[1]
		}
		if lowerParts[0] > 0 {
			return wantParts[0] == lowerParts[0]
		}
		if lowerParts[1] > 0 {
			return wantParts[0] == 0 && wantParts[1] == lowerParts[1]
		}
		return wantParts == lowerParts
	}
	return false
}

func semverParts(version string) [3]int {
	core := strings.TrimPrefix(version, "v")
	if cutoff := strings.IndexAny(core, "-+"); cutoff >= 0 {
		core = core[:cutoff]
	}
	fields := strings.Split(core, ".")
	parts := [3]int{}
	for i := 0; i < len(fields) && i < len(parts); i++ {
		parts[i], _ = strconv.Atoi(fields[i])
	}
	return parts
}
func validatePythonPins(data []byte) error {
	for _, line := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
		trimmed := strings.TrimSpace(strings.SplitN(line, "#", 2)[0])
		if trimmed == "" {
			continue
		}
		withoutMarker := strings.TrimSpace(strings.SplitN(trimmed, ";", 2)[0])
		lower := strings.ToLower(withoutMarker)
		for name, want := range pythonDependencyPins {
			if !strings.HasPrefix(lower, name) {
				continue
			}
			rest := strings.TrimSpace(withoutMarker[len(name):])
			if rest == "" {
				return fmt.Errorf("unpinned OpenTelemetry requirement %s; expected %s==%s", line, name, want)
			}
			if !strings.Contains("=<>!~[", rest[:1]) {
				continue
			}
			if rest != "=="+want {
				return fmt.Errorf("incompatible OpenTelemetry requirement %s; expected %s==%s", line, name, want)
			}
		}
	}
	return nil
}
func mergeRequirements(text string, packages []string) string {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	seen := map[string]bool{}
	for i, l := range lines {
		s := strings.TrimSpace(l)
		if s == "" || strings.HasPrefix(s, "#") {
			continue
		}
		name := strings.ToLower(strings.FieldsFunc(s, func(r rune) bool { return r == '=' || r == '<' || r == '>' || r == '!' || r == '~' || r == ' ' })[0])
		seen[name] = true
		if want, ok := pythonDependencyPins[name]; ok {
			lines[i] = name + "==" + want
		}
	}
	for _, p := range packages {
		if !seen[strings.ToLower(p)] {
			lines = append(lines, p+"=="+pythonDependencyPins[strings.ToLower(p)])
			seen[strings.ToLower(p)] = true
		}
	}
	return strings.TrimRight(strings.Join(lines, "\n"), "\n") + "\n"
}

func pythonDBInstrumentationFor(path string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	text := strings.ToLower(string(data))
	var packages []string
	seen := map[string]bool{}
	for appDep, instrumentationDeps := range pythonDatabaseInstrumentation {
		if !strings.Contains(text, strings.ToLower(appDep)) {
			continue
		}
		for _, dep := range instrumentationDeps {
			if !seen[dep] {
				packages = append(packages, dep)
				seen[dep] = true
			}
		}
	}
	sort.Strings(packages)
	return packages
}

func exists(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0
}

var dotnetBasePins = []struct {
	Name    string
	Version string
}{
	{Name: "OpenTelemetry.Extensions.Hosting", Version: "1.11.2"},
	{Name: "OpenTelemetry.Instrumentation.AspNetCore", Version: "1.11.1"},
	{Name: "OpenTelemetry.Instrumentation.Http", Version: "1.11.1"},
	{Name: "OpenTelemetry.Exporter.OpenTelemetryProtocol", Version: "1.11.2"},
}

var dotnetEFPin = struct {
	Name    string
	Version string
}{
	Name:    "OpenTelemetry.Instrumentation.EntityFrameworkCore",
	Version: "1.10.0-beta.1",
}

func isDotnetProject(root string) bool {
	found := false
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			if scanner.IsExcludedDir(info.Name()) || info.Name() == "bin" || info.Name() == "obj" {
				return filepath.SkipDir
			}
			return nil
		}
		name := strings.ToLower(info.Name())
		if strings.HasSuffix(name, ".csproj") || strings.HasSuffix(name, ".sln") || strings.HasSuffix(name, ".slnx") || strings.HasSuffix(name, ".slnf") {
			found = true
			return filepath.SkipAll
		}
		return nil
	})
	return found
}

type dotnetCandidate struct {
	path  string
	score int
}

func selectDotnetEntrypoint(root string, opts Options) (string, error) {
	var candidates []string
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			if scanner.IsExcludedDir(info.Name()) || info.Name() == "bin" || info.Name() == "obj" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.EqualFold(info.Name(), "program.cs") {
			rel, err := filepath.Rel(root, path)
			if err == nil {
				candidates = append(candidates, filepath.ToSlash(rel))
			}
		}
		return nil
	})

	if opts.Entrypoint != "" {
		cleanEp := filepath.ToSlash(filepath.Clean(opts.Entrypoint))
		for _, c := range candidates {
			if c == cleanEp {
				return c, nil
			}
		}
		if exists(filepath.Join(root, filepath.FromSlash(cleanEp))) {
			return cleanEp, nil
		}
		return "", fmt.Errorf("invalid entrypoint %q", opts.Entrypoint)
	}

	if len(candidates) == 0 {
		return "", nil
	}
	if len(candidates) == 1 {
		return candidates[0], nil
	}

	var scored []dotnetCandidate
	for _, c := range candidates {
		score := 0
		lower := strings.ToLower(c)
		if strings.Contains(lower, "test") || strings.Contains(lower, "mock") {
			score -= 30
		}
		if strings.Contains(lower, "/src/") || strings.HasPrefix(lower, "src/") {
			score += 5
		}
		if strings.Contains(lower, "api") || strings.Contains(lower, "web") || strings.Contains(lower, "server") || strings.Contains(lower, "presentation") {
			score += 10
		}
		dir := filepath.Dir(filepath.Join(root, filepath.FromSlash(c)))
		entries, err := os.ReadDir(dir)
		if err == nil {
			for _, entry := range entries {
				if !entry.IsDir() && strings.HasSuffix(strings.ToLower(entry.Name()), ".csproj") {
					cspData, err := os.ReadFile(filepath.Join(dir, entry.Name()))
					if err == nil && strings.Contains(string(cspData), "Microsoft.NET.Sdk.Web") {
						score += 25
					}
				}
			}
		}
		scored = append(scored, dotnetCandidate{path: c, score: score})
	}

	sort.Slice(scored, func(i, j int) bool {
		return scored[i].score > scored[j].score
	})

	if len(scored) > 1 && scored[0].score == scored[1].score {
		return "", fmt.Errorf("ambiguous entrypoints: %s", strings.Join(candidates, ", "))
	}
	return scored[0].path, nil
}

func findDotnetCsproj(root, ep string) (string, error) {
	fullEp := filepath.Join(root, filepath.FromSlash(ep))
	currDir := filepath.Dir(fullEp)
	absRoot, _ := filepath.Abs(root)

	for {
		entries, err := os.ReadDir(currDir)
		if err == nil {
			var found []string
			for _, e := range entries {
				if !e.IsDir() && strings.HasSuffix(strings.ToLower(e.Name()), ".csproj") {
					found = append(found, filepath.Join(currDir, e.Name()))
				}
			}
			if len(found) == 1 {
				rel, err := filepath.Rel(root, found[0])
				if err == nil {
					return filepath.ToSlash(rel), nil
				}
			}
			if len(found) > 1 {
				for _, f := range found {
					data, err := os.ReadFile(f)
					if err == nil && strings.Contains(string(data), "Microsoft.NET.Sdk.Web") {
						rel, err := filepath.Rel(root, f)
						if err == nil {
							return filepath.ToSlash(rel), nil
						}
					}
				}
				rel, err := filepath.Rel(root, found[0])
				if err == nil {
					return filepath.ToSlash(rel), nil
				}
			}
		}

		absCurr, _ := filepath.Abs(currDir)
		if absCurr == absRoot || len(absCurr) <= len(absRoot) {
			break
		}
		parent := filepath.Dir(currDir)
		if parent == currDir {
			break
		}
		currDir = parent
	}

	var allCsproj []string
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			if scanner.IsExcludedDir(info.Name()) || info.Name() == "bin" || info.Name() == "obj" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(strings.ToLower(info.Name()), ".csproj") {
			allCsproj = append(allCsproj, path)
		}
		return nil
	})

	for _, c := range allCsproj {
		data, err := os.ReadFile(c)
		if err == nil && strings.Contains(string(data), "Microsoft.NET.Sdk.Web") {
			rel, err := filepath.Rel(root, c)
			if err == nil {
				return filepath.ToSlash(rel), nil
			}
		}
	}

	if len(allCsproj) > 0 {
		rel, err := filepath.Rel(root, allCsproj[0])
		if err == nil {
			return filepath.ToSlash(rel), nil
		}
	}

	return "", fmt.Errorf("no .csproj found for entrypoint %s", ep)
}

func detectDotnetEFCore(root string) bool {
	found := false
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			if scanner.IsExcludedDir(info.Name()) || info.Name() == "bin" || info.Name() == "obj" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(strings.ToLower(info.Name()), ".csproj") {
			data, err := os.ReadFile(path)
			if err == nil && strings.Contains(strings.ToLower(string(data)), "microsoft.entityframeworkcore") {
				found = true
				return filepath.SkipAll
			}
		}
		return nil
	})
	return found
}

func findDirectoryPackagesProps(root, csprojRel string) string {
	fullCsproj := filepath.Join(root, filepath.FromSlash(csprojRel))
	curr := filepath.Dir(fullCsproj)
	absRoot, _ := filepath.Abs(root)
	for {
		candidate := filepath.Join(curr, "Directory.Packages.props")
		if exists(candidate) {
			rel, err := filepath.Rel(root, candidate)
			if err == nil {
				return filepath.ToSlash(rel)
			}
		}
		absCurr, _ := filepath.Abs(curr)
		if absCurr == absRoot || len(absCurr) <= len(absRoot) {
			break
		}
		parent := filepath.Dir(curr)
		if parent == curr {
			break
		}
		curr = parent
	}
	if exists(filepath.Join(root, "Directory.Packages.props")) {
		return "Directory.Packages.props"
	}
	return ""
}

func injectDotnetDirectoryPackagesProps(data []byte, hasEFCore bool) ([]byte, error) {
	content := string(data)
	lower := strings.ToLower(content)

	var missing []struct {
		Name    string
		Version string
	}

	for _, pin := range dotnetBasePins {
		target := strings.ToLower(pin.Name)
		if !strings.Contains(lower, `include="`+target+`"`) && !strings.Contains(lower, `include='`+target+`'`) {
			missing = append(missing, pin)
		}
	}
	if hasEFCore {
		target := strings.ToLower(dotnetEFPin.Name)
		if !strings.Contains(lower, `include="`+target+`"`) && !strings.Contains(lower, `include='`+target+`'`) {
			missing = append(missing, dotnetEFPin)
		}
	}
	if len(missing) == 0 {
		return data, nil
	}

	var b strings.Builder
	newline := "\n"
	if strings.Contains(content, "\r\n") {
		newline = "\r\n"
	}
	b.WriteString("  <ItemGroup>" + newline)
	for _, item := range missing {
		b.WriteString(fmt.Sprintf("    <PackageVersion Include=\"%s\" Version=\"%s\" />%s", item.Name, item.Version, newline))
	}
	b.WriteString("  </ItemGroup>" + newline)

	projCloseIdx := strings.LastIndex(lower, "</project>")
	if projCloseIdx == -1 {
		return nil, errors.New("invalid Directory.Packages.props: missing </Project>")
	}

	res := content[:projCloseIdx] + b.String() + content[projCloseIdx:]
	return []byte(res), nil
}

func injectDotnetCsproj(data []byte, hasEFCore bool, isCPM bool) ([]byte, error) {
	content := string(data)
	lower := strings.ToLower(content)

	var missing []struct {
		Name    string
		Version string
	}

	for _, pin := range dotnetBasePins {
		target := strings.ToLower(pin.Name)
		if !strings.Contains(lower, `include="`+target+`"`) && !strings.Contains(lower, `include='`+target+`'`) {
			missing = append(missing, pin)
		}
	}
	if hasEFCore {
		target := strings.ToLower(dotnetEFPin.Name)
		if !strings.Contains(lower, `include="`+target+`"`) && !strings.Contains(lower, `include='`+target+`'`) {
			missing = append(missing, dotnetEFPin)
		}
	}
	if len(missing) == 0 {
		return data, nil
	}

	var b strings.Builder
	newline := "\n"
	if strings.Contains(content, "\r\n") {
		newline = "\r\n"
	}
	b.WriteString("  <ItemGroup>" + newline)
	for _, item := range missing {
		if isCPM {
			b.WriteString(fmt.Sprintf("    <PackageReference Include=\"%s\" />%s", item.Name, newline))
		} else {
			b.WriteString(fmt.Sprintf("    <PackageReference Include=\"%s\" Version=\"%s\" />%s", item.Name, item.Version, newline))
		}
	}
	b.WriteString("  </ItemGroup>" + newline)

	projCloseIdx := strings.LastIndex(lower, "</project>")
	if projCloseIdx == -1 {
		return nil, errors.New("invalid .csproj: missing </Project>")
	}

	res := content[:projCloseIdx] + b.String() + content[projCloseIdx:]
	return []byte(res), nil
}

func extractBuilderVar(lines []string, targetIdx int) string {
	for i := targetIdx; i >= 0 && i >= targetIdx-2; i-- {
		line := strings.TrimSpace(lines[i])
		if eqIdx := strings.Index(line, "="); eqIdx > 0 {
			left := strings.TrimSpace(line[:eqIdx])
			parts := strings.Fields(left)
			if len(parts) > 0 {
				name := parts[len(parts)-1]
				if name != "" && name != "var" {
					return name
				}
			}
		}
	}
	return "builder"
}

func canInstrumentProgramCs(data []byte) bool {
	content := string(data)
	if strings.Contains(content, "AddExtentObservability") {
		return true
	}
	keywords := []string{
		"CreateBuilder(",
		"CreateSlimBuilder(",
		"CreateDefaultBuilder(",
		"CreateApplicationBuilder(",
		"CreateEmptyBuilder(",
	}
	for _, kw := range keywords {
		if strings.Contains(content, kw) {
			return true
		}
	}
	return strings.Contains(content, ".Services.") || strings.Contains(content, ".Build()")
}

func injectDotnetStartupCs(data []byte) ([]byte, error) {
	content := string(data)
	if strings.Contains(content, "AddExtentObservability") {
		return data, nil
	}

	isCRLF := strings.Contains(content, "\r\n")
	newline := "\n"
	if isCRLF {
		newline = "\r\n"
	}
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")

	insertIdx := -1
	servicesVar := "services"

	for i, line := range lines {
		if strings.Contains(line, "ConfigureServices(") || strings.Contains(line, "ConfigureServices (") {
			lower := strings.ToLower(line)
			if idx := strings.Index(lower, "iservicecollection"); idx != -1 {
				rest := strings.TrimSpace(line[idx+len("iservicecollection"):])
				if endIdx := strings.IndexAny(rest, ",)"); endIdx != -1 {
					varName := strings.TrimSpace(rest[:endIdx])
					if varName != "" {
						servicesVar = varName
					}
				}
			}
			for j := i; j < len(lines); j++ {
				if strings.Contains(lines[j], "{") {
					insertIdx = j + 1
					break
				}
			}
			break
		}
	}

	if insertIdx == -1 {
		return nil, errors.New("unable to locate ConfigureServices in Startup.cs")
	}

	refLine := ""
	if insertIdx < len(lines) && strings.TrimSpace(lines[insertIdx]) != "" {
		refLine = lines[insertIdx]
	} else if insertIdx > 0 {
		refLine = lines[insertIdx-1]
	}
	indent := ""
	for _, r := range refLine {
		if r == ' ' || r == '\t' {
			indent += string(r)
		} else {
			break
		}
	}
	if indent == "" || (insertIdx > 0 && refLine == lines[insertIdx-1]) {
		indent += "    "
	}

	configExpr := "Configuration"
	if strings.Contains(content, "_configuration") && !strings.Contains(content, "Configuration") {
		configExpr = "_configuration"
	}

	injection := indent + servicesVar + ".AddExtentObservability(" + configExpr + ");"
	newLines := make([]string, 0, len(lines)+1)
	newLines = append(newLines, lines[:insertIdx]...)
	newLines = append(newLines, injection)
	newLines = append(newLines, lines[insertIdx:]...)

	return []byte(strings.Join(newLines, newline)), nil
}

func injectDotnetProgramCs(data []byte) ([]byte, error) {
	content := string(data)
	if strings.Contains(content, "AddExtentObservability") {
		return data, nil
	}

	isCRLF := strings.Contains(content, "\r\n")
	newline := "\n"
	if isCRLF {
		newline = "\r\n"
	}
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")

	builderKeywords := []string{
		"CreateBuilder(",
		"CreateSlimBuilder(",
		"CreateDefaultBuilder(",
		"CreateApplicationBuilder(",
		"CreateEmptyBuilder(",
	}

	for i, line := range lines {
		hasKw := false
		for _, kw := range builderKeywords {
			if strings.Contains(line, kw) {
				hasKw = true
				break
			}
		}
		if !hasKw {
			continue
		}

		if strings.Contains(line, ".Build()") {
			buildIdx := strings.Index(line, ".Build()")
			indent := ""
			for _, r := range line {
				if r == ' ' || r == '\t' {
					indent += string(r)
				} else {
					break
				}
			}
			afterBuild := line[buildIdx+len(".Build()"):]

			eqIdx := strings.Index(line[:buildIdx], "=")
			if eqIdx != -1 {
				left := strings.TrimSpace(line[:eqIdx])
				right := strings.TrimSpace(line[eqIdx+1 : buildIdx])
				builderVar := "builder"
				if strings.Contains(left, "builder") {
					builderVar = "builderInst"
				}
				newLines := make([]string, 0, len(lines)+2)
				newLines = append(newLines, lines[:i]...)
				newLines = append(newLines, indent+"var "+builderVar+" = "+right+";")
				newLines = append(newLines, indent+builderVar+".Services.AddExtentObservability("+builderVar+".Configuration);")
				newLines = append(newLines, indent+left+" = "+builderVar+".Build()"+afterBuild)
				newLines = append(newLines, lines[i+1:]...)
				return []byte(strings.Join(newLines, newline)), nil
			}

			right := strings.TrimSpace(line[:buildIdx])
			builderVar := "builder"
			newLines := make([]string, 0, len(lines)+2)
			newLines = append(newLines, lines[:i]...)
			newLines = append(newLines, indent+"var "+builderVar+" = "+right+";")
			newLines = append(newLines, indent+builderVar+".Services.AddExtentObservability("+builderVar+".Configuration);")
			newLines = append(newLines, indent+builderVar+".Build()"+afterBuild)
			newLines = append(newLines, lines[i+1:]...)
			return []byte(strings.Join(newLines, newline)), nil
		}

		insertIdx := i + 1
		builderVar := extractBuilderVar(lines, i)
		refLine := line
		if insertIdx < len(lines) && strings.TrimSpace(lines[insertIdx]) != "" {
			refLine = lines[insertIdx]
		}
		indent := ""
		for _, r := range refLine {
			if r == ' ' || r == '\t' {
				indent += string(r)
			} else {
				break
			}
		}

		injection := indent + builderVar + ".Services.AddExtentObservability(" + builderVar + ".Configuration);"
		newLines := make([]string, 0, len(lines)+1)
		newLines = append(newLines, lines[:insertIdx]...)
		newLines = append(newLines, injection)
		newLines = append(newLines, lines[insertIdx:]...)
		return []byte(strings.Join(newLines, newline)), nil
	}

	insertIdx := -1
	builderVar := "builder"

	for i, line := range lines {
		if strings.Contains(line, ".Build()") {
			insertIdx = i
			builderVar = extractBuilderVar(lines, i)
			break
		}
	}

	if insertIdx == -1 {
		for i, line := range lines {
			if strings.Contains(line, ".Services.") {
				insertIdx = i
				builderVar = extractBuilderVar(lines, i)
				break
			}
		}
	}

	if insertIdx == -1 {
		return nil, errors.New("unable to locate builder initialization in Program.cs")
	}

	refLine := ""
	if insertIdx > 0 && insertIdx <= len(lines) {
		refLine = lines[insertIdx-1]
	} else if insertIdx < len(lines) {
		refLine = lines[insertIdx]
	}
	indent := ""
	for _, r := range refLine {
		if r == ' ' || r == '\t' {
			indent += string(r)
		} else {
			break
		}
	}

	injection := indent + builderVar + ".Services.AddExtentObservability(" + builderVar + ".Configuration);"
	newLines := make([]string, 0, len(lines)+1)
	newLines = append(newLines, lines[:insertIdx]...)
	newLines = append(newLines, injection)
	newLines = append(newLines, lines[insertIdx:]...)

	return []byte(strings.Join(newLines, newline)), nil
}

func dotnetBootstrap(hasEFCore bool) string {
	efInstrumentation := ""
	if hasEFCore {
		efInstrumentation = "                    .AddEntityFrameworkCoreInstrumentation()\n"
	}
	return fmt.Sprintf(dotnetBootstrapTemplate, efInstrumentation)
}

const dotnetBootstrapTemplate = `using System;
using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.DependencyInjection;
using OpenTelemetry.Metrics;
using OpenTelemetry.Resources;
using OpenTelemetry.Trace;

namespace Microsoft.Extensions.DependencyInjection
{
    public static class ExtentObservabilityExtensions
    {
        public static IServiceCollection AddExtentObservability(this IServiceCollection services, IConfiguration configuration)
        {
            var serviceName = Environment.GetEnvironmentVariable("OTEL_SERVICE_NAME")
                ?? configuration["OTEL_SERVICE_NAME"]
                ?? "dotnet-service";
            var serviceNamespace = Environment.GetEnvironmentVariable("OTEL_SERVICE_NAMESPACE")
                ?? configuration["OTEL_SERVICE_NAMESPACE"]
                ?? "default";
            string otlpEndpoint = Environment.GetEnvironmentVariable("OTEL_EXPORTER_OTLP_ENDPOINT")
                ?? configuration["OTEL_EXPORTER_OTLP_ENDPOINT"]
                ?? "http://localhost:4318";

            services.AddOpenTelemetry()
                .ConfigureResource(resource => resource
                    .AddService(serviceName: serviceName, serviceNamespace: serviceNamespace))
                .WithTracing(tracing =>
                {
                    tracing
                        .AddAspNetCoreInstrumentation(opts =>
                        {
                            opts.RecordException = true;
                        })
                        .AddHttpClientInstrumentation()
%s                        .AddOtlpExporter(opts =>
                        {
                            opts.Endpoint = new Uri(otlpEndpoint);
                            if (otlpEndpoint.Contains(":4318"))
                            {
                                opts.Protocol = OpenTelemetry.Exporter.OtlpExportProtocol.HttpProtobuf;
                            }
                        });
                })
                .WithMetrics(metrics =>
                {
                    metrics
                        .AddAspNetCoreInstrumentation()
                        .AddHttpClientInstrumentation()
                        .AddOtlpExporter(opts =>
                        {
                            opts.Endpoint = new Uri(otlpEndpoint);
                            if (otlpEndpoint.Contains(":4318"))
                            {
                                opts.Protocol = OpenTelemetry.Exporter.OtlpExportProtocol.HttpProtobuf;
                            }
                        });
                });

            return services;
        }
    }
}
`

func dotnetPlan(root string, opts Options) ([]fileops.Step, error) {
	ep, err := selectDotnetEntrypoint(root, opts)
	if err != nil {
		return nil, err
	}
	if ep == "" {
		return nil, nil
	}
	csprojRel, err := findDotnetCsproj(root, ep)
	if err != nil {
		return nil, err
	}
	hasEFCore := detectDotnetEFCore(root)
	steps := []fileops.Step{}

	dppRel := findDirectoryPackagesProps(root, csprojRel)
	if dppRel != "" {
		dppPath := filepath.Join(root, filepath.FromSlash(dppRel))
		dppData, err := os.ReadFile(dppPath)
		if err != nil {
			return nil, err
		}
		updatedDpp, err := injectDotnetDirectoryPackagesProps(dppData, hasEFCore)
		if err != nil {
			return nil, err
		}
		if !bytes.Equal(dppData, updatedDpp) {
			steps = append(steps, fileops.Step{
				Path:   dppRel,
				Action: fileops.Update,
				Data:   updatedDpp,
				Mode:   0644,
			})
		}
	}

	csprojPath := filepath.Join(root, filepath.FromSlash(csprojRel))
	csprojData, err := os.ReadFile(csprojPath)
	if err != nil {
		return nil, err
	}
	updatedCsproj, err := injectDotnetCsproj(csprojData, hasEFCore, dppRel != "")
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(csprojData, updatedCsproj) {
		steps = append(steps, fileops.Step{
			Path:   csprojRel,
			Action: fileops.Update,
			Data:   updatedCsproj,
			Mode:   0644,
		})
	}

	extRel := filepath.ToSlash(filepath.Join(filepath.Dir(ep), "ExtentObservabilityExtensions.cs"))
	extPath := filepath.Join(root, filepath.FromSlash(extRel))
	extContent := dotnetBootstrap(hasEFCore)
	oldExt, err := os.ReadFile(extPath)
	if err != nil || string(oldExt) != extContent {
		action := fileops.Create
		if err == nil {
			action = fileops.Update
		}
		steps = append(steps, fileops.Step{
			Path:   extRel,
			Action: action,
			Data:   []byte(extContent),
			Mode:   0644,
		})
	}

	epPath := filepath.Join(root, filepath.FromSlash(ep))
	epData, err := os.ReadFile(epPath)
	if err != nil {
		return nil, err
	}

	startupRel := filepath.ToSlash(filepath.Join(filepath.Dir(ep), "Startup.cs"))
	startupPath := filepath.Join(root, filepath.FromSlash(startupRel))
	hasStartup := exists(startupPath)

	if hasStartup && (strings.Contains(string(epData), "UseStartup") || !canInstrumentProgramCs(epData)) {
		startupData, err := os.ReadFile(startupPath)
		if err != nil {
			return nil, err
		}
		updatedStartup, err := injectDotnetStartupCs(startupData)
		if err != nil {
			return nil, err
		}
		if !bytes.Equal(startupData, updatedStartup) {
			steps = append(steps, fileops.Step{
				Path:   startupRel,
				Action: fileops.Update,
				Data:   updatedStartup,
				Mode:   0644,
			})
		}
	} else {
		updatedEp, err := injectDotnetProgramCs(epData)
		if err != nil {
			if hasStartup {
				startupData, readErr := os.ReadFile(startupPath)
				if readErr == nil {
					updatedStartup, sErr := injectDotnetStartupCs(startupData)
					if sErr == nil {
						if !bytes.Equal(startupData, updatedStartup) {
							steps = append(steps, fileops.Step{
								Path:   startupRel,
								Action: fileops.Update,
								Data:   updatedStartup,
								Mode:   0644,
							})
						}
						return steps, nil
					}
				}
			}
			return nil, err
		}
		if !bytes.Equal(epData, updatedEp) {
			steps = append(steps, fileops.Step{
				Path:   ep,
				Action: fileops.Update,
				Data:   updatedEp,
				Mode:   0644,
			})
		}
	}

	return steps, nil
}

const nodeCJSBootstrap = `// Generated by Extent. Keep this file imported before application code.
const { NodeSDK } = require("@opentelemetry/sdk-node");
const { BatchLogRecordProcessor } = require("@opentelemetry/sdk-logs");
const { logs } = require("@opentelemetry/api-logs");
const { PeriodicExportingMetricReader } = require("@opentelemetry/sdk-metrics");
const { resourceFromAttributes } = require("@opentelemetry/resources");
const { getNodeAutoInstrumentations } = require("@opentelemetry/auto-instrumentations-node");
const { OTLPTraceExporter } = require("@opentelemetry/exporter-trace-otlp-http");
const { OTLPMetricExporter } = require("@opentelemetry/exporter-metrics-otlp-http");
const { OTLPLogExporter } = require("@opentelemetry/exporter-logs-otlp-http");

const resource = resourceFromAttributes({
  "service.name": process.env.OTEL_SERVICE_NAME || "service",
  "service.namespace": process.env.OTEL_SERVICE_NAMESPACE || "default",
  "deployment.environment.name": process.env.OTEL_DEPLOYMENT_ENVIRONMENT || "development",
});
const configuredMetricInterval = Number(process.env.OTEL_METRIC_EXPORT_INTERVAL || 5000);
const metricExportIntervalMillis = Number.isFinite(configuredMetricInterval) && configuredMetricInterval >= 1000
  ? configuredMetricInterval
  : 5000;

const sdk = new NodeSDK({
  resource,
  traceExporter: new OTLPTraceExporter(),
  metricReaders: [new PeriodicExportingMetricReader({ exporter: new OTLPMetricExporter(), exportIntervalMillis: metricExportIntervalMillis })],
  logRecordProcessors: [new BatchLogRecordProcessor({ exporter: new OTLPLogExporter() })],
  instrumentations: [getNodeAutoInstrumentations({
    "@opentelemetry/instrumentation-pg": {
      enhancedDatabaseReporting: true,
      requireParentSpan: true,
    },
    "@opentelemetry/instrumentation-mysql": { enhancedDatabaseReporting: true },
    "@opentelemetry/instrumentation-mongodb": { enhancedDatabaseReporting: true },
    "@opentelemetry/instrumentation-redis": { requireParentSpan: true },
    "@opentelemetry/instrumentation-http": {
      requestHook: (_span, request) => logs.getLogger("extent.http").emit({
        body: "HTTP request",
        attributes: { "http.request.header.x_request_id": request.headers?.["x-request-id"] || "" },
      }),
      headersToSpanAttributes: {
        client: { requestHeaders: ["x-request-id"], responseHeaders: ["x-request-id"] },
        server: { requestHeaders: ["x-request-id"], responseHeaders: ["x-request-id"] },
      },
    },
  })],
});

let stopping = false;
async function shutdown(signal) {
  if (stopping) return;
  stopping = true;
  try {
    await sdk.shutdown();
  } catch (error) {
    console.error("extent: OpenTelemetry shutdown failed", error);
    process.exitCode = 1;
  }
  if (signal) process.exit();
}

process.once("SIGTERM", () => void shutdown("SIGTERM"));
process.once("SIGINT", () => void shutdown("SIGINT"));
sdk.start();

module.exports = { sdk, shutdown };
`

const nodeESMBootstrap = `// Generated by Extent. Keep this file imported before application code.
import { NodeSDK } from "@opentelemetry/sdk-node";
import { BatchLogRecordProcessor } from "@opentelemetry/sdk-logs";
import { logs } from "@opentelemetry/api-logs";
import { PeriodicExportingMetricReader } from "@opentelemetry/sdk-metrics";
import { resourceFromAttributes } from "@opentelemetry/resources";
import { getNodeAutoInstrumentations } from "@opentelemetry/auto-instrumentations-node";
import { OTLPTraceExporter } from "@opentelemetry/exporter-trace-otlp-http";
import { OTLPMetricExporter } from "@opentelemetry/exporter-metrics-otlp-http";
import { OTLPLogExporter } from "@opentelemetry/exporter-logs-otlp-http";

const resource = resourceFromAttributes({
  "service.name": process.env.OTEL_SERVICE_NAME || "service",
  "service.namespace": process.env.OTEL_SERVICE_NAMESPACE || "default",
  "deployment.environment.name": process.env.OTEL_DEPLOYMENT_ENVIRONMENT || "development",
});
const configuredMetricInterval = Number(process.env.OTEL_METRIC_EXPORT_INTERVAL || 5000);
const metricExportIntervalMillis = Number.isFinite(configuredMetricInterval) && configuredMetricInterval >= 1000
  ? configuredMetricInterval
  : 5000;

const sdk = new NodeSDK({
  resource,
  traceExporter: new OTLPTraceExporter(),
  metricReaders: [new PeriodicExportingMetricReader({ exporter: new OTLPMetricExporter(), exportIntervalMillis: metricExportIntervalMillis })],
  logRecordProcessors: [new BatchLogRecordProcessor({ exporter: new OTLPLogExporter() })],
  instrumentations: [getNodeAutoInstrumentations({
    "@opentelemetry/instrumentation-pg": {
      enhancedDatabaseReporting: true,
      requireParentSpan: true,
    },
    "@opentelemetry/instrumentation-mysql": { enhancedDatabaseReporting: true },
    "@opentelemetry/instrumentation-mongodb": { enhancedDatabaseReporting: true },
    "@opentelemetry/instrumentation-redis": { requireParentSpan: true },
    "@opentelemetry/instrumentation-http": {
      requestHook: (_span, request) => logs.getLogger("extent.http").emit({
        body: "HTTP request",
        attributes: { "http.request.header.x_request_id": request.headers?.["x-request-id"] || "" },
      }),
      headersToSpanAttributes: {
        client: { requestHeaders: ["x-request-id"], responseHeaders: ["x-request-id"] },
        server: { requestHeaders: ["x-request-id"], responseHeaders: ["x-request-id"] },
      },
    },
  })],
});

let stopping = false;
async function shutdown(signal) {
  if (stopping) return;
  stopping = true;
  try {
    await sdk.shutdown();
  } catch (error) {
    console.error("extent: OpenTelemetry shutdown failed", error);
    process.exitCode = 1;
  }
  if (signal) process.exit();
}

process.once("SIGTERM", () => void shutdown("SIGTERM"));
process.once("SIGINT", () => void shutdown("SIGINT"));
sdk.start();

export { sdk, shutdown };
`

const goBootstrap = `// Package observability initializes OpenTelemetry for the service.
// Generated by Extent. Keep this package imported before application code.
package observability

import (
	"context"
	"log"
	"os"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

var provider *sdktrace.TracerProvider

func init() {
	serviceName := os.Getenv("OTEL_SERVICE_NAME")
	if serviceName == "" {
		serviceName = "go-service"
	}
	exporter, err := otlptracehttp.New(context.Background())
	if err != nil {
		log.Printf("extent: failed to create OTLP trace exporter: %v", err)
		return
	}
	provider = sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceName(serviceName),
		)),
	)
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
}

func Shutdown() {
	if provider == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := provider.Shutdown(ctx); err != nil {
		log.Printf("extent: failed to shut down OpenTelemetry: %v", err)
	}
}
`

const pythonBootstrap = `"""Generated by Extent. Import before application code."""

import atexit
import logging
import os

os.environ.setdefault("OTEL_INSTRUMENTATION_HTTP_CAPTURE_HEADERS_SERVER_REQUEST", "X-Request-ID")

from opentelemetry import trace
from opentelemetry._logs import set_logger_provider
from opentelemetry.instrumentation.logging import LoggingInstrumentor
from opentelemetry.instrumentation.requests import RequestsInstrumentor
from opentelemetry.metrics import set_meter_provider
from opentelemetry.exporter.otlp.proto.http.metric_exporter import OTLPMetricExporter
from opentelemetry.exporter.otlp.proto.http.trace_exporter import OTLPSpanExporter
from opentelemetry.exporter.otlp.proto.http._log_exporter import OTLPLogExporter
from opentelemetry.sdk._logs import LoggerProvider, LoggingHandler
from opentelemetry.sdk._logs.export import BatchLogRecordProcessor
from opentelemetry.sdk.metrics import MeterProvider
from opentelemetry.sdk.metrics.export import PeriodicExportingMetricReader
from opentelemetry.sdk.resources import Resource
from opentelemetry.sdk.trace import TracerProvider
from opentelemetry.sdk.trace.export import BatchSpanProcessor

try:
    from opentelemetry.instrumentation.fastapi import FastAPIInstrumentor
except Exception:
    FastAPIInstrumentor = None

try:
    from opentelemetry.instrumentation.flask import FlaskInstrumentor
except Exception:
    FlaskInstrumentor = None

try:
    from opentelemetry.instrumentation.sqlalchemy import SQLAlchemyInstrumentor
except Exception:
    SQLAlchemyInstrumentor = None

try:
    from opentelemetry.instrumentation.psycopg2 import Psycopg2Instrumentor
except Exception:
    Psycopg2Instrumentor = None

resource = Resource.create({
    "service.name": os.getenv("OTEL_SERVICE_NAME", "service"),
    "service.namespace": os.getenv("OTEL_SERVICE_NAMESPACE", "default"),
    "deployment.environment.name": os.getenv("OTEL_DEPLOYMENT_ENVIRONMENT", "development"),
})
trace_provider = TracerProvider(resource=resource)
trace_provider.add_span_processor(BatchSpanProcessor(OTLPSpanExporter()))
trace.set_tracer_provider(trace_provider)

try:
    metric_export_interval = max(1000, int(os.getenv("OTEL_METRIC_EXPORT_INTERVAL", "5000")))
except ValueError:
    metric_export_interval = 5000
metric_reader = PeriodicExportingMetricReader(OTLPMetricExporter(), export_interval_millis=metric_export_interval)
meter_provider = MeterProvider(resource=resource, metric_readers=[metric_reader])
set_meter_provider(meter_provider)

logger_provider = LoggerProvider(resource=resource)
logger_provider.add_log_record_processor(BatchLogRecordProcessor(OTLPLogExporter()))
set_logger_provider(logger_provider)
LoggingInstrumentor().instrument(set_logging_format=True)
logging.getLogger().addHandler(LoggingHandler(level=logging.NOTSET, logger_provider=logger_provider))
RequestsInstrumentor().instrument()
if FastAPIInstrumentor is not None:
    FastAPIInstrumentor().instrument()
if FlaskInstrumentor is not None:
    FlaskInstrumentor().instrument()
if SQLAlchemyInstrumentor is not None:
    SQLAlchemyInstrumentor().instrument(enable_commenter=True, commenter_options={})
if Psycopg2Instrumentor is not None:
    Psycopg2Instrumentor().instrument(enable_commenter=True)

atexit.register(logger_provider.shutdown)
atexit.register(meter_provider.shutdown)
atexit.register(trace_provider.shutdown)
`
