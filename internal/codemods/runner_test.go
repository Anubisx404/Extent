package codemods

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPlanCommandsDetectsLanguageCodemods(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "package.json"), "{}")
	mustWrite(t, filepath.Join(root, "requirements.txt"), "fastapi\n")
	mustWrite(t, filepath.Join(root, "app.csproj"), "<Project />")
	mustWrite(t, filepath.Join(root, "pom.xml"), "<project />")

	commands := PlanCommands(root)

	for _, want := range []string{"node", "python", "dotnet", "java"} {
		if !hasRuntime(commands, want) {
			t.Fatalf("expected %s codemod command in %#v", want, commands)
		}
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func hasRuntime(commands []Command, runtime string) bool {
	for _, command := range commands {
		if command.Runtime == runtime {
			return true
		}
	}
	return false
}
