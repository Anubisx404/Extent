package analyzer

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/Anubisx404/Extent/internal/process"
	"github.com/Anubisx404/Extent/internal/scanner"
	"gopkg.in/yaml.v3"
)

type Options struct {
	Dynamic bool `json:"dynamic"`
}

type Result struct {
	Root                string            `json:"root"`
	ServiceName         string            `json:"serviceName"`
	Runtime             []string          `json:"runtime"`
	Frameworks          []string          `json:"frameworks"`
	Entrypoints         []string          `json:"entrypoints"`
	Routes              []Route           `json:"routes"`
	DatabaseLibraries   []string          `json:"databaseLibraries"`
	Queues              []string          `json:"queues"`
	Loggers             []string          `json:"loggers"`
	ExternalHTTPClients []string          `json:"externalHttpClients"`
	Docker              DockerState       `json:"docker"`
	TestCommands        []string          `json:"testCommands"`
	Recommendations     []string          `json:"recommendations"`
	Warnings            []scanner.Warning `json:"warnings,omitempty"`
}

type Route struct {
	Method string `json:"method"`
	Path   string `json:"path"`
	File   string `json:"file"`
}

type DockerState struct {
	ComposeFiles []string `json:"composeFiles"`
	Ports        []string `json:"ports"`
	EnvFiles     []string `json:"envFiles"`
	Networks     []string `json:"networks"`
}

func Analyze(root string) (Result, error) {
	return AnalyzeWithOptions(root, Options{})
}

func AnalyzeWithOptions(root string, opts Options) (Result, error) {
	scan, err := scanner.Scan(root)
	if err != nil {
		return Result{}, err
	}
	result := Result{
		Root:              scan.Root,
		ServiceName:       detectServiceName(scan.Root),
		Runtime:           scan.Runtimes,
		Frameworks:        scan.Frameworks,
		Entrypoints:       scan.Entrypoints,
		DatabaseLibraries: scan.DatabaseLibraries,
		Warnings:          append([]scanner.Warning(nil), scan.Warnings...),
		Docker: DockerState{
			ComposeFiles: scan.ComposeFiles,
		},
	}

	depSignals, testCommands := analyzePackageJSON(scan.Root)
	result.Loggers = append(result.Loggers, depSignals.Loggers...)
	result.Queues = append(result.Queues, depSignals.Queues...)
	result.ExternalHTTPClients = append(result.ExternalHTTPClients, depSignals.ExternalHTTPClients...)
	result.TestCommands = append(result.TestCommands, testCommands...)

	pythonSignals := analyzePythonRequirements(scan.Root)
	result.Loggers = append(result.Loggers, pythonSignals.Loggers...)
	result.Queues = append(result.Queues, pythonSignals.Queues...)
	result.ExternalHTTPClients = append(result.ExternalHTTPClients, pythonSignals.ExternalHTTPClients...)
	if exists(filepath.Join(scan.Root, "requirements.txt")) {
		result.TestCommands = append(result.TestCommands, "pytest")
	}
	goSignals := analyzeGoMod(scan.Root)
	result.Loggers = append(result.Loggers, goSignals.Loggers...)
	result.Queues = append(result.Queues, goSignals.Queues...)
	result.ExternalHTTPClients = append(result.ExternalHTTPClients, goSignals.ExternalHTTPClients...)
	if exists(filepath.Join(scan.Root, "go.mod")) {
		result.TestCommands = append(result.TestCommands, "go test ./...")
	}

	files := map[string]string{}
	walkErr := filepath.WalkDir(scan.Root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && scanner.IsExcludedDir(d.Name()) && path != scan.Root {
			return filepath.SkipDir
		}
		if d.IsDir() {
			return nil
		}
		rel := relPath(scan.Root, path)
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			result.Warnings = append(result.Warnings, scanner.Warning{Kind: "source", Path: rel, Detail: readErr.Error()})
			return nil
		}
		text := string(data)
		files[rel] = text
		if isComposeFile(rel) {
			ports, envFiles, networks := parseCompose(text)
			result.Docker.Ports = append(result.Docker.Ports, ports...)
			result.Docker.EnvFiles = append(result.Docker.EnvFiles, envFiles...)
			result.Docker.Networks = append(result.Docker.Networks, networks...)
		}
		return nil
	})
	if walkErr != nil {
		return Result{}, walkErr
	}

	mountPrefixes := detectMountPrefixes(files)
	for rel, text := range files {
		prefix := mountPrefixes[rel]
		result.Routes = append(result.Routes, detectRoutes(rel, text, prefix)...)
		result.Loggers = append(result.Loggers, detectSourceLoggers(rel, text)...)
		result.ExternalHTTPClients = append(result.ExternalHTTPClients, detectSourceHTTPClients(rel, text)...)
	}

	if opts.Dynamic {
		inspectDynamicRoutes(scan.Root, &result)
	}

	result.Routes = uniqueRoutes(result.Routes)
	result.Loggers = sortedUnique(result.Loggers)
	result.Queues = sortedUnique(result.Queues)
	result.ExternalHTTPClients = sortedUnique(result.ExternalHTTPClients)
	result.TestCommands = sortedUnique(result.TestCommands)
	result.Docker.Ports = sortedUnique(result.Docker.Ports)
	result.Docker.EnvFiles = sortedUnique(result.Docker.EnvFiles)
	result.Docker.Networks = sortedUnique(result.Docker.Networks)
	result.Recommendations = recommendations(result)
	return result, nil
}

