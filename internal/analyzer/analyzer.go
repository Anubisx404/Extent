package analyzer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"extent/internal/scanner"
)

type Result struct {
	Root                string      `json:"root"`
	ServiceName         string      `json:"serviceName"`
	Runtime             []string    `json:"runtime"`
	Frameworks          []string    `json:"frameworks"`
	Entrypoints         []string    `json:"entrypoints"`
	Routes              []Route     `json:"routes"`
	DatabaseLibraries   []string    `json:"databaseLibraries"`
	Queues              []string    `json:"queues"`
	Loggers             []string    `json:"loggers"`
	ExternalHTTPClients []string    `json:"externalHttpClients"`
	Docker              DockerState `json:"docker"`
	TestCommands        []string    `json:"testCommands"`
	Recommendations     []string    `json:"recommendations"`
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
	scan, err := scanner.Scan(root)
	if err != nil {
		return Result{}, err
	}
	result := Result{
		Root:              scan.Root,
		ServiceName:       filepath.Base(scan.Root),
		Runtime:           scan.Runtimes,
		Frameworks:        scan.Frameworks,
		Entrypoints:       scan.Entrypoints,
		DatabaseLibraries: scan.DatabaseLibraries,
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

	_ = filepath.WalkDir(scan.Root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && shouldSkip(d.Name()) && path != scan.Root {
			return filepath.SkipDir
		}
		if d.IsDir() {
			return nil
		}
		rel := relPath(scan.Root, path)
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		text := string(data)
		result.Routes = append(result.Routes, detectRoutes(rel, text)...)
		result.Loggers = append(result.Loggers, detectSourceLoggers(rel, text)...)
		result.ExternalHTTPClients = append(result.ExternalHTTPClients, detectSourceHTTPClients(rel, text)...)
		if isComposeFile(rel) {
			result.Docker.Ports = append(result.Docker.Ports, detectComposeValues(text, `(?m)^\s*-\s*"?([0-9]+:[0-9]+)"?`)...)
			result.Docker.EnvFiles = append(result.Docker.EnvFiles, detectComposeValues(text, `(?m)^\s*-\s*([.\w/-]*\.env[\w.-]*)`)...)
			result.Docker.Networks = append(result.Docker.Networks, detectComposeValues(text, `(?m)^\s{2,}([a-zA-Z0-9_-]+):\s*$`)...)
		}
		return nil
	})

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

func detectRoutes(file, text string) []Route {
	var routes []Route
	patterns := []struct {
		rx     *regexp.Regexp
		method int
		path   int
	}{
		{regexp.MustCompile(`(?i)\b(?:app|router)\.(get|post|put|patch|delete)\s*\(\s*["']([^"']+)["']`), 1, 2},
		{regexp.MustCompile(`(?i)@(get|post|put|patch|delete)\s*\(\s*["']([^"']+)["']`), 1, 2},
		{regexp.MustCompile(`(?i)\b(?:http\.HandleFunc|mux\.HandleFunc)\s*\(\s*["']([^"']+)["']`), 0, 1},
	}
	for _, pattern := range patterns {
		matches := pattern.rx.FindAllStringSubmatch(text, -1)
		for _, match := range matches {
			method := "ANY"
			if pattern.method > 0 {
				method = strings.ToUpper(match[pattern.method])
			}
			routes = append(routes, Route{Method: method, Path: match[pattern.path], File: file})
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
		"slog.":      "slog",
		"zap.":       "zap",
		"zerolog.":   "zerolog",
		"pino(":      "pino",
		"winston.":   "winston",
		"structlog.": "structlog",
		"logging.":   "python logging",
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
		"http.get(":     "net/http",
		"http.post(":    "net/http",
		"axios.":        "axios",
		"fetch(":        "fetch",
		"requests.get(": "requests",
		"httpx.":        "httpx",
	} {
		if strings.Contains(lower, needle) {
			clients = append(clients, client)
		}
	}
	return clients
}

func detectComposeValues(text, pattern string) []string {
	rx := regexp.MustCompile(pattern)
	var values []string
	for _, match := range rx.FindAllStringSubmatch(text, -1) {
		values = append(values, match[1])
	}
	return values
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
	var out []string
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && !seen[value] {
			out = append(out, value)
			seen[value] = true
		}
	}
	sort.Strings(out)
	return out
}

func shouldSkip(name string) bool {
	switch strings.ToLower(name) {
	case ".git", ".extent", ".gocache", ".smoke", "node_modules", "vendor", "bin", "obj", ".venv", "venv", "dist", "build", ".next":
		return true
	default:
		return false
	}
}

func isComposeFile(path string) bool {
	base := strings.ToLower(filepath.Base(path))
	return base == "docker-compose.yml" || base == "docker-compose.yaml" || strings.HasPrefix(base, "docker-compose.") || strings.HasPrefix(base, "compose.")
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
