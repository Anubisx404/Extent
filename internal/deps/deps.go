package deps

import (
	"errors"
	"os/exec"
	"strings"
)

func InstallCommand(managers []string) ([]string, bool) {
	has := map[string]bool{}
	for _, manager := range managers {
		has[manager] = true
	}
	switch {
	case has["pnpm"]:
		return []string{"pnpm", "install"}, true
	case has["yarn"]:
		return []string{"yarn", "install"}, true
	case has["npm"]:
		return []string{"npm", "install"}, true
	case has["pip"]:
		return []string{"python", "-m", "pip", "install", "-r", "requirements.txt"}, true
	case has["go-modules"]:
		return []string{"go", "get", "go.opentelemetry.io/otel", "go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp", "go.opentelemetry.io/otel/sdk"}, true
	default:
		return nil, false
	}
}

func Install(root string, managers []string) ([]string, error) {
	command, ok := InstallCommand(managers)
	if !ok {
		return nil, errors.New("no supported dependency install command found")
	}
	cmd := exec.Command(command[0], command[1:]...)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		return command, errors.New(strings.TrimSpace(string(out)))
	}
	return command, nil
}