type depSignals struct {
	Loggers             []string
	Queues              []string
	ExternalHTTPClients []string
}

func analyzePackageJSON(root string) (depSignals, []string) {
	path := filepath.Join(root, "package.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return depSignals{}, nil
	}
	var pkg struct {
		Scripts         map[string]string `json:"scripts"`
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
		Name            string            `json:"name"`
	}
	if json.Unmarshal(data, &pkg) != nil {
		return depSignals{}, nil
	}
	deps := map[string]bool{}
	for dep := range pkg.Dependencies {
		deps[dep] = true
	}
	for dep := range pkg.DevDependencies {
		deps[dep] = true
	}
	signals := depSignals{}
	for dep, logger := range map[string]string{"pino": "pino", "winston": "winston", "bunyan": "bunyan"} {
		if deps[dep] {
			signals.Loggers = append(signals.Loggers, logger)
		}
	}
	for dep, queue := range map[string]string{"bullmq": "bullmq", "bull": "bull", "amqplib": "rabbitmq", "kafkajs": "kafka"} {
		if deps[dep] {
			signals.Queues = append(signals.Queues, queue)
		}
	}
	for dep, client := range map[string]string{"axios": "axios", "got": "got", "node-fetch": "node-fetch", "undici": "undici"} {
		if deps[dep] {
			signals.ExternalHTTPClients = append(signals.ExternalHTTPClients, client)
		}
	}
	var commands []string
	if _, ok := pkg.Scripts["test"]; ok {
		commands = append(commands, "npm test")
	}
	return signals, commands
}

func analyzePythonRequirements(root string) depSignals {
	data, err := os.ReadFile(filepath.Join(root, "requirements.txt"))
	if err != nil {
		return depSignals{}
	}
	text := strings.ToLower(string(data))
	signals := depSignals{}
	for dep, logger := range map[string]string{"structlog": "structlog", "loguru": "loguru"} {
		if strings.Contains(text, dep) {
			signals.Loggers = append(signals.Loggers, logger)
		}
	}
	if strings.Contains(text, "celery") {
		signals.Queues = append(signals.Queues, "celery")
	}
	for dep, client := range map[string]string{"requests": "requests", "httpx": "httpx", "aiohttp": "aiohttp"} {
		if strings.Contains(text, dep) {
			signals.ExternalHTTPClients = append(signals.ExternalHTTPClients, client)
		}
	}
	return signals
}

func analyzeGoMod(root string) depSignals {
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return depSignals{}
	}
	text := strings.ToLower(string(data))
	signals := depSignals{}
	for dep, logger := range map[string]string{
		"go.uber.org/zap":            "zap",
		"github.com/rs/zerolog":      "zerolog",
		"github.com/sirupsen/logrus": "logrus",
	} {
		if strings.Contains(text, dep) {
			signals.Loggers = append(signals.Loggers, logger)
		}
	}
	for dep, queue := range map[string]string{
		"github.com/segmentio/kafka-go":              "kafka",
		"github.com/confluentinc/confluent-kafka-go": "kafka",
		"github.com/streadway/amqp":                  "rabbitmq",
		"github.com/rabbitmq/amqp091-go":             "rabbitmq",
		"github.com/hibiken/asynq":                   "asynq",
	} {
		if strings.Contains(text, dep) {
			signals.Queues = append(signals.Queues, queue)
		}
	}
	for dep, client := range map[string]string{
		"github.com/go-resty/resty":             "resty",
		"github.com/hashicorp/go-retryablehttp": "retryablehttp",
	} {
		if strings.Contains(text, dep) {
			signals.ExternalHTTPClients = append(signals.ExternalHTTPClients, client)
		}
	}
	return signals
}

