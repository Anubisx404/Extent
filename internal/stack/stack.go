package stack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/Anubisx404/Extent/internal/process"
	"gopkg.in/yaml.v3"
)

const composeFile = "docker-compose.observability.yml"

type Runner interface {
	Run(context.Context, string, ...string) process.Result
}

func Up(root string) error { return UpContext(context.Background(), process.Runner{Dir: root}, root) }
func UpConfigured(root string, opts UpOptions) error {
	return UpWithOptions(context.Background(), process.Runner{Dir: root}, root, opts)
}
func Down(root string) error {
	return DownContext(context.Background(), process.Runner{Dir: root}, root)
}
func Status(root string) error {
	_, err := InspectContext(context.Background(), process.Runner{Dir: root}, root)
	return err
}
func UpContext(ctx context.Context, r Runner, root string) error {
	return UpWithOptions(ctx, r, root, UpOptions{Wait: true})
}

type UpOptions struct {
	Wait        bool
	Timeout     time.Duration
	ProjectName string
}

type ComponentStatus struct {
	Name     string `json:"name"`
	Service  string `json:"service,omitempty"`
	State    string `json:"state,omitempty"`
	Health   string `json:"health,omitempty"`
	ExitCode int    `json:"exitCode,omitempty"`
}

type StatusReport struct {
	Project    string            `json:"project"`
	Components []ComponentStatus `json:"components"`
}

func UpWithOptions(ctx context.Context, r Runner, root string, opts UpOptions) error {
	if opts.Timeout <= 0 {
		opts.Timeout = 2 * time.Minute
	}
	if err := preflight(ctx, r, root); err != nil {
		return err
	}
	if opts.ProjectName == "" {
		opts.ProjectName = projectName(root)
	} else if !validProjectName.MatchString(opts.ProjectName) {
		return fmt.Errorf("invalid Compose project name %q", opts.ProjectName)
	}
	args := []string{"compose", "-f", composeFile}
	args = append(args, "-p", opts.ProjectName)
	args = append(args, "config", "--quiet")
	if res := r.Run(ctx, "docker", args...); res.Err != nil {
		return processFailure("compose config invalid", res)
	}
	args = args[:len(args)-2]
	statusArgs := append(append([]string{}, args...), "ps", "--format", "json")
	if status := r.Run(ctx, "docker", statusArgs...); status.Err != nil || strings.TrimSpace(status.Stdout) == "" || strings.TrimSpace(status.Stdout) == "[]" {
		if err := checkPortConflicts(root); err != nil {
			return err
		}
	}
	args = append(args, "up", "-d", "--force-recreate")
	if opts.Wait {
		args = append(args, "--wait", "--wait-timeout", fmt.Sprint(int(opts.Timeout.Seconds())))
	}
	c, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()
	if res := r.Run(c, "docker", args...); res.Err != nil {
		return processFailure("docker compose failed", res)
	}
	return nil
}
func DownContext(ctx context.Context, r Runner, root string) error {
	return dockerCompose(ctx, r, root, "down")
}
func StatusContext(ctx context.Context, r Runner, root string) error {
	_, err := InspectContext(ctx, r, root)
	return err
}
func Inspect(root string) (StatusReport, error) {
	return InspectContext(context.Background(), process.Runner{Dir: root}, root)
}
func InspectContext(ctx context.Context, r Runner, root string) (StatusReport, error) {
	if err := preflight(ctx, r, root); err != nil {
		return StatusReport{}, err
	}
	project := projectName(root)
	args := []string{"compose", "-f", composeFile, "-p", project, "ps", "--format", "json"}
	result := r.Run(ctx, "docker", args...)
	if result.Err != nil {
		return StatusReport{}, processFailure("docker compose status failed", result)
	}
	components, err := parseStatus(result.Stdout)
	if err != nil {
		return StatusReport{}, err
	}
	return StatusReport{Project: project, Components: components}, nil
}
func parseStatus(raw string) ([]ComponentStatus, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return []ComponentStatus{}, nil
	}
	var components []ComponentStatus
	if strings.HasPrefix(raw, "[") {
		if err := json.Unmarshal([]byte(raw), &components); err != nil {
			return nil, fmt.Errorf("decode Compose status: %w", err)
		}
		return components, nil
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	for {
		var component ComponentStatus
		if err := decoder.Decode(&component); err == io.EOF {
			break
		} else if err != nil {
			return nil, fmt.Errorf("decode Compose status: %w", err)
		}
		components = append(components, component)
	}
	return components, nil
}
func dockerCompose(ctx context.Context, r Runner, root string, args ...string) error {
	return composeWithPreflight(ctx, r, root, args...)
}

func checkPortConflicts(root string) error {
	data, err := os.ReadFile(filepath.Join(root, composeFile))
	if err != nil {
		return err
	}
	var document struct {
		Services map[string]struct {
			Ports []string `yaml:"ports"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(data, &document); err != nil {
		return fmt.Errorf("parse Compose ports: %w", err)
	}
	seen := map[string]struct{}{}
	for _, service := range document.Services {
		for _, mapping := range service.Ports {
			mapping = strings.SplitN(mapping, "/", 2)[0]
			parts := strings.Split(mapping, ":")
			if len(parts) < 2 {
				continue
			}
			published := parts[len(parts)-2]
			if published == "" || strings.Contains(published, "-") {
				continue
			}
			if _, duplicate := seen[published]; duplicate {
				continue
			}
			seen[published] = struct{}{}
			listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", published))
			if err != nil {
				return fmt.Errorf("host port %s is unavailable: %w", published, err)
			}
			_ = listener.Close()
		}
	}
	return nil
}

func preflight(ctx context.Context, r Runner, root string) error {
	info, err := os.Lstat(filepath.Join(root, composeFile))
	if err != nil {
		return fmt.Errorf("compose file unavailable: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("compose file must be a regular, non-symlink file")
	}
	if e := r.Run(ctx, "docker", "--version").Err; e != nil {
		return errors.New("docker executable unavailable")
	}
	if e := r.Run(ctx, "docker", "compose", "version").Err; e != nil {
		return errors.New("docker compose unavailable")
	}
	if e := r.Run(ctx, "docker", "info", "--format", "{{json .ServerVersion}}").Err; e != nil {
		return errors.New("docker daemon unavailable")
	}
	return nil
}
func composeWithPreflight(ctx context.Context, r Runner, root string, args ...string) error {
	if err := preflight(ctx, r, root); err != nil {
		return err
	}
	a := append([]string{"compose", "-f", composeFile, "-p", projectName(root)}, args...)
	res := r.Run(ctx, "docker", a...)
	if res.Err != nil {
		return processFailure("docker compose failed", res)
	}
	return nil
}

func processFailure(prefix string, result process.Result) error {
	detail := strings.TrimSpace(result.Stderr)
	if detail == "" {
		detail = strings.TrimSpace(result.Stdout)
	}
	if detail == "" {
		return fmt.Errorf("%s: %w", prefix, result.Err)
	}
	return fmt.Errorf("%s: %w: %s", prefix, result.Err, detail)
}

var (
	validProjectName  = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)
	invalidProjectRun = regexp.MustCompile(`[^a-z0-9_-]+`)
)

func projectName(root string) string {
	name := strings.ToLower(filepath.Base(filepath.Clean(root)))
	name = invalidProjectRun.ReplaceAllString(name, "-")
	name = strings.Trim(name, "-_")
	if name == "" {
		name = "project"
	}
	name = "extent-" + name
	if len(name) > 63 {
		name = strings.TrimRight(name[:63], "-_")
	}
	return name
}
