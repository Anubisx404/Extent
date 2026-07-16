package baseline

import (
	"path/filepath"
	"testing"
)

func TestSaveLoadAndCompareBaseline(t *testing.T) {
	root := t.TempDir()
	before := Snapshot{ServiceName: "checkout", TraceCoverage: 0.1, LogCorrelation: 0.2, DBSpanCount: 1, SlowDBOperations: 0, NPlusOneFindings: 0}
	after := Snapshot{ServiceName: "checkout", TraceCoverage: 1, LogCorrelation: 0.85, DBSpanCount: 14, SlowDBOperations: 3, NPlusOneFindings: 2}

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
	comparison := Compare(loaded, after)
	if comparison.TraceCoverageDelta <= 0 || comparison.LogCorrelationDelta <= 0 {
		t.Fatalf("expected positive deltas, got %#v", comparison)
	}
	if comparison.Summary == "" {
		t.Fatal("expected comparison summary")
	}
}