func detectMountPrefixes(files map[string]string) map[string]string {
	prefixes := map[string]string{}
	requireRx := regexp.MustCompile(`(?m)(?:const|let|var)\s+(\w+)\s*=\s*require\(\s*["'](\.[^"']+)["']\)`)
	importRx := regexp.MustCompile(`(?m)import\s+(\w+)\s+from\s*["'](\.[^"']+)["']`)
	useVarRx := regexp.MustCompile(`(?m)\bapp\.use\(\s*["'](/[^'"]*)["']\s*,\s*(\w+)\)`)
	useReqRx := regexp.MustCompile(`(?m)\bapp\.use\(\s*["'](/[^'"]*)["']\s*,\s*require\(\s*["'](\.[^"']+)["']\)\)`)
	includeRouterRx := regexp.MustCompile(`(?m)\bapp\.include_router\(\s*(\w+)\s*,\s*prefix\s*=\s*["'](/[^'"]*)["']\)`)
	blueprintRx := regexp.MustCompile(`(?m)\bapp\.register_blueprint\(\s*(\w+)\s*,\s*url_prefix\s*=\s*["'](/[^'"]*)["']\)`)

	for callerFile, content := range files {
		callerDir := filepath.Dir(callerFile)
		varToPath := map[string]string{}
		for _, m := range requireRx.FindAllStringSubmatch(content, -1) {
			varToPath[m[1]] = m[2]
		}
		for _, m := range importRx.FindAllStringSubmatch(content, -1) {
			varToPath[m[1]] = m[2]
		}

		resolveTarget := func(imported string) string {
			clean := filepath.ToSlash(filepath.Clean(filepath.Join(callerDir, imported)))
			for _, ext := range []string{"", ".js", ".ts", ".py", "/index.js", "/index.ts"} {
				candidate := clean + ext
				if _, ok := files[candidate]; ok {
					return candidate
				}
			}
			return clean
		}

		for _, m := range useVarRx.FindAllStringSubmatch(content, -1) {
			prefix := m[1]
			varName := m[2]
			if target, ok := varToPath[varName]; ok {
				resolved := resolveTarget(target)
				prefixes[resolved] = prefix
			}
		}
		for _, m := range useReqRx.FindAllStringSubmatch(content, -1) {
			prefix := m[1]
			target := m[2]
			resolved := resolveTarget(target)
			prefixes[resolved] = prefix
		}
		for _, m := range includeRouterRx.FindAllStringSubmatch(content, -1) {
			varName := m[1]
			prefix := m[2]
			if target, ok := varToPath[varName]; ok {
				resolved := resolveTarget(target)
				prefixes[resolved] = prefix
			}
		}
		for _, m := range blueprintRx.FindAllStringSubmatch(content, -1) {
			varName := m[1]
			prefix := m[2]
			if target, ok := varToPath[varName]; ok {
				resolved := resolveTarget(target)
				prefixes[resolved] = prefix
			}
		}
	}
	return prefixes
}

func joinRoutePath(prefix, p string) string {
	prefix = strings.TrimSuffix(prefix, "/")
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	if p == "/" {
		if prefix == "" {
			return "/"
		}
		return prefix
	}
	return prefix + p
}

func detectRoutes(file, text, prefix string) []Route {
	var routes []Route
	patterns := []struct {
		rx     *regexp.Regexp
		method int
		path   int
	}{
		{regexp.MustCompile(`(?i)\b(?:app|router|r|api|v\d+|group|engine|server|subrouter)\.(get|post|put|patch|delete|head|options)\s*\(\s*["']([^"']+)["']`), 1, 2},
		{regexp.MustCompile(`(?i)@(?:app|router|bp|api)?\.?(get|post|put|patch|delete|head|options)\s*\(\s*["']([^"']+)["']`), 1, 2},
		{regexp.MustCompile(`(?i)@(get|post|put|patch|delete)\s*\(\s*["']([^"']+)["']`), 1, 2},
		{regexp.MustCompile(`(?i)\b(?:http\.HandleFunc|mux\.HandleFunc|r\.HandleFunc)\s*\(\s*["']([^"']+)["']`), 0, 1},
		{regexp.MustCompile(`(?i)@(?:\w+)\.route\s*\(\s*["']([^"']+)["']`), 0, 1},
		{regexp.MustCompile(`(?i)\b(?:path|re_path)\s*\(\s*["']([^"']+)["']`), 0, 1},
	}
	for _, pattern := range patterns {
		matches := pattern.rx.FindAllStringSubmatch(text, -1)
		for _, match := range matches {
			method := "ANY"
			if pattern.method > 0 {
				method = strings.ToUpper(match[pattern.method])
			}
			p := match[pattern.path]
			if prefix != "" {
				p = joinRoutePath(prefix, p)
			}
			routes = append(routes, Route{Method: method, Path: p, File: file})
		}
	}
	return routes
}

