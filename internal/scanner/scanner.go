package scanner

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type Result struct {
	Root              string    `json:"root"`
	Runtimes          []string  `json:"runtimes"`
	Frameworks        []string  `json:"frameworks"`
	PackageManagers   []string  `json:"packageManagers"`
	DatabaseLibraries []string  `json:"databaseLibraries"`
	ComposeFiles      []string  `json:"composeFiles"`
	Entrypoints       []string  `json:"entrypoints"`
	Signals           []Signal  `json:"signals"`
	Warnings          []Warning `json:"warnings,omitempty"`
}

type Signal struct {
	Kind  string `json:"kind"`
	Path  string `json:"path"`
	Value string `json:"value,omitempty"`
}

type Warning struct {
	Kind   string `json:"kind"`
	Path   string `json:"path"`
	Detail string `json:"detail"`
}

func Scan(root string) (Result, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return Result{}, err
	}
	result := Result{Root: absRoot}
	runtimes := map[string]bool{}
	frameworks := map[string]bool{}
	packageManagers := map[string]bool{}
	databaseLibraries := map[string]bool{}

	err = filepath.WalkDir(absRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && shouldSkipDir(d.Name()) && path != absRoot {
			return filepath.SkipDir
		}
		if d.IsDir() {
			return nil
		}

		rel := relPath(absRoot, path)
		base := strings.ToLower(d.Name())
		switch {
		case base == "package.json":
			runtimes["node"] = true
			packageManagers["npm"] = true
			result.Signals = append(result.Signals, Signal{Kind: "runtime", Path: rel, Value: "node"})
			result.Signals = append(result.Signals, Signal{Kind: "package-manager", Path: rel, Value: "npm"})
			if err := addPackageSignals(path, rel, frameworks, databaseLibraries, &result); err != nil {
				result.Warnings = append(result.Warnings, Warning{Kind: "manifest", Path: rel, Detail: err.Error()})
			}
		case base == "package-lock.json":
			packageManagers["npm"] = true
			result.Signals = append(result.Signals, Signal{Kind: "package-manager", Path: rel, Value: "npm"})
		case base == "pnpm-lock.yaml":
			packageManagers["pnpm"] = true
			result.Signals = append(result.Signals, Signal{Kind: "package-manager", Path: rel, Value: "pnpm"})
		case base == "yarn.lock":
			packageManagers["yarn"] = true
			result.Signals = append(result.Signals, Signal{Kind: "package-manager", Path: rel, Value: "yarn"})
		case base == "go.mod":
			runtimes["go"] = true
			packageManagers["go-modules"] = true
			result.Signals = append(result.Signals, Signal{Kind: "runtime", Path: rel, Value: "go"})
			addTextFrameworkSignals(path, rel, frameworks, databaseLibraries, &result)
		case base == "pyproject.toml" || base == "requirements.txt":
			runtimes["python"] = true
			if base == "requirements.txt" {
				packageManagers["pip"] = true
				result.Signals = append(result.Signals, Signal{Kind: "package-manager", Path: rel, Value: "pip"})
			}
			result.Signals = append(result.Signals, Signal{Kind: "runtime", Path: rel, Value: "python"})
			addTextFrameworkSignals(path, rel, frameworks, databaseLibraries, &result)
		case base == "poetry.lock":
			packageManagers["poetry"] = true
			result.Signals = append(result.Signals, Signal{Kind: "package-manager", Path: rel, Value: "poetry"})
		case base == "uv.lock":
			packageManagers["uv"] = true
			result.Signals = append(result.Signals, Signal{Kind: "package-manager", Path: rel, Value: "uv"})
		case strings.HasSuffix(base, ".csproj") || strings.HasSuffix(base, ".sln") || strings.HasSuffix(base, ".slnx") || strings.HasSuffix(base, ".slnf"):
			runtimes["dotnet"] = true
			packageManagers["dotnet"] = true
			result.Signals = append(result.Signals, Signal{Kind: "runtime", Path: rel, Value: "dotnet"})
			result.Signals = append(result.Signals, Signal{Kind: "package-manager", Path: rel, Value: "dotnet"})
			addTextFrameworkSignals(path, rel, frameworks, databaseLibraries, &result)
		case base == "packages.lock.json":
			packageManagers["nuget"] = true
			result.Signals = append(result.Signals, Signal{Kind: "package-manager", Path: rel, Value: "nuget"})
		case base == "pom.xml" || base == "build.gradle" || base == "build.gradle.kts":
			runtimes["java"] = true
			result.Signals = append(result.Signals, Signal{Kind: "runtime", Path: rel, Value: "java"})
			addTextFrameworkSignals(path, rel, frameworks, databaseLibraries, &result)
			if base == "pom.xml" {
				packageManagers["maven"] = true
			} else {
				packageManagers["gradle"] = true
			}
		case isComposeFile(base):
			result.ComposeFiles = append(result.ComposeFiles, rel)
			result.Signals = append(result.Signals, Signal{Kind: "docker-compose", Path: rel})
		case isLikelyEntrypoint(base):
			result.Entrypoints = append(result.Entrypoints, rel)
		case strings.HasPrefix(base, "appsettings") && strings.HasSuffix(base, ".json"):
			addAppSettingsSignals(path, rel, frameworks, databaseLibraries, &result)
		case strings.HasSuffix(base, ".http"):
			addHttpFileSignals(path, rel, &result)
		case base == "launchsettings.json":
			addLaunchSettingsSignals(path, rel, &result)
		}
		return nil
	})
	if err != nil {
		return Result{}, err
	}

	result.Runtimes = keys(runtimes)
	result.Frameworks = keys(frameworks)
	result.PackageManagers = keys(packageManagers)
	result.DatabaseLibraries = keys(databaseLibraries)
	sort.Strings(result.ComposeFiles)
	sort.Strings(result.Entrypoints)
	return result, nil
}

