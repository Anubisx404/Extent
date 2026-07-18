package state

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestSaveLoadRoundTripAndVersionValidation(t *testing.T) {
	root := t.TempDir()
	manifest := Manifest{
		Version: CurrentVersion,
		Operations: []Operation{{
			ID:   "op-1",
			Kind: "instrument",
			Entries: []Entry{{
				Path:   "main.go",
				Action: "update",
				Before: Hash([]byte("before")),
				After:  Hash([]byte("after")),
				Mode:   0o644,
				Backup: ".extent/backups/op-1/main.go",
			}},
		}},
	}
	if err := Save(root, manifest); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Operations) != 1 || loaded.Operations[0].Entries[0].Path != "main.go" {
		t.Fatalf("loaded manifest = %#v", loaded)
	}
	info, err := os.Stat(filepath.Join(root, ".extent", "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("state permissions are too broad: %o", info.Mode().Perm())
	}
}

func TestLoadRejectsCorruptionUnknownVersionAndUnsafeEntries(t *testing.T) {
	tests := []string{
		`not-json`,
		`{"version":2,"operations":[]}`,
		`{"version":1,"operations":[{"id":"op","kind":"x","entries":[{"path":"../escape","action":"create"}]}]}`,
		`{"version":1,"operations":[{"id":"","kind":"x","entries":[]}]}`,
	}
	for _, data := range tests {
		root := t.TempDir()
		dir := filepath.Join(root, ".extent")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(root); err == nil {
			t.Fatalf("accepted state %s", data)
		}
	}
}

func TestSaveDoesNotLeaveTemporaryFiles(t *testing.T) {
	root := t.TempDir()
	manifest := Manifest{Version: CurrentVersion}
	if err := Save(root, manifest); err != nil {
		t.Fatal(err)
	}
	if err := Save(root, manifest); err != nil {
		t.Fatal(err)
	}
	matches, err := filepath.Glob(filepath.Join(root, ".extent", ".state-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("temporary state files remain: %#v", matches)
	}
}

func TestLoadRecoversInterruptedStateReplacement(t *testing.T) {
	root := t.TempDir()
	manifest := Manifest{Version: CurrentVersion, Operations: []Operation{{ID: "op", Kind: "x"}}}
	if err := Save(root, manifest); err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(root, ".extent")
	if err := os.Rename(filepath.Join(directory, "state.json"), filepath.Join(directory, ".state-previous")); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Operations) != 1 || loaded.Operations[0].ID != "op" {
		t.Fatalf("recovered manifest = %#v", loaded)
	}
}

func TestSaveRejectsSymlinkedStateDirectory(t *testing.T) {
	root := t.TempDir()
	external := t.TempDir()
	if err := os.Symlink(external, filepath.Join(root, ".extent")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if err := Save(root, Manifest{Version: CurrentVersion}); err == nil {
		t.Fatal("accepted symlinked state directory")
	}
	if _, err := os.Stat(filepath.Join(external, "state.json")); !os.IsNotExist(err) {
		t.Fatal("state was written through symlinked directory")
	}
}