func detectSourceLoggers(file, text string) []string {
	if !strings.HasSuffix(file, ".go") && !strings.HasSuffix(file, ".js") && !strings.HasSuffix(file, ".ts") && !strings.HasSuffix(file, ".py") {
		return nil
	}
	lower := strings.ToLower(text)
	var loggers []string
	for needle, logger := range map[string]string{
		"slog.":          "slog",
		"zap.":           "zap",
		"zerolog.":       "zerolog",
		"logrus.":        "logrus",
		"pino(":          "pino",
		"pino.":          "pino",
		"winston.":       "winston",
		"bunyan.":        "bunyan",
		"structlog.":     "structlog",
		"loguru.":        "loguru",
		"logging.":       "python logging",
		"loggerfactory.": "slf4j",
		"log4j":          "log4j",
		"serilog.":       "serilog",
	} {
		if strings.Contains(lower, needle) {
			loggers = append(loggers, logger)
		}
	}
	return loggers
}

func detectSourceHTTPClients(file, text string) []string {
	if !strings.HasSuffix(file, ".go") && !strings.HasSuffix(file, ".js") && !strings.HasSuffix(file, ".ts") && !strings.HasSuffix(file, ".py") {
		return nil
	}
	lower := strings.ToLower(text)
	var clients []string
	for needle, client := range map[string]string{
		"http.get(":       "net/http",
		"http.post(":      "net/http",
		"http.newrequest": "net/http",
		"http.client":     "net/http",
		"axios.":          "axios",
		"axios(":          "axios",
		"got(":            "got",
		"got.":            "got",
		"undici.":         "undici",
		"node-fetch":      "node-fetch",
		"fetch(":          "fetch",
		"requests.":       "requests",
		"httpx.":          "httpx",
		"aiohttp.":        "aiohttp",
		"urllib.":         "urllib",
		"resty.":          "resty",
	} {
		if strings.Contains(lower, needle) {
			clients = append(clients, client)
		}
	}
	return clients
}

type composeDoc struct {
	Services map[string]struct {
		Ports    any `yaml:"ports"`
		EnvFile  any `yaml:"env_file"`
		Networks any `yaml:"networks"`
	} `yaml:"services"`
	Networks map[string]any `yaml:"networks"`
}

func parseCompose(text string) (ports, envFiles, networks []string) {
	var doc composeDoc
	if err := yaml.Unmarshal([]byte(text), &doc); err == nil && (len(doc.Services) > 0 || len(doc.Networks) > 0) {
		for netName := range doc.Networks {
			netName = strings.TrimSpace(netName)
			if netName != "" {
				networks = append(networks, netName)
			}
		}
		for _, svc := range doc.Services {
			ports = append(ports, extractComposeStrings(svc.Ports)...)
			envFiles = append(envFiles, extractComposeStrings(svc.EnvFile)...)
			networks = append(networks, extractComposeStrings(svc.Networks)...)
		}
		return sortedUnique(ports), sortedUnique(envFiles), sortedUnique(networks)
	}
	ports = detectComposeValues(text, `(?m)^\s*-\s*"?([0-9]+:[0-9]+)"?`)
	envFiles = detectComposeValues(text, `(?m)^\s*-\s*([.\w/-]*\.env[\w.-]*)`)
	return sortedUnique(ports), sortedUnique(envFiles), nil
}

