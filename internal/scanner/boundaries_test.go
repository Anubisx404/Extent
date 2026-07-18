package scanner

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScanSkipsAllDefaultExcludedTrees(t *testing.T) {
	root := t.TempDir()
	for _, dir := range DefaultExcludedDirs() {
		if err := os.MkdirAll(filepath.Join(root, dir), 0755); err != nil {
			t.Fatal(err)
		}
		mustWrite(t, filepath.Join(root, dir, "package.json"), `{"dependencies":{"express":"1"}}`)
	}
	result, err := Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Runtimes)+len(result.Frameworks)+len(result.Signals) != 0 {
		t.Fatalf("excluded files affected scan: %#v", result)
	}
}

func TestDefaultExcludedDirectoriesAreDeterministic(t *testing.T) {
	first, second := DefaultExcludedDirs(), DefaultExcludedDirs()
	if len(first) != len(second) {
		t.Fatal("defaults changed")
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("nondeterministic defaults: %#v %#v", first, second)
		}
	}
}

func TestScanReportsMalformedRecognizedManifest(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "package.json"), `{not-json`)
	result, err := Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Runtimes) != 1 || result.Runtimes[0] != "node" {
		t.Fatalf("runtime detection should survive a malformed manifest: %#v", result.Runtimes)
	}
	if len(result.Warnings) != 1 || result.Warnings[0].Path != "package.json" {
		t.Fatalf("warnings = %#v", result.Warnings)
	}
}
