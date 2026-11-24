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
	candidates := []struct {
		key string
		alt string
		cmd []string
	}{
		{key: "pnpm", cmd: []string{"pnpm", "install"}},
		{key: "yarn", cmd: []string{"yarn", "install"}},
		{key: "npm", cmd: []string{"npm", "install"}},
		{key: "pip", cmd: []string{"python", "-m", "pip", "install", "-r", "requirements.txt"}},
		{key: "pip-pyproject", cmd: []string{"python", "-m", "pip", "install", "-e", "."}},
		{key: "poetry", cmd: []string{"poetry", "install"}},
		{key: "uv", cmd: []string{"uv", "sync"}},
		{key: "go-modules", cmd: []string{"go", "mod", "tidy"}},
		{key: "dotnet", alt: "nuget", cmd: []string{"dotnet", "restore"}},
	}
	var c []string
	for _, cand := range candidates {
		if set[cand.key] || (cand.alt != "" && set[cand.alt]) {
			if c != nil {
				return nil, errors.New("ambiguous dependency managers")
			}
			c = cand.cmd
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