func extractComposeStrings(val any) []string {
	var out []string
	switch v := val.(type) {
	case string:
		v = strings.TrimSpace(v)
		if v != "" {
			out = append(out, v)
		}
	case []any:
		for _, item := range v {
			if s, ok := item.(string); ok {
				s = strings.TrimSpace(s)
				if s != "" {
					out = append(out, s)
				}
			} else if m, ok := item.(map[string]any); ok {
				if published, ok := m["published"]; ok {
					if target, ok := m["target"]; ok {
						out = append(out, fmt.Sprintf("%v:%v", published, target))
					}
				}
			}
		}
	case map[string]any:
		for k := range v {
			k = strings.TrimSpace(k)
			if k != "" {
				out = append(out, k)
			}
		}
	}
	return out
}

func detectComposeValues(text, pattern string) []string {
	rx := regexp.MustCompile(pattern)
	var values []string
	for _, match := range rx.FindAllStringSubmatch(text, -1) {
		values = append(values, match[1])
	}
	return values
}

// dynamicTimeout bounds each Node process started for --dynamic inspection.
const dynamicTimeout = 3 * time.Second

// dynamicScript loads one entrypoint and prints the routes registered on an
// Express-style router as a single JSON line. Load failures are written to
// stderr and exit non-zero so the caller can record them as warnings.
const dynamicScript = `const path = require('path');
try {
  const mod = require(path.resolve(process.argv[1]));
  const app = mod.default || mod;
  if (app && app._router && app._router.stack) {
    const routes = [];
    app._router.stack.forEach(layer => {
      if (layer.route) {
        const methods = Object.keys(layer.route.methods).map(m => m.toUpperCase());
        routes.push({ path: layer.route.path, methods });
      }
    });
    if (routes.length > 0) console.log(JSON.stringify(routes));
  }
} catch (e) {
  console.error('extent-dynamic: ' + (e && e.message ? e.message : String(e)));
  process.exitCode = 1;
}`

// dynamicEnv is the only environment handed to the dynamic Node process. The
// target repository is untrusted, so inherited secrets must not be visible.
func dynamicEnv() []string {
	env := []string{"NODE_ENV=extent-analysis"}
	for _, name := range []string{"PATH", "HOME", "USERPROFILE", "SYSTEMROOT"} {
		if value := os.Getenv(name); value != "" {
			env = append(env, name+"="+value)
		}
	}
	return env
}

func inspectDynamicRoutes(root string, result *Result) {
	var targets []string
	for _, ep := range result.Entrypoints {
		if strings.HasSuffix(ep, ".js") {
			targets = append(targets, ep)
		}
	}
	if len(targets) == 0 {
		return
	}
	nodeCmd, err := exec.LookPath("node")
	if err != nil {
		addDynamicWarning(result, "", "node not found on PATH; dynamic route inspection skipped")
		return
	}
	if abs, absErr := filepath.Abs(nodeCmd); absErr == nil {
		nodeCmd = abs
	}
	for _, ep := range targets {
		routes, detail := runDynamicEntrypoint(root, nodeCmd, ep)
		if detail != "" {
			addDynamicWarning(result, ep, detail)
			continue
		}
		result.Routes = append(result.Routes, routes...)
	}
}

// runDynamicEntrypoint executes one entrypoint under Node and returns the
// routes it registered. A non-empty detail string describes why the entrypoint
// could not be inspected; it is never a fatal error.
func runDynamicEntrypoint(root, nodeCmd, ep string) ([]Route, string) {
	runner := process.Runner{Timeout: dynamicTimeout, MaxOutput: 1 << 20, Dir: root, Env: dynamicEnv(), ReplaceEnv: true}
	res := runner.Run(context.Background(), nodeCmd, "-e", dynamicScript, filepath.Join(root, ep))
	if res.TimedOut {
		return nil, fmt.Sprintf("timed out after %s", dynamicTimeout)
	}
	if res.Err != nil {
		detail := strings.TrimSpace(res.Stderr)
		if detail == "" {
			detail = res.Err.Error()
		}
		return nil, truncateDetail(detail)
	}
	if res.StdoutTruncated {
		return nil, "route output exceeded the size limit"
	}
	line := lastNonEmptyLine(res.Stdout)
	if line == "" {
		return nil, ""
	}
	var dynamicRoutes []struct {
		Path    string   `json:"path"`
		Methods []string `json:"methods"`
	}
	if err := json.Unmarshal([]byte(line), &dynamicRoutes); err != nil {
		return nil, "could not parse route output"
	}
	var routes []Route
	for _, dr := range dynamicRoutes {
		for _, m := range dr.Methods {
			routes = append(routes, Route{Method: m, Path: dr.Path, File: ep})
		}
	}
	return routes, ""
}

