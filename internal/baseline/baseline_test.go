package baseline

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Anubisx404/Extent/internal/fileops"
	"github.com/Anubisx404/Extent/internal/state"
)

func TestSaveLoadAndCompareBaseline(t *testing.T) {
	root := t.TempDir()
	before := Snapshot{ServiceName: "checkout", Measurements: map[string]Measurement{"traceCount": {Status: "measured", Value: 1, Unit: "count"}, "logCorrelation": {Status: "unknown", Unit: "ratio"}}}
	after := Snapshot{ServiceName: "checkout", Measurements: map[string]Measurement{"traceCount": {Status: "measured", Value: 14, Unit: "count"}, "logCorrelation": {Status: "measured", Value: 0.85, Unit: "ratio"}}}

	if err := Save(root, before); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(filepath.Join(root, ".extent", "baseline.json"))
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ServiceName != "checkout" {
		t.Fatalf("expected loaded service name, got %#v", loaded)
	}
	if loaded.Version != CurrentVersion {
		t.Fatalf("baseline version = %d", loaded.Version)
	}
	comparison := Compare(loaded, after)
	if delta := comparison.Deltas["traceCount"]; !delta.Comparable || delta.Change != 13 {
		t.Fatalf("expected measured trace delta, got %#v", comparison)
	}
	if comparison.Deltas["logCorrelation"].Comparable {
		t.Fatalf("unknown baseline value became comparable: %#v", comparison)
	}
	if comparison.Summary == "" {
		t.Fatal("expected comparison summary")
	}
}

func TestSaveIsIdempotentAndUndoRestoresExistingBytes(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".extent"), 0o700); err != nil {
		t.Fatal(err)
	}
	original := []byte("legacy baseline bytes\n")
	path := filepath.Join(root, ".extent", "baseline.json")
	if err := os.WriteFile(path, original, 0o640); err != nil {
		t.Fatal(err)
	}
	snapshot := Snapshot{CapturedAt: time.Unix(1_700_000_000, 0).UTC(), ServiceName: "checkout", Measurements: map[string]Measurement{"traceCount": {Status: "measured", Value: 2, Unit: "count"}}}
	if err := Save(root, snapshot); err != nil {
		t.Fatal(err)
	}
	if err := Save(root, snapshot); err != nil {
		t.Fatal(err)
	}
	manifest, err := state.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Operations) != 1 {
		t.Fatalf("idempotent baseline recorded %d operations", len(manifest.Operations))
	}
	if err := fileops.UndoKind(root, "baseline"); err != nil {
		t.Fatal(err)
	}
	restored, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(restored, original) {
		t.Fatalf("restored baseline = %q", restored)
	}
}

func TestLoadRejectsUnknownTrailingAndInvalidSnapshots(t *testing.T) {
	for name, content := range map[string]string{
		"unknown":  `{"capturedAt":"2025-01-01T00:00:00Z","serviceName":"x","extra":true}`,
		"trailing": `{"capturedAt":"2025-01-01T00:00:00Z","serviceName":"x"} {}`,
		"invalid":  `{"capturedAt":"2025-01-01T00:00:00Z","serviceName":" ","traceCoverage":0.5}`,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "baseline.json")
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(path); err == nil {
				t.Fatalf("accepted %s baseline", name)
			}
		})
	}
}

func TestSaveRejectsInvalidSnapshotWithoutMutation(t *testing.T) {
	root := t.TempDir()
	if err := Save(root, Snapshot{ServiceName: " ", Measurements: map[string]Measurement{"x": {Status: "measured", Unit: "count"}}}); err == nil {
		t.Fatal("invalid snapshot accepted")
	}
	if _, err := os.Stat(filepath.Join(root, ".extent")); !os.IsNotExist(err) {
		t.Fatalf("invalid save mutated project: %v", err)
	}
}
