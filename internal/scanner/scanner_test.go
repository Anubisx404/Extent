package scanner

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScanDetectsNodeExpressAndCompose(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "package.json"), `{"dependencies":{"express":"^4.0.0","pg":"^8.0.0"}}`)
	mustWrite(t, filepath.Join(root, "package-lock.json"), `{"lockfileVersion":3}`)
	mustWrite(t, filepath.Join(root, "docker-compose.yml"), "services: {}\n")
	mustWrite(t, filepath.Join(root, "server.js"), "console.log('ok')\n")

	result, err := Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, result.Runtimes, "node")
	assertContains(t, result.Frameworks, "express")
	assertContains(t, result.PackageManagers, "npm")
	assertContains(t, result.DatabaseLibraries, "postgres")
	assertContains(t, result.ComposeFiles, "docker-compose.yml")
	assertContains(t, result.Entrypoints, "server.js")
}

func TestScanDetectsPythonDatabaseLibraries(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "requirements.txt"), "fastapi\nsqlalchemy\nredis\n")

	result, err := Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, result.DatabaseLibraries, "sqlalchemy")
	assertContains(t, result.DatabaseLibraries, "redis")
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func assertContains(t *testing.T, values []string, want string) {
	t.Helper()
	for _, value := range values {
		if value == want {
			return
		}
	}
	t.Fatalf("expected %q in %#v", want, values)
}