func addDynamicWarning(result *Result, path, detail string) {
	result.Warnings = append(result.Warnings, scanner.Warning{Kind: "dynamic", Path: path, Detail: detail})
}

func lastNonEmptyLine(text string) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

func truncateDetail(detail string) string {
	const limit = 500
	if len(detail) > limit {
		return detail[:limit] + "..."
	}
	return detail
}

func recommendations(result Result) []string {
	var out []string
	if len(result.Routes) > 0 {
		out = append(out, "Enable HTTP RED metrics per route and trace route span names.")
	}
	if len(result.DatabaseLibraries) > 0 {
		out = append(out, "Enable DB span enrichment and slow query reporting.")
	}
	if len(result.Loggers) > 0 {
		out = append(out, "Inject trace_id/span_id log correlation for detected logger.")
	}
	if len(result.Queues) > 0 {
		out = append(out, "Add producer/consumer spans and queue lag metrics.")
	}
	return out
}

func uniqueRoutes(routes []Route) []Route {
	seen := map[string]bool{}
	var out []Route
	for _, route := range routes {
		key := route.Method + " " + route.Path + " " + route.File
		if !seen[key] {
			out = append(out, route)
			seen[key] = true
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Method+" "+out[i].Path < out[j].Method+" "+out[j].Path
	})
	return out
}

func sortedUnique(values []string) []string {
	seen := map[string]bool{}
	var out []RouteStr
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && !seen[value] {
			out = append(out, RouteStr(value))
			seen[value] = true
		}
	}
	res := make([]string, len(out))
	for i, s := range out {
		res[i] = string(s)
	}
	sort.Strings(res)
	return res
}

type RouteStr string

func isComposeFile(path string) bool {
	base := strings.ToLower(filepath.Base(path))
	return base == "docker-compose.yml" || base == "docker-compose.yaml" || strings.HasPrefix(base, "docker-compose.") || strings.HasPrefix(base, "compose.")
}

// detectServiceName prefers the project's own package name over the folder
// name: package.json name (npm scope stripped), then pyproject [project].name,
// then the go.mod module path's last element, and finally the folder name.
func detectServiceName(root string) string {
	if name := packageJSONName(root); name != "" {
		return name
	}
	if name := pyprojectName(root); name != "" {
		return name
	}
	if name := goModuleName(root); name != "" {
		return name
	}
	return filepath.Base(root)
}

func packageJSONName(root string) string {
	data, err := os.ReadFile(filepath.Join(root, "package.json"))
	if err != nil {
		return ""
	}
	var pkg struct {
		Name string `json:"name"`
	}
	if json.Unmarshal(data, &pkg) != nil {
		return ""
	}
	name := strings.TrimSpace(pkg.Name)
	if strings.HasPrefix(name, "@") {
		if i := strings.Index(name, "/"); i >= 0 {
			name = name[i+1:]
		}
	}
	return strings.TrimSpace(name)
}

var (
	tomlSectionRx = regexp.MustCompile(`^\s*\[([^\]]+)\]\s*(#.*)?$`)
	tomlNameRx    = regexp.MustCompile(`^\s*name\s*=\s*["']([^"']+)["']`)
)

func pyprojectName(root string) string {
	data, err := os.ReadFile(filepath.Join(root, "pyproject.toml"))
	if err != nil {
		return ""
	}
	inProject := false
	for _, line := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
		if m := tomlSectionRx.FindStringSubmatch(line); m != nil {
			inProject = strings.TrimSpace(m[1]) == "project"
			continue
		}
		if inProject {
			if m := tomlNameRx.FindStringSubmatch(line); m != nil {
				return strings.TrimSpace(m[1])
			}
		}
	}
	return ""
}

var goMajorSuffixRx = regexp.MustCompile(`^v[0-9]+$`)

func goModuleName(root string) string {
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] != "module" {
			continue
		}
		module := strings.Trim(fields[1], `"`)
		parts := strings.Split(strings.TrimSuffix(module, "/"), "/")
		last := parts[len(parts)-1]
		// Major-version suffixes (module example.com/x/v2) name the repo, not the service.
		if goMajorSuffixRx.MatchString(last) && len(parts) > 1 {
			last = parts[len(parts)-2]
		}
		return last
	}
	return ""
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func relPath(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return filepath.ToSlash(rel)
}