func shouldSkipDir(name string) bool {
	return IsExcludedDir(name)
}

var excludedDirs = []string{".git", ".extent", ".gocache", ".smoke", "extent.codemods", ".extent-backup", "docs", "plans", "baseline_test", "node_modules", "vendor", "bin", "obj", ".venv", "venv", "dist", "build", ".next", ".agents", ".aider", ".antigravitycli", ".claude", ".cline", ".cody", ".codex", ".continue", ".cursor", ".gemini", ".hermes", ".omg", ".omx", ".opencode", ".qwen", ".sweep", ".windsurf", "ai-plans", "agent-plans"}

func DefaultExcludedDirs() []string { return append([]string(nil), excludedDirs...) }
func IsExcludedDir(name string) bool {
	name = strings.ToLower(name)
	for _, excluded := range excludedDirs {
		if name == excluded {
			return true
		}
	}
	return false
}

func isComposeFile(name string) bool {
	return name == "docker-compose.yml" ||
		name == "docker-compose.yaml" ||
		strings.HasPrefix(name, "docker-compose.") && (strings.HasSuffix(name, ".yml") || strings.HasSuffix(name, ".yaml")) ||
		strings.HasPrefix(name, "compose.") && (strings.HasSuffix(name, ".yml") || strings.HasSuffix(name, ".yaml"))
}

func isLikelyEntrypoint(name string) bool {
	switch strings.ToLower(name) {
	case "main.go", "main.py", "app.py", "server.js", "server.ts", "index.js", "index.ts", "program.cs":
		return true
	default:
		return false
	}
}

