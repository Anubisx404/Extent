package deps

import (
	"context"
	"errors"
	"fmt"
	"github.com/Anubisx404/Extent/internal/process"
)

type Runner interface {
	Run(context.Context, string, ...string) process.Result
}

func InstallCommand(managers []string) ([]string, bool) {
	c, err := selectCommand(managers)
	return c, err == nil
}
func selectCommand(managers []string) ([]string, error) {
	set := map[string]bool{}
	for _, m := range managers {
		set[m] = true
	}
	var c []string
	for _, x := range [][]string{{"pnpm", "install"}, {"yarn", "install"}, {"npm", "install"}, {"python", "-m", "pip", "install", "-r", "requirements.txt"}, {"go", "get", "go.opentelemetry.io/otel", "go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp", "go.opentelemetry.io/otel/sdk"}} {
		key := x[0]
		if key == "python" {
			key = "pip"
		}
		if key == "go" {
			key = "go-modules"
		}
		if set[key] {
			if c != nil {
				return nil, errors.New("ambiguous dependency managers")
			}
			c = x
		}
	}
	if c == nil {
		return nil, errors.New("no supported dependency install command found")
	}
	return c, nil
}
func Install(root string, managers []string) ([]string, error) {
	return InstallContext(context.Background(), process.Runner{Dir: root}, root, managers)
}
func InstallContext(ctx context.Context, r Runner, root string, managers []string) ([]string, error) {
	c, e := selectCommand(managers)
	if e != nil {
		return nil, e
	}
	res := r.Run(ctx, c[0], c[1:]...)
	if res.Err != nil {
		return c, fmt.Errorf("dependency install failed: %w", res.Err)
	}
	return c, nil
}
