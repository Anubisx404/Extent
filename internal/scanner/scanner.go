package scanner

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type Result struct {
	Root              string   `json:"root"`
	Runtimes          []string `json:"runtimes"`
	Frameworks        []string `json:"frameworks"`
	PackageManagers   []string `json:"packageManagers"`
	DatabaseLibraries []string `json:"databaseLibraries"`
	ComposeFiles      []string `json:"composeFiles"`
	Entrypoints       []string `json:"entrypoints"`
	Signals           []Signal `json:"signals"`
}

type Signal struct {
	Kind  string `json:"kind"`
	Path  string `json:"path"`
	Value string `json:"value,omitempty"`
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
			addPackageSignals(path, rel, frameworks, databaseLibraries, &result)
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
		case strings.HasSuffix(base, ".csproj"):
			runtimes["dotnet"] = true
			result.Signals = append(result.Signals, Signal{Kind: "runtime", Path: rel, Value: "dotnet"})
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
	switch strings.ToLower(name) {
	case ".git", ".extent", ".gocache", ".smoke", "node_modules", "vendor", "bin", "obj", ".venv", "venv", "dist", "build", ".next":
		return true
	default:
		return false
	}
}

func isComposeFile(name string) bool {
	return name == "docker-compose.yml" ||
		name == "docker-compose.yaml" ||
		strings.HasPrefix(name, "docker-compose.") && (strings.HasSuffix(name, ".yml") || strings.HasSuffix(name, ".yaml")) ||
		strings.HasPrefix(name, "compose.") && (strings.HasSuffix(name, ".yml") || strings.HasSuffix(name, ".yaml"))
}

func isLikelyEntrypoint(name string) bool {
	switch name {
	case "main.go", "main.py", "app.py", "server.js", "server.ts", "index.js", "index.ts", "program.cs":
		return true
	default:
		return false
	}
}

func addPackageSignals(path, rel string, frameworks, databaseLibraries map[string]bool, result *Result) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var pkg struct {
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
	}
	if json.Unmarshal(data, &pkg) != nil {
		return
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
