package fileops

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/Anubisx404/Extent/internal/state"
)

func TestPlanDeterministicAndContained(t *testing.T) {
	root := t.TempDir()
	p, err := NewPlan(root, "config", []Step{{Path: "b", Action: Create, Data: []byte("b")}, {Path: "a", Action: Delete}})
	if err != nil {
		t.Fatal(err)
	}
	if p.Steps[0].Path != "a" {
		t.Fatal("steps not sorted")
	}
	if _, err := NewPlan(root, "x", []Step{{Path: "../escape", Action: Create}}); !errors.Is(err, ErrUnsafePath) {
		t.Fatal(err)
	}
	if _, err := NewPlan(root, "x", []Step{{Path: "same", Action: Create}, {Path: "same", Action: Update}}); err == nil {
		t.Fatal("accepted duplicate target")
	}
	if _, err := NewPlan(root, "x", []Step{{Path: "file", Action: Action("bogus")}}); err == nil {
		t.Fatal("accepted invalid action")
	}
}

func TestApplyUndoRestoresExactBytesAndIsIdempotent(t *testing.T) {
	root := t.TempDir()
	original := []byte("original\r\nbytes\x00")
	updatedPath := filepath.Join(root, "a-existing.txt")
	if err := os.WriteFile(updatedPath, original, 0o640); err != nil {
		t.Fatal(err)
	}
	p, err := NewPlan(root, "instrument", []Step{
		{Path: "a-existing.txt", Action: Update, Data: []byte("updated"), Mode: 0o600},
		{Path: "b-created.txt", Action: Create, Data: []byte("created"), Mode: 0o644},
	})
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := Apply(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Operations) != 1 {
		t.Fatalf("operations = %#v", manifest.Operations)
	}
	manifest, err = Apply(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Operations) != 1 {
		t.Fatalf("idempotent apply added history: %#v", manifest.Operations)
	}
	if err := UndoKind(root, "instrument"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(updatedPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, original) {
		t.Fatalf("restored bytes = %q, want %q", got, original)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(updatedPath)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o640 {
			t.Fatalf("restored mode = %o", info.Mode().Perm())
		}
	}
	if _, err := os.Stat(filepath.Join(root, "b-created.txt")); !os.IsNotExist(err) {
		t.Fatal("created file was not removed")
	}
	if _, err := os.Stat(filepath.Join(root, ".extent", "state.json")); !os.IsNotExist(err) {
		t.Fatalf("transaction metadata was not removed after final undo: %v", err)
	}
}

func TestRollbackInjectedFailure(t *testing.T) {
	root := t.TempDir()
	original := []byte("before")
	if err := os.WriteFile(filepath.Join(root, "a-existing"), original, 0o644); err != nil {
		t.Fatal(err)
	}
	p, _ := NewPlan(root, "x", []Step{{Path: "a-existing", Action: Update, Data: []byte("after")}, {Path: "b-created", Action: Create, Data: []byte("b")}})
	_, err := ApplyWithOptions(p, Options{FailAfter: 1})
	if err == nil {
		t.Fatal("expected failure")
	}
	got, readErr := os.ReadFile(filepath.Join(root, "a-existing"))
	if readErr != nil || !bytes.Equal(got, original) {
		t.Fatalf("update rollback failed: %q, %v", got, readErr)
	}
	if _, statErr := os.Stat(filepath.Join(root, "b-created")); !os.IsNotExist(statErr) {
		t.Fatal("create rollback failed")
	}
}

func TestApplyRejectsUnownedCreateAndUndoRejectsModifiedOwnedFile(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "owned.txt")
	if err := os.WriteFile(path, []byte("user"), 0o644); err != nil {
		t.Fatal(err)
	}
	conflict, _ := NewPlan(root, "x", []Step{{Path: "owned.txt", Action: Create, Data: []byte("extent")}})
	if _, err := Apply(conflict); !errors.Is(err, ErrConflict) {
		t.Fatalf("create conflict = %v", err)
	}
	if got, _ := os.ReadFile(path); string(got) != "user" {
		t.Fatalf("unowned file changed to %q", got)
	}

	created, _ := NewPlan(root, "x", []Step{{Path: "created.txt", Action: Create, Data: []byte("extent")}})
	if _, err := Apply(created); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "created.txt"), []byte("user edit"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Undo(root); !errors.Is(err, ErrConflict) {
		t.Fatalf("modified undo = %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "created.txt")); string(got) != "user edit" {
		t.Fatalf("modified owned file changed to %q", got)
	}
}

func TestApplyRejectsLockAndSymlinkParent(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".extent"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".extent", "lock"), []byte("busy"), 0o600); err != nil {
		t.Fatal(err)
	}
	p, _ := NewPlan(root, "x", []Step{{Path: "a", Action: Create, Data: []byte("a")}})
	if _, err := Apply(p); !errors.Is(err, ErrLocked) {
		t.Fatalf("lock error = %v", err)
	}
	if err := os.Remove(filepath.Join(root, ".extent", "lock")); err != nil {
		t.Fatal(err)
	}

	target := t.TempDir()
	link := filepath.Join(root, "linked")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	linkedPlan, _ := NewPlan(root, "x", []Step{{Path: filepath.Join("linked", "escape.txt"), Action: Create, Data: []byte("x")}})
	if _, err := Apply(linkedPlan); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("symlink parent error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(target, "escape.txt")); !os.IsNotExist(err) {
		t.Fatal("wrote through symlink parent")
	}
}

func TestUndoRejectsMissingOrCorruptState(t *testing.T) {
	root := t.TempDir()
	if err := Undo(root); err == nil {
		t.Fatal("undo accepted missing state")
	}
	if err := os.MkdirAll(filepath.Join(root, ".extent"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".extent", "state.json"), []byte("bad"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Undo(root); err == nil {
		t.Fatal("undo accepted corrupt state")
	}
}

func TestDeleteRequiresOwnershipAndCanBeUndone(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "user.txt"), []byte("user"), 0o644); err != nil {
		t.Fatal(err)
	}
	deleteUser, _ := NewPlan(root, "x", []Step{{Path: "user.txt", Action: Delete}})
	if _, err := Apply(deleteUser); !errors.Is(err, ErrConflict) {
		t.Fatalf("unowned delete = %v", err)
	}

	create, _ := NewPlan(root, "create", []Step{{Path: "owned.txt", Action: Create, Data: []byte("owned")}})
	if _, err := Apply(create); err != nil {
		t.Fatal(err)
	}
	deleteOwned, _ := NewPlan(root, "delete", []Step{{Path: "owned.txt", Action: Delete}})
	if _, err := Apply(deleteOwned); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "owned.txt")); !os.IsNotExist(err) {
		t.Fatal("owned file not deleted")
	}
	if err := UndoKind(root, "delete"); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(root, "owned.txt")); err != nil || string(got) != "owned" {
		t.Fatalf("deleted file not restored: %q, %v", got, err)
	}
	manifest, err := state.Load(root)
	if err != nil || len(manifest.Operations) != 1 {
		t.Fatalf("remaining state = %#v, %v", manifest, err)
	}
}

func TestApplyRejectsSymlinkedExtentStateDirectory(t *testing.T) {
	root := t.TempDir()
	external := t.TempDir()
	if err := os.Symlink(external, filepath.Join(root, ".extent")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	plan, err := NewPlan(root, "x", []Step{{Path: "created.txt", Action: Create, Data: []byte("x")}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(plan); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("symlinked state directory error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(external, "lock")); !os.IsNotExist(err) {
		t.Fatal("lock was written through symlinked state directory")
	}
}
