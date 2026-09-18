package baseline

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
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

func TestCompareLoadProfilesAndDetectRegression(t *testing.T) {
	before := Snapshot{
		ServiceName: "checkout",
		Measurements: map[string]Measurement{
			"traceCount": {Status: "measured", Value: 10, Unit: "count"},
		},
		LoadProfile: &LoadProfile{
			DurationSeconds: 30,
			Concurrency:     5,
			ThroughputRPS:   120.5,
			TotalRequests:   3615,
			ErrorRate:       0.01,
			P99LatencyMS:    45.0,
		},
	}
	after := Snapshot{
		ServiceName: "checkout",
		Measurements: map[string]Measurement{
			"traceCount": {Status: "measured", Value: 10, Unit: "count"},
		},
		LoadProfile: &LoadProfile{
			DurationSeconds: 30,
			Concurrency:     5,
			ThroughputRPS:   80.0,
			TotalRequests:   2400,
			ErrorRate:       0.08,
			P99LatencyMS:    95.0,
		},
	}

	comparison := Compare(before, after)
	if !comparison.LoadRegression {
		t.Fatalf("expected load regression to be true, got false")
	}
	summaryLower := strings.ToLower(comparison.LoadSummary)
	if !strings.Contains(summaryLower, "regression") || !strings.Contains(summaryLower, "latency") || !strings.Contains(summaryLower, "throughput") || !strings.Contains(summaryLower, "error") {
		t.Fatalf("expected load summary to mention latency, throughput, and error regression, got %q", comparison.LoadSummary)
	}
	if !strings.Contains(strings.ToLower(comparison.Summary), "regression") {
		t.Fatalf("expected comparison.Summary to mention regression, got %q", comparison.Summary)
	}

	root := t.TempDir()
	if err := Save(root, before); err != nil {
		t.Fatalf("failed to save baseline with load profile: %v", err)
	}
	loaded, err := Load(filepath.Join(root, ".extent", "baseline.json"))
	if err != nil {
		t.Fatalf("failed to load baseline with load profile: %v", err)
	}
	if loaded.LoadProfile == nil {
		t.Fatal("expected loaded.LoadProfile to not be nil")
	}
	if loaded.LoadProfile.Concurrency != 5 || loaded.LoadProfile.ThroughputRPS != 120.5 || loaded.LoadProfile.P99LatencyMS != 45.0 {
		t.Fatalf("loaded load profile mismatch: %#v", loaded.LoadProfile)
	}

	invalidDuration := before
	invalidDuration.LoadProfile = &LoadProfile{DurationSeconds: -1, Concurrency: 1}
	if err := validate(invalidDuration); err == nil {
		t.Fatal("expected error for negative duration seconds")
	}

	invalidConcurrency := before
	invalidConcurrency.LoadProfile = &LoadProfile{DurationSeconds: 10, Concurrency: -1}
	if err := validate(invalidConcurrency); err == nil {
		t.Fatal("expected error for negative concurrency")
	}
}
