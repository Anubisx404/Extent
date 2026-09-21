package instrumenter

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestGeneratedProjectTransactions(t *testing.T) {
	t.Logf("runtime=%s/%s node=%s python=%s go=%s", runtime.GOOS, runtime.GOARCH, toolVersion("node", "--version"), toolVersion("python", "--version"), toolVersion("go", "version"))
	verifyNodeGeneratedProject(t)
	verifyPythonGeneratedProject(t)
	verifyGoGeneratedProject(t)
}

func verifyNodeGeneratedProject(t *testing.T) {
	t.Helper()
	root := t.TempDir()
	original := map[string][]byte{"package.json": []byte(`{"dependencies":{"express":"4.21.2"}}`), "server.js": []byte("console.log('ok')\n")}
	for name, data := range original {
		mustWrite(t, filepath.Join(root, name), string(data))
	}
	if _, err := Instrument(root, Options{Mode: "bootstrap"}); err != nil {
		t.Fatal("node apply:", err)
	}
	first := snapshotTree(t, root)
	if _, err := Instrument(root, Options{Mode: "bootstrap"}); err != nil {
		t.Fatal("node second apply:", err)
	}
	if !sameSnapshot(first, snapshotTree(t, root)) {
		t.Fatal("node second apply changed generated project")
	}
	if _, err := exec.LookPath("node"); err == nil {
		for _, file := range []string{"extent.instrumentation.js", "server.js"} {
			if err := runIn(root, "node", "--check", file); err != nil {
				t.Fatalf("node syntax failed for %s: %v", file, err)
			}
		}
		t.Log("node generated syntax: PASS (dependency load and telemetry are separate live gates)")
	} else {
		t.Log("node build/import: SKIP (node unavailable)")
	}
	if _, err := Instrument(root, Options{Mode: "bootstrap", Undo: true}); err != nil {
		t.Fatal("node undo:", err)
	}
	assertSnapshotFiles(t, root, original)
}

func verifyPythonGeneratedProject(t *testing.T) {
	t.Helper()
	root := t.TempDir()
	original := map[string][]byte{"requirements.txt": []byte("fastapi==0.115.0\n"), "main.py": []byte("print('ok')\n")}
	for name, data := range original {
		mustWrite(t, filepath.Join(root, name), string(data))
	}
	if _, err := Instrument(root, Options{Mode: "bootstrap"}); err != nil {
		t.Fatal("python apply:", err)
	}
	first := snapshotTree(t, root)
	if _, err := Instrument(root, Options{Mode: "bootstrap"}); err != nil {
		t.Fatal("python second apply:", err)
	}
	if !sameSnapshot(first, snapshotTree(t, root)) {
		t.Fatal("python second apply changed generated project")
	}
	if _, err := exec.LookPath("python"); err == nil {
		if err := runIn(root, "python", "-B", "-c", "import pathlib; [compile(p.read_text(), str(p), 'exec') for p in pathlib.Path('.').rglob('*.py')]"); err != nil {
			t.Fatalf("python generated syntax failed: %v", err)
		}
		t.Log("python generated syntax: PASS (dependency import and telemetry are separate live gates)")
	} else {
		t.Log("python build/import: SKIP (python unavailable)")
	}
	if _, err := Instrument(root, Options{Mode: "bootstrap", Undo: true}); err != nil {
		t.Fatal("python undo:", err)
	}
	assertSnapshotFiles(t, root, original)
}

func verifyGoGeneratedProject(t *testing.T) {
	t.Helper()
	root := t.TempDir()
	original := map[string][]byte{"go.mod": []byte("module example.com/service\n\ngo 1.22\n"), "main.go": []byte("package main\nfunc main() {}\n")}
	for name, data := range original {
		mustWrite(t, filepath.Join(root, name), string(data))
	}
	if _, err := Instrument(root, Options{Mode: "bootstrap"}); err != nil {
		t.Fatal("go apply:", err)
	}
	first := snapshotTree(t, root)
	if _, err := Instrument(root, Options{Mode: "bootstrap"}); err != nil {
		t.Fatal("go second apply:", err)
	}
	if !sameSnapshot(first, snapshotTree(t, root)) {
		t.Fatal("go second apply changed generated project")
	}
	if _, err := exec.LookPath("go"); err == nil {
		buildRoot := t.TempDir()
		copyGeneratedTree(t, root, buildRoot)
		if err := runIn(buildRoot, "go", "mod", "tidy"); err != nil {
			t.Fatalf("go generated dependency resolution failed: %v", err)
		}
		if err := runIn(buildRoot, "go", "test", "./..."); err != nil {
			t.Fatalf("go generated build failed: %v", err)
		}
		t.Log("go generated dependency resolution/build: PASS (telemetry is a separate live gate)")
	} else {
		t.Log("go build/import: SKIP (go unavailable)")
	}
	if _, err := Instrument(root, Options{Mode: "bootstrap", Undo: true}); err != nil {
		t.Fatal("go undo:", err)
	}
	assertSnapshotFiles(t, root, original)
}

func runIn(dir, name string, args ...string) error {
	c := exec.Command(name, args...)
	c.Dir = dir
	c.Env = append(os.Environ(), "GOTOOLCHAIN=local")
	out, err := c.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
func toolVersion(name string, args ...string) string {
	c, err := exec.LookPath(name)
	if err != nil {
		return "unavailable"
	}
	versionArgs := args
	if name == "go" {
		versionArgs = []string{"version"}
	}
	out, err := exec.Command(c, versionArgs...).CombinedOutput()
	if err != nil {
		return "error"
	}
	return strings.TrimSpace(string(out))
}
func snapshotTree(t *testing.T, root string) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	filepath.Walk(root, func(p string, i os.FileInfo, e error) error {
		if e == nil && i.Mode().IsRegular() {
			b, _ := os.ReadFile(p)
			out[filepath.ToSlash(strings.TrimPrefix(p, root+string(os.PathSeparator)))] = b
		}
		return nil
	})
	return out
}
func sameSnapshot(a, b map[string][]byte) bool { return fmt.Sprint(a) == fmt.Sprint(b) }
func copyGeneratedTree(t *testing.T, source, destination string) {
	t.Helper()
	err := filepath.Walk(source, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if relative == ".extent" || strings.HasPrefix(relative, ".extent"+string(os.PathSeparator)) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		target := filepath.Join(destination, relative)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode().Perm())
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode().Perm())
	})
	if err != nil {
		t.Fatal(err)
	}
}
func assertSnapshotFiles(t *testing.T, root string, want map[string][]byte) {
	t.Helper()
	got := snapshotTree(t, root)
	for n, b := range want {
		if !bytes.Equal(got[n], b) {
			t.Fatalf("%s bytes not restored", n)
		}
	}
	if len(got) != len(want) {
		left := make([]string, 0, len(got))
		for n := range got {
			if _, ok := want[n]; !ok {
				left = append(left, n)
			}
		}
		t.Errorf("undo left generated files: got %d original %d extras=%v", len(got), len(want), left)
	}
}
