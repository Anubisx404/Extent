package instrumenter

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Anubisx404/Extent/internal/fileops"
)

type faultFixture struct {
	name  string
	files map[string]string
	// edits are generated or mutated paths a user may change after apply.
	edits []string
}

func nodeFaultFixture() faultFixture {
	return faultFixture{
		name: "node",
		files: map[string]string{
			"package.json": `{"name":"api","dependencies":{"express":"4.21.2"}}` + "\n",
			"server.js":    "console.log('start')\n",
		},
		edits: []string{"package.json", "server.js", "extent.instrumentation.js"},
	}
}

func nodeESMFaultFixture() faultFixture {
	return faultFixture{
		name: "node-esm",
		files: map[string]string{
			"package.json": `{"type":"module","dependencies":{"express":"^4.18.0"}}` + "\n",
			"index.js":     "import express from \"express\";\nconsole.log(express);\n",
		},
		edits: []string{"package.json", "index.js", "extent.instrumentation.mjs"},
	}
}

func goFaultFixture() faultFixture {
	return faultFixture{
		name: "go",
		files: map[string]string{
			"go.mod":  "module example.com/service\n\ngo 1.22\n",
			"main.go": "package main\n\nfunc main() {}\n",
		},
		edits: []string{"go.mod", "main.go", "internal/observability/otel.go"},
	}
}

func (f faultFixture) build(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for rel, content := range f.files {
		mustWrite(t, filepath.Join(root, filepath.FromSlash(rel)), content)
	}
	return root
}

// projectSnapshot returns every regular file under root except the Extent
// transaction metadata, keyed by slash-separated relative path.
func projectSnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		if rel == ".extent" || strings.HasPrefix(rel, ".extent"+string(filepath.Separator)) {
			return nil
		}
		if d.Type().IsRegular() {
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

// fullSnapshot is projectSnapshot plus any transaction metadata, so a restore
// check catches leftover state, backups or locks.
func fullSnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type().IsRegular() {
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

func equalSnapshots(a, b map[string]string) bool { return fmt.Sprint(a) == fmt.Sprint(b) }

// TestBootstrapWriteFailureAtEveryWriteRestoresOrConverges injects a failure
// at every mutating filesystem operation of a bootstrap instrument. After each
// failure the tree must be byte-for-byte the original, or a clean rerun must
// converge to the same project as an uninterrupted run. Undo must always
// restore the original tree exactly.
func TestBootstrapWriteFailureAtEveryWriteRestoresOrConverges(t *testing.T) {
	for _, fixture := range []faultFixture{nodeFaultFixture(), nodeESMFaultFixture(), goFaultFixture()} {
		t.Run(fixture.name, func(t *testing.T) {
			reference := fixture.build(t)
			original := fullSnapshot(t, reference)
			if _, err := Instrument(reference, Options{Mode: "bootstrap"}); err != nil {
				t.Fatal(err)
			}
			wantProject := projectSnapshot(t, reference)
			if _, err := Instrument(reference, Options{Mode: "bootstrap", Undo: true}); err != nil {
				t.Fatal(err)
			}
			if got := fullSnapshot(t, reference); !equalSnapshots(got, original) {
				t.Fatalf("reference undo did not restore tree")
			}

			reachedEnd := false
			failures := 0
			for n := 1; n < 128 && !reachedEnd; n++ {
				root := fixture.build(t)
				before := fullSnapshot(t, root)
				_, err := instrument(root, Options{Mode: "bootstrap"}, fileops.Options{FailWriteAt: n})
				if err == nil {
					reachedEnd = true
					if got := projectSnapshot(t, root); !equalSnapshots(got, wantProject) {
						t.Fatalf("write %d: clean run differs from reference", n)
					}
				} else {
					failures++
					if !strings.Contains(err.Error(), "injected write failure") {
						t.Fatalf("write %d: unexpected error %v", n, err)
					}
					if got := fullSnapshot(t, root); !equalSnapshots(got, before) {
						t.Fatalf("write %d: failed instrument did not restore tree\nafter:  %v\nbefore: %v", n, got, before)
					}
					// Rerun converges to the uninterrupted result.
					if _, err := Instrument(root, Options{Mode: "bootstrap"}); err != nil {
						t.Fatalf("write %d: rerun after failure: %v", n, err)
					}
					if got := projectSnapshot(t, root); !equalSnapshots(got, wantProject) {
						t.Fatalf("write %d: rerun did not converge to clean result", n)
					}
				}
				if _, err := Instrument(root, Options{Mode: "bootstrap", Undo: true}); err != nil {
					t.Fatalf("write %d: undo: %v", n, err)
				}
				if got := fullSnapshot(t, root); !equalSnapshots(got, original) {
					t.Fatalf("write %d: undo did not restore original tree byte for byte", n)
				}
			}
			if !reachedEnd {
				t.Fatal("injection range did not reach a clean run")
			}
			if failures < 4 {
				t.Fatalf("only %d injected failures exercised; expected writes across backups, targets, deletes and the manifest", failures)
			}
		})
	}
}

// TestUndoRefusesUserEditsAndPreservesThem edits each generated or mutated
// file (source, manifest, generated config) after apply. Undo must refuse with
// a conflict, leave the user's edit in place, and leave every other file in
// its instrumented state.
func TestUndoRefusesUserEditsAndPreservesThem(t *testing.T) {
	for _, fixture := range []faultFixture{nodeFaultFixture(), nodeESMFaultFixture(), goFaultFixture()} {
		for _, edited := range fixture.edits {
			t.Run(fixture.name+"/"+edited, func(t *testing.T) {
				root := fixture.build(t)
				if _, err := Instrument(root, Options{Mode: "bootstrap"}); err != nil {
					t.Fatal(err)
				}
				instrumented := projectSnapshot(t, root)
				full := filepath.Join(root, filepath.FromSlash(edited))
				if _, ok := instrumented[edited]; !ok {
					t.Fatalf("%s not present after apply", edited)
				}
				userEdit := []byte("// hand-edited by a user\n")
				if err := os.WriteFile(full, append(append([]byte(nil), []byte(instrumented[edited])...), userEdit...), 0o644); err != nil {
					t.Fatal(err)
				}
				edits := projectSnapshot(t, root)

				_, err := Instrument(root, Options{Mode: "bootstrap", Undo: true})
				if !errors.Is(err, fileops.ErrConflict) {
					t.Fatalf("undo over user edit to %s: error = %v, want ErrConflict", edited, err)
				}
				if got := projectSnapshot(t, root); !equalSnapshots(got, edits) {
					t.Fatalf("undo changed files after refusing; user edit to %s was overwritten or partial undo happened", edited)
				}
			})
		}
	}
}
