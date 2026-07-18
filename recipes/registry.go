package recipes

import (
	"bytes"
	"embed"
	"fmt"
	"gopkg.in/yaml.v3"
	"io"
	"io/fs"
	"sort"
	"strings"
)

//go:embed node/*.yaml python/*.yaml go/*.yaml dotnet/*.yaml java/*.yaml
var files embed.FS

type Recipe struct {
	Name       string     `yaml:"name"`
	Runtime    string     `yaml:"runtime"`
	Detect     Detect     `yaml:"detect"`
	Entrypoint Entrypoint `yaml:"entrypoint"`
	Inject     Inject     `yaml:"inject"`
	Metrics    Metrics    `yaml:"metrics"`
	Verify     Verify     `yaml:"verify"`
	Support    Support    `yaml:"support"`
}
type Detect struct {
	Files        []string `yaml:"files,omitempty"`
	Dependencies []string `yaml:"dependencies,omitempty"`
	Patterns     []string `yaml:"patterns,omitempty"`
}
type Entrypoint struct {
	Patterns []string `yaml:"patterns,omitempty"`
}
type Inject struct {
	Bootstrap  map[string]string `yaml:"bootstrap,omitempty"`
	Middleware map[string]string `yaml:"middleware,omitempty"`
	Logger     Logger            `yaml:"logger,omitempty"`
	Database   map[string]string `yaml:"database,omitempty"`
	GraphQL    map[string]string `yaml:"graphql,omitempty"`
}
type Logger struct {
	Supported []string `yaml:"supported,omitempty"`
}
type Metrics struct {
	HTTPRed   bool `yaml:"http_red"`
	Runtime   bool `yaml:"runtime"`
	DBLatency bool `yaml:"db_latency"`
	DBPool    bool `yaml:"db_pool"`
}
type Verify struct {
	RequestPath string `yaml:"request_path,omitempty"`
}
type Support struct {
	Bootstrap string `yaml:"bootstrap"`
	Deep      string `yaml:"deep"`
	Reason    string `yaml:"reason,omitempty"`
}

func Load(name string) (Recipe, error) {
	data, err := files.ReadFile(name)
	if err != nil {
		return Recipe{}, err
	}
	return decode(data, name)
}
func All() ([]Recipe, error) {
	var names []string
	for _, dir := range []string{"node", "python", "go", "dotnet", "java"} {
		p, err := fs.Glob(files, dir+"/*.yaml")
		if err != nil {
			return nil, err
		}
		names = append(names, p...)
	}
	sort.Strings(names)
	out := make([]Recipe, 0, len(names))
	for _, n := range names {
		r, e := Load(n)
		if e != nil {
			return nil, e
		}
		out = append(out, r)
	}
	return out, nil
}
func decode(data []byte, name string) (Recipe, error) {
	var r Recipe
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&r); err != nil {
		return r, fmt.Errorf("recipe %s: %w", name, err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return r, fmt.Errorf("recipe %s: multiple YAML documents are not supported", name)
		}
		return r, fmt.Errorf("recipe %s: %w", name, err)
	}
	if strings.TrimSpace(r.Name) == "" || strings.TrimSpace(r.Runtime) == "" {
		return r, fmt.Errorf("recipe %s: name and runtime are required", name)
	}
	allowedRuntimes := map[string]bool{"node": true, "python": true, "go": true, "dotnet": true, "java": true}
	if !allowedRuntimes[r.Runtime] {
		return r, fmt.Errorf("recipe %s: unsupported runtime %q", name, r.Runtime)
	}
	allowedSupport := map[string]bool{"stable": true, "experimental": true, "unsupported": true, "guidance": true}
	if !allowedSupport[r.Support.Bootstrap] || !allowedSupport[r.Support.Deep] {
		return r, fmt.Errorf("recipe %s: invalid bootstrap/deep support status", name)
	}
	if r.Verify.RequestPath != "" && !strings.HasPrefix(r.Verify.RequestPath, "/") {
		return r, fmt.Errorf("recipe %s: verify.request_path must start with /", name)
	}
	return r, nil
}
