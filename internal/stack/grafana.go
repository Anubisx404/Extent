package stack

import (
	"os"
	"path/filepath"
	"strings"
)

// EnvFile is the per-project env file that holds generated stack credentials.
// `extent apply` writes it; `docker compose` reads it via --env-file.
const EnvFile = ".env.observability"

const (
	GrafanaUserKey     = "GRAFANA_ADMIN_USER"
	GrafanaPasswordKey = "GRAFANA_ADMIN_PASSWORD"
	DefaultGrafanaUser = "admin"
	GrafanaURL         = "http://localhost:3000"
)

// ParseEnv reads KEY=VALUE lines. Blank lines and lines starting with # are
// ignored; when a key repeats, the last value wins.
func ParseEnv(content string) map[string]string {
	values := map[string]string{}
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		values[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	return values
}

// GrafanaCredentials returns the Grafana admin user and password recorded in
// the project's EnvFile. The password is empty when the file is missing or has none.
func GrafanaCredentials(root string) (user, password string) {
	data, err := os.ReadFile(filepath.Join(root, EnvFile))
	if err != nil {
		return DefaultGrafanaUser, ""
	}
	values := ParseEnv(string(data))
	user = values[GrafanaUserKey]
	if user == "" {
		user = DefaultGrafanaUser
	}
	return user, values[GrafanaPasswordKey]
}

// GrafanaHint is the one-line Grafana summary printed by `extent stack up`.
// It never includes the password itself.
func GrafanaHint(root string) string {
	user, _ := GrafanaCredentials(root)
	return "Grafana: " + GrafanaURL + " (user " + user + ", password in " + EnvFile + ")"
}

// composeArgs returns the global docker compose arguments for a project. The
// env file is passed only when it exists, so compose does not fail on a
// project that has not been applied yet.
func composeArgs(root, project string) []string {
	args := []string{"compose", "-f", composeFile}
	if info, err := os.Stat(filepath.Join(root, EnvFile)); err == nil && info.Mode().IsRegular() {
		args = append(args, "--env-file", EnvFile)
	}
	return append(args, "-p", project)
}
