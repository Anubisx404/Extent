package baseline

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type Snapshot struct {
	CapturedAt       time.Time `json:"capturedAt"`
	ServiceName      string    `json:"serviceName"`
	TraceCoverage    float64   `json:"traceCoverage"`
	LogCorrelation   float64   `json:"logCorrelation"`
	DBSpanCount      int       `json:"dbSpanCount"`
	SlowDBOperations int       `json:"slowDbOperations"`
	NPlusOneFindings int       `json:"nPlusOneFindings"`
	HealthScore      int       `json:"healthScore"`
}

type Comparison struct {
	Before               Snapshot `json:"before"`
	After                Snapshot `json:"after"`
	TraceCoverageDelta   float64  `json:"traceCoverageDelta"`
	LogCorrelationDelta  float64  `json:"logCorrelationDelta"`
	DBSpanDelta          int      `json:"dbSpanDelta"`
	SlowDBOperationDelta int      `json:"slowDbOperationDelta"`
	NPlusOneDelta        int      `json:"nPlusOneDelta"`
	HealthScoreDelta     int      `json:"healthScoreDelta"`
	Summary              string   `json:"summary"`
}

func Save(root string, snapshot Snapshot) error {
	if snapshot.CapturedAt.IsZero() {
		snapshot.CapturedAt = time.Now().UTC()
	}
	path := filepath.Join(root, ".extent", "baseline.json")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0644)
}

func Load(path string) (Snapshot, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Snapshot{}, err
	}
	var snapshot Snapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return Snapshot{}, err
	}
	return snapshot, nil
}

func Compare(before, after Snapshot) Comparison {
	comparison := Comparison{
		Before:               before,
		After:                after,
		TraceCoverageDelta:   after.TraceCoverage - before.TraceCoverage,
		LogCorrelationDelta:  after.LogCorrelation - before.LogCorrelation,
		DBSpanDelta:          after.DBSpanCount - before.DBSpanCount,
		SlowDBOperationDelta: after.SlowDBOperations - before.SlowDBOperations,
		NPlusOneDelta:        after.NPlusOneFindings - before.NPlusOneFindings,
		HealthScoreDelta:     after.HealthScore - before.HealthScore,
	}
	comparison.Summary = fmt.Sprintf("Before vs after: trace coverage %+0.0f%%, log correlation %+0.0f%%, DB spans %+d, slow DB findings %+d, N+1 findings %+d.",
		comparison.TraceCoverageDelta*100,
		comparison.LogCorrelationDelta*100,
		comparison.DBSpanDelta,
		comparison.SlowDBOperationDelta,
		comparison.NPlusOneDelta,
	)
	return comparison
}
