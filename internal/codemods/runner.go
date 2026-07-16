package codemods

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type Command struct {
	Runtime string   `json:"runtime"`
	Command []string `json:"command"`
}

type RunResult struct {
	Commands []Command `json:"commands"`
	Output   []string  `json:"output,omitempty"`
}

func PlanCommands(root string) []Command {
	var commands []Command
	if exists(filepath.Join(root, "package.json")) {
		commands = append(commands, Command{Runtime: "node", Command: []string{"node", filepath.Join("extent.codemods", "node", "deep-instrument.mjs"), root}})
	}
	if exists(filepath.Join(root, "requirements.txt")) || exists(filepath.Join(root, "pyproject.toml")) {
		commands = append(commands, Command{Runtime: "python", Command: []string{"python", filepath.Join("extent.codemods", "python", "deep_instrument.py"), root}})
	}
	if len(glob(root, "*.csproj")) > 0 {
		commands = append(commands, Command{Runtime: "dotnet", Command: []string{"dotnet", "run", "--project", filepath.Join("extent.codemods", "dotnet", "Extent.Codemods.csproj"), "--", root}})
	}
	if exists(filepath.Join(root, "pom.xml")) || exists(filepath.Join(root, "build.gradle")) || exists(filepath.Join(root, "build.gradle.kts")) {
		commands = append(commands, Command{Runtime: "java", Command: []string{"java", "-cp", filepath.Join("extent.codemods", "java", "*"), "ExtentJavaParserPatcher", root}})
	}
	return commands
}

func Run(root string) (RunResult, error) {
	commands := PlanCommands(root)
	result := RunResult{Commands: commands}
	for _, command := range commands {
		if len(command.Command) == 0 {
			continue
		}
		if _, err := exec.LookPath(command.Command[0]); err != nil {
			return result, errors.New(command.Runtime + " codemod tool not found in PATH: " + command.Command[0])
		}
		cmd := exec.Command(command.Command[0], command.Command[1:]...)
		cmd.Dir = root
		out, err := cmd.CombinedOutput()
		if len(out) > 0 {
			result.Output = append(result.Output, strings.TrimSpace(string(out)))
		}
		if err != nil {
			return result, errors.New(command.Runtime + " codemod failed: " + strings.TrimSpace(string(out)))
		}
	}
	return result, nil
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func glob(root, pattern string) []string {
	matches, _ := filepath.Glob(filepath.Join(root, pattern))
	return matches
}