func addPackageSignals(path, rel string, frameworks, databaseLibraries map[string]bool, result *Result) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read package manifest: %w", err)
	}
	var pkg struct {
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return fmt.Errorf("parse package manifest: %w", err)
	}
	deps := map[string]string{}
	for k, v := range pkg.Dependencies {
		deps[k] = v
	}
	for k, v := range pkg.DevDependencies {
		deps[k] = v
	}
	depFrameworks := map[string]string{
		"express":       "express",
		"@nestjs/core":  "nestjs",
		"next":          "nextjs",
		"fastify":       "fastify",
		"svelte":        "svelte",
		"@sveltejs/kit": "sveltekit",
	}
	for dep, framework := range depFrameworks {
		if _, ok := deps[dep]; ok {
			frameworks[framework] = true
			result.Signals = append(result.Signals, Signal{Kind: "framework", Path: rel, Value: framework})
		}
	}
	depDatabases := map[string]string{
		"pg":             "postgres",
		"mysql":          "mysql",
		"mysql2":         "mysql",
		"mongodb":        "mongodb",
		"mongoose":       "mongodb",
		"redis":          "redis",
		"ioredis":        "redis",
		"sequelize":      "sequelize",
		"typeorm":        "typeorm",
		"prisma":         "prisma",
		"@prisma/client": "prisma",
	}
	for dep, database := range depDatabases {
		if _, ok := deps[dep]; ok {
			databaseLibraries[database] = true
			result.Signals = append(result.Signals, Signal{Kind: "database", Path: rel, Value: database})
		}
	}
	return nil
}

func addTextFrameworkSignals(path, rel string, frameworks, databaseLibraries map[string]bool, result *Result) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	text := strings.ToLower(string(data))
	candidates := map[string]string{
		"fastapi":                  "fastapi",
		"django":                   "django",
		"flask":                    "flask",
		"microsoft.aspnetcore":     "aspnetcore",
		"spring-boot":              "springboot",
		"github.com/gin-gonic/gin": "gin",
		"github.com/gofiber/fiber": "fiber",
		"github.com/go-chi/chi":    "chi",
	}
	for needle, framework := range candidates {
		if strings.Contains(text, needle) {
			frameworks[framework] = true
			result.Signals = append(result.Signals, Signal{Kind: "framework", Path: rel, Value: framework})
		}
	}
	dbCandidates := map[string]string{
		"sqlalchemy":                     "sqlalchemy",
		"psycopg2":                       "postgres",
		"psycopg":                        "postgres",
		"asyncpg":                        "postgres",
		"pymongo":                        "mongodb",
		"redis":                          "redis",
		"mysqlclient":                    "mysql",
		"pymysql":                        "mysql",
		"github.com/lib/pq":              "postgres",
		"github.com/jackc/pgx":           "postgres",
		"github.com/go-sql-driver/mysql": "mysql",
		"go.mongodb.org/mongo-driver":    "mongodb",
		"github.com/redis/go-redis":      "redis",
		"github.com/go-redis/redis":      "redis",
		"gorm.io/gorm":                   "gorm",
		"github.com/jmoiron/sqlx":        "sqlx",
	}
	for needle, database := range dbCandidates {
		if strings.Contains(text, needle) {
			databaseLibraries[database] = true
			result.Signals = append(result.Signals, Signal{Kind: "database", Path: rel, Value: database})
		}
	}
}

