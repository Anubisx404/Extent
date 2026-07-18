package codemods

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Anubisx404/Extent/internal/process"
)

type Command struct {
	Runtime string   `json:"runtime"`
	Command []string `json:"command"`
}
type RunResult struct {
	Commands     []Command `json:"commands"`
	Output       []string  `json:"output,omitempty"`
	Experimental bool      `json:"experimental"`
	Warning      string    `json:"warning,omitempty"`
}

type CommandRunner interface {
	Run(context.Context, string, ...string) process.Result
}

func PlanCommands(root string) []Command {
	var commands []Command
	if exists(filepath.Join(root, "package.json")) {
		commands = append(commands, Command{"node", []string{"node", filepath.Join("extent.codemods", "node", "deep-instrument.mjs"), root}})
	}
	if exists(filepath.Join(root, "requirements.txt")) || exists(filepath.Join(root, "pyproject.toml")) {
		commands = append(commands, Command{"python", []string{"python", filepath.Join("extent.codemods", "python", "deep_instrument.py"), root}})
	}
	if len(glob(root, "*.csproj")) > 0 {
		commands = append(commands, Command{"dotnet", []string{"dotnet", "run", "--project", filepath.Join("extent.codemods", "dotnet", "Extent.Codemods.csproj"), "--", root}})
	}
	if exists(filepath.Join(root, "pom.xml")) || exists(filepath.Join(root, "build.gradle")) || exists(filepath.Join(root, "build.gradle.kts")) {
		commands = append(commands, Command{"java", []string{"java", "-cp", filepath.Join("extent.codemods", "java", "*"), "ExtentJavaParserPatcher", root}})
	}
	return commands
}

func Run(root string) (RunResult, error) {
	return RunContext(context.Background(), root, process.Runner{Dir: root})
}
func RunContext(ctx context.Context, root string, runner CommandRunner) (RunResult, error) {
	commands := PlanCommands(root)
	result := RunResult{Commands: commands}
	if len(commands) > 0 {
		result.Experimental = true
		result.Warning = "deep codemods mutate target source outside the Extent file transaction; use only after review with a clean worktree"
	}
	for _, command := range commands {
		if len(command.Command) == 0 {
			continue
		}
		r := runner.Run(ctx, command.Command[0], command.Command[1:]...)
		if r.Stdout != "" || r.Stderr != "" {
			result.Output = append(result.Output, strings.TrimSpace(r.Stdout+r.Stderr))
		}
		if r.Err != nil {
			return result, &Failure{Runtime: command.Runtime, Command: command.Command, Result: r}
		}
	}
	return result, nil
}

type Failure struct {
	Runtime string
	Command []string
	Result  process.Result
}

func (e *Failure) Error() string {
	if e.Result.TimedOut {
		return e.Runtime + " codemod timed out: " + strings.Join(e.Command, " ")
	}
	return fmt.Sprintf("%s codemod failed (exit %d): %s", e.Runtime, e.Result.ExitCode, strings.Join(e.Command, " "))
}
func exists(path string) bool { _, err := os.Stat(path); return err == nil }
func glob(root, pattern string) []string {
	matches, _ := filepath.Glob(filepath.Join(root, pattern))
	return matches
}
