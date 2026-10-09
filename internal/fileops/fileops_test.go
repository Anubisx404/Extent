package fileops

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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

func TestSymlinkedParentOutsideRootIsRefusedForEveryAction(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	victim := filepath.Join(outside, "dashboard.json")
	if err := os.WriteFile(victim, []byte("external"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "grafana")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	cases := []Step{
		{Path: filepath.Join("grafana", "dashboard.json"), Action: Update, Data: []byte("pwned")},
		{Path: filepath.Join("grafana", "dashboard.json"), Action: Delete},
		{Path: filepath.Join("grafana", "new.json"), Action: Create, Data: []byte("pwned")},
		{Path: filepath.Join("grafana", "nested", "new.json"), Action: Create, Data: []byte("pwned")},
	}
	for _, step := range cases {
		plan, err := NewPlan(root, "x", []Step{step})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Apply(plan); !errors.Is(err, ErrUnsafePath) {
			t.Fatalf("%s %s: error = %v, want ErrUnsafePath", step.Action, step.Path, err)
		}
	}
	if got, _ := os.ReadFile(victim); string(got) != "external" {
		t.Fatalf("file outside root changed to %q", got)
	}
	if _, err := os.Stat(filepath.Join(outside, "new.json")); !os.IsNotExist(err) {
		t.Fatal("created file outside root through symlinked parent")
	}
	if _, err := os.Stat(filepath.Join(outside, "nested")); !os.IsNotExist(err) {
		t.Fatal("created directory outside root through symlinked parent")
	}
}

func TestUnsafeStepPathsAreRefused(t *testing.T) {
	root := t.TempDir()
	bad := []string{
		"../x",
		"a/../../x",
		"a/../b/../../c",
		"/abs",
		`\abs`,
		"NUL.txt",
		"nul",
		"dir/CON.json",
		"aux.tar.gz",
		"PRN",
		"COM1",
		"com9.log",
		"LPT1.txt",
		"lpt9",
		`dir\..\escape`,
	}
	for _, path := range bad {
		if _, err := NewPlan(root, "x", []Step{{Path: path, Action: Create, Data: []byte("x")}}); !errors.Is(err, ErrUnsafePath) {
			t.Fatalf("NewPlan(%q) error = %v, want ErrUnsafePath", path, err)
		}
	}
	// Names that merely contain a reserved word are fine.
	for _, path := range []string{"console.txt", "nullable.go", "COM10.txt", "lpt.txt", "dir/aux-data.json"} {
		if _, err := NewPlan(root, "x", []Step{{Path: path, Action: Create, Data: []byte("x")}}); err != nil {
			t.Fatalf("NewPlan(%q) rejected: %v", path, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, ".extent")); !os.IsNotExist(err) {
		t.Fatal("refused plan touched the project")
	}
}

func TestSafeTargetRejectsReservedNameInRecordedPath(t *testing.T) {
	root := t.TempDir()
	if _, err := safeTarget(root, "NUL.txt"); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("safeTarget(NUL.txt) = %v", err)
	}
}

// TestV1ManifestWithoutVersionFieldUndoes hand-writes a state manifest in the
// shape v1.0.x wrote: the same operation and entry layout, no version field.
func TestV1ManifestWithoutVersionFieldUndoes(t *testing.T) {
	root := t.TempDir()
	original := []byte("console.log('start')\n")
	instrumented := []byte("import './extent.js';\nconsole.log('start')\n")
	if err := os.WriteFile(filepath.Join(root, "server.js"), instrumented, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "extent.js"), []byte("bootstrap"), 0o644); err != nil {
		t.Fatal(err)
	}
	backupDir := filepath.Join(root, ".extent", "backups", "v1op")
	if err := os.MkdirAll(backupDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backupDir, "server.js"), original, 0o600); err != nil {
		t.Fatal(err)
	}
	manifest := `{
  "operations": [
    {
      "id": "v1op",
      "kind": "instrument/bootstrap",
      "entries": [
        {
          "path": "server.js",
          "action": "update",
          "before": "` + state.Hash(original) + `",
          "after": "` + state.Hash(instrumented) + `",
          "mode": 420,
          "existed": true,
          "backup": ".extent/backups/v1op/server.js"
        },
        {
          "path": "extent.js",
          "action": "create",
          "after": "` + state.Hash([]byte("bootstrap")) + `",
          "mode": 420
        }
      ]
    }
  ]
}
`
	if err := os.WriteFile(filepath.Join(root, ".extent", "state.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := state.Load(root)
	if err != nil {
		t.Fatalf("load v1.0.x manifest: %v", err)
	}
	if loaded.Version != state.CurrentVersion || len(loaded.Operations) != 1 {
		t.Fatalf("loaded = %#v", loaded)
	}
	if err := Undo(root); err != nil {
		t.Fatalf("undo v1.0.x manifest: %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "server.js")); !bytes.Equal(got, original) {
		t.Fatalf("server.js = %q, want %q", got, original)
	}
	if _, err := os.Stat(filepath.Join(root, "extent.js")); !os.IsNotExist(err) {
		t.Fatalf("created file survived undo: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".extent", "state.json")); !os.IsNotExist(err) {
		t.Fatal("state survived final undo")
	}
}

// snapshotFiles returns every regular file under root (excluding nothing), keyed
// by slash-separated relative path, so a comparison catches stray leftovers.
func snapshotFiles(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			rel, _ := filepath.Rel(root, path)
			data, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			out[filepath.ToSlash(rel)] = string(data)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// TestInjectedWriteFailureAtEveryWriteRollsBack fails each mutating operation
// of a mixed plan in turn (backup writes, target writes, deletes and the
// manifest save). After every failure the tree must match the pre-apply tree
// byte for byte, and a retry must succeed and undo to the original.
func TestInjectedWriteFailureAtEveryWriteRollsBack(t *testing.T) {
	setup := func(t *testing.T) string {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, "a-existing.txt"), []byte("alpha\r\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		// c-delete.txt must be Extent-owned before a Delete is allowed, so it
		// is created through a real apply.
		seed, err := NewPlan(root, "seed", []Step{{Path: "c-delete.txt", Action: Create, Data: []byte("gamma")}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Apply(seed); err != nil {
			t.Fatal(err)
		}
		return root
	}
	plan := func(t *testing.T, root string) Plan {
		p, err := NewPlan(root, "mixed", []Step{
			{Path: "a-existing.txt", Action: Update, Data: []byte("updated")},
			{Path: "b-created.txt", Action: Create, Data: []byte("created")},
			{Path: "d/nested/e.txt", Action: Create, Data: []byte("nested")},
			{Path: "c-delete.txt", Action: Delete},
		})
		if err != nil {
			t.Fatal(err)
		}
		return p
	}

	reached := false
	for n := 1; n < 64 && !reached; n++ {
		root := setup(t)
		before := snapshotFiles(t, root)
		_, err := ApplyWithOptions(plan(t, root), Options{FailWriteAt: n})
		if err == nil {
			reached = true
			// Past the last write: the plan must have applied cleanly.
			if _, statErr := os.Stat(filepath.Join(root, "c-delete.txt")); !os.IsNotExist(statErr) {
				t.Fatal("clean apply did not delete c-delete.txt")
			}
			if err := UndoKind(root, "mixed"); err != nil {
				t.Fatalf("undo after clean apply: %v", err)
			}
			if got := snapshotFiles(t, root); fmt.Sprint(got) != fmt.Sprint(before) {
				t.Fatalf("undo after clean apply did not restore tree: %v want %v", got, before)
			}
			continue
		}
		if !strings.Contains(err.Error(), "injected write failure") {
			t.Fatalf("write %d: unexpected error %v", n, err)
		}
		if after := snapshotFiles(t, root); fmt.Sprint(after) != fmt.Sprint(before) {
			t.Fatalf("write %d: tree not restored\nafter:  %v\nbefore: %v", n, after, before)
		}
		if _, statErr := os.Stat(filepath.Join(root, ".extent", "state.json")); !os.IsNotExist(statErr) {
			// The seed operation has its own manifest; only the mixed operation must be absent.
			manifest, loadErr := state.Load(root)
			if loadErr != nil {
				t.Fatalf("write %d: load manifest: %v", n, loadErr)
			}
			for _, op := range manifest.Operations {
				if op.Kind == "mixed" {
					t.Fatalf("write %d: failed operation left a manifest entry", n)
				}
			}
		}
		if entries, _ := filepath.Glob(filepath.Join(root, ".extent", "backups", "*")); len(entries) != 0 {
			t.Fatalf("write %d: backup directories left behind: %v", n, entries)
		}
		// Retry converges and undoes to the original tree.
		if _, err := Apply(plan(t, root)); err != nil {
			t.Fatalf("write %d: retry failed: %v", n, err)
		}
		if err := UndoKind(root, "mixed"); err != nil {
			t.Fatalf("write %d: undo after retry: %v", n, err)
		}
		if got := snapshotFiles(t, root); fmt.Sprint(got) != fmt.Sprint(before) {
			t.Fatalf("write %d: undo after retry did not restore tree: %v want %v", n, got, before)
		}
	}
	if !reached {
		t.Fatal("never reached a clean apply; injection range too small")
	}
}