func addAppSettingsSignals(path, rel string, frameworks, databaseLibraries map[string]bool, result *Result) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return
	}
	result.Signals = append(result.Signals, Signal{Kind: "appsettings", Path: rel})

	if connStrings, ok := raw["ConnectionStrings"].(map[string]any); ok {
		for key, val := range connStrings {
			result.Signals = append(result.Signals, Signal{Kind: "connection-string", Path: rel, Value: key})
			str, ok := val.(string)
			if !ok {
				continue
			}
			lower := strings.ToLower(str)
			if strings.Contains(lower, "postgres") || strings.Contains(lower, "5432") || strings.Contains(lower, "npgsql") {
				databaseLibraries["postgres"] = true
				result.Signals = append(result.Signals, Signal{Kind: "database", Path: rel, Value: "postgres"})
			}
			if strings.Contains(lower, "sqlserver") || strings.Contains(lower, "1433") || strings.Contains(lower, "trusted_connection") || strings.Contains(lower, "initial catalog") || strings.Contains(lower, "sqlexpress") {
				databaseLibraries["sqlserver"] = true
				result.Signals = append(result.Signals, Signal{Kind: "database", Path: rel, Value: "sqlserver"})
			}
			if strings.Contains(lower, "mysql") || strings.Contains(lower, "3306") {
				databaseLibraries["mysql"] = true
				result.Signals = append(result.Signals, Signal{Kind: "database", Path: rel, Value: "mysql"})
			}
			if strings.Contains(lower, "mongodb") || strings.Contains(lower, "27017") {
				databaseLibraries["mongodb"] = true
				result.Signals = append(result.Signals, Signal{Kind: "database", Path: rel, Value: "mongodb"})
			}
			if strings.Contains(lower, "redis") || strings.Contains(lower, "6379") {
				databaseLibraries["redis"] = true
				result.Signals = append(result.Signals, Signal{Kind: "database", Path: rel, Value: "redis"})
			}
			if strings.Contains(lower, ".db") || strings.Contains(lower, "sqlite") || strings.Contains(lower, "data source=") {
				databaseLibraries["sqlite"] = true
				result.Signals = append(result.Signals, Signal{Kind: "database", Path: rel, Value: "sqlite"})
			}
		}
	}

	if _, ok := raw["OpenTelemetry"]; ok {
		result.Signals = append(result.Signals, Signal{Kind: "otel-config", Path: rel, Value: "OpenTelemetry"})
	}

	if urls, ok := raw["Urls"].(string); ok && urls != "" {
		for _, u := range strings.Split(urls, ";") {
			trimmed := strings.TrimSpace(u)
			if trimmed != "" {
				result.Signals = append(result.Signals, Signal{Kind: "app-url", Path: rel, Value: trimmed})
			}
		}
	}
}

func addHttpFileSignals(path, rel string, result *Result) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	result.Signals = append(result.Signals, Signal{Kind: "http-file", Path: rel})
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "//") {
			continue
		}
		if strings.HasPrefix(trimmed, "@") && strings.Contains(trimmed, "=") {
			parts := strings.SplitN(trimmed, "=", 2)
			val := strings.TrimSpace(parts[1])
			if strings.HasPrefix(val, "http://") || strings.HasPrefix(val, "https://") {
				result.Signals = append(result.Signals, Signal{Kind: "http-host", Path: rel, Value: val})
			}
			continue
		}
		for _, method := range []string{"GET", "POST", "PUT", "DELETE", "PATCH", "HEAD", "OPTIONS"} {
			if strings.HasPrefix(trimmed, method+" ") {
				tokens := strings.Fields(trimmed)
				if len(tokens) >= 2 {
					result.Signals = append(result.Signals, Signal{Kind: "http-endpoint", Path: rel, Value: method + " " + tokens[1]})
				}
				break
			}
		}
	}
}

func addLaunchSettingsSignals(path, rel string, result *Result) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var raw struct {
		Profiles map[string]struct {
			ApplicationURL string `json:"applicationUrl"`
		} `json:"profiles"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return
	}
	result.Signals = append(result.Signals, Signal{Kind: "launchsettings", Path: rel})
	for _, p := range raw.Profiles {
		if p.ApplicationURL != "" {
			for _, u := range strings.Split(p.ApplicationURL, ";") {
				trimmed := strings.TrimSpace(u)
				if trimmed != "" {
					result.Signals = append(result.Signals, Signal{Kind: "dev-url", Path: rel, Value: trimmed})
				}
			}
		}
	}
}

func relPath(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return filepath.ToSlash(rel)
}

func keys(values map[string]bool) []string {
	out := make([]string, 0, len(values))
	for key := range values {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}
