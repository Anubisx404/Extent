package instrumenter

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Anubisx404/Extent/internal/codemods"
	"github.com/Anubisx404/Extent/internal/fileops"
	"github.com/Anubisx404/Extent/internal/scanner"
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

func Instrument(root string, opts Options) (Result, error) {
	return instrument(root, opts, fileops.Options{})
}

// instrument is Instrument with transaction options. Tests use applyOptions to
// inject write failures; production code always passes the zero value.
func instrument(root string, opts Options, applyOptions fileops.Options) (Result, error) {
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
	manifest, err := fileops.ApplyWithOptions(plan, applyOptions)
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
