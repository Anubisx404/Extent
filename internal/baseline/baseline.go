package baseline

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Anubisx404/Extent/internal/fileops"
)

type Snapshot struct {
	Version      int                    `json:"version"`
	CapturedAt   time.Time              `json:"capturedAt"`
	ServiceName  string                 `json:"serviceName"`
	Measurements map[string]Measurement `json:"measurements"`
	LoadProfile  *LoadProfile           `json:"loadProfile,omitempty"`
}

type LoadProfile struct {
	DurationSeconds float64 `json:"durationSeconds,omitempty"`
	Concurrency     int     `json:"concurrency,omitempty"`
	ThroughputRPS   float64 `json:"throughputRps,omitempty"`
	TotalRequests   int     `json:"totalRequests,omitempty"`
	ErrorRate       float64 `json:"errorRate,omitempty"`
	P99LatencyMS    float64 `json:"p99LatencyMs,omitempty"`
}

const CurrentVersion = 1

type Measurement struct {
	Status string  `json:"status"`
	Value  float64 `json:"value,omitempty"`
	Unit   string  `json:"unit,omitempty"`
}

type Comparison struct {
	Before         Snapshot         `json:"before"`
	After          Snapshot         `json:"after"`
	Deltas         map[string]Delta `json:"deltas"`
	Summary        string           `json:"summary"`
	LoadRegression bool             `json:"loadRegression,omitempty"`
	LoadSummary    string           `json:"loadSummary,omitempty"`
}

type Delta struct {
	Before     Measurement `json:"before"`
	After      Measurement `json:"after"`
	Comparable bool        `json:"comparable"`
	Change     float64     `json:"change,omitempty"`
}

func Save(root string, snapshot Snapshot) error {
	if snapshot.Version == 0 {
		snapshot.Version = CurrentVersion
	}
	if snapshot.CapturedAt.IsZero() {
		snapshot.CapturedAt = time.Now().UTC()
	}
	if err := validate(snapshot); err != nil {
		return err
	}
	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(root, ".extent", "baseline.json")
	action := fileops.Create
	if _, statErr := os.Stat(path); statErr == nil {
		action = fileops.Update
	} else if !os.IsNotExist(statErr) {
		return statErr
	}
	transaction, err := fileops.NewPlan(root, "baseline", []fileops.Step{{Path: filepath.ToSlash(filepath.Join(".extent", "baseline.json")), Action: action, Data: append(data, '\n'), Mode: 0644}})
	if err != nil {
		return err
	}
	_, err = fileops.Apply(transaction)
	return err
}

func Load(path string) (Snapshot, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Snapshot{}, err
	}
	var snapshot Snapshot
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&snapshot); err != nil {
		return Snapshot{}, err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return Snapshot{}, fmt.Errorf("trailing JSON data")
	}
	if err := validate(snapshot); err != nil {
		return Snapshot{}, err
	}
	return snapshot, nil
}

func validate(s Snapshot) error {
	if s.Version != CurrentVersion {
		if s.Version == 0 {
			return fmt.Errorf("baseline version missing; regenerate the baseline with this Extent version")
		}
		return fmt.Errorf("unsupported baseline version %d", s.Version)
	}
	if strings.TrimSpace(s.ServiceName) == "" || s.CapturedAt.IsZero() || len(s.Measurements) == 0 {
		return fmt.Errorf("invalid baseline snapshot")
	}
	if s.LoadProfile != nil {
		if s.LoadProfile.Concurrency < 0 || s.LoadProfile.DurationSeconds < 0 {
			return fmt.Errorf("invalid baseline load profile")
		}
	}
	measured := 0
	for name, measurement := range s.Measurements {
		if strings.TrimSpace(name) == "" || strings.TrimSpace(measurement.Unit) == "" {
			return fmt.Errorf("invalid baseline measurement")
		}
		switch measurement.Status {
		case "measured":
			measured++
		case "missing", "unavailable", "unknown":
		default:
			return fmt.Errorf("invalid baseline measurement status %q", measurement.Status)
		}
	}
	if measured == 0 {
		return fmt.Errorf("baseline has no measured evidence")
	}
	return nil
}

func Compare(before, after Snapshot) Comparison {
	comparison := Comparison{Before: before, After: after, Deltas: map[string]Delta{}}
	names := map[string]struct{}{}
	for name := range before.Measurements {
		names[name] = struct{}{}
	}
	for name := range after.Measurements {
		names[name] = struct{}{}
	}
	comparable := 0
	for name := range names {
		left, leftOK := before.Measurements[name]
		right, rightOK := after.Measurements[name]
		delta := Delta{Before: left, After: right}
		if leftOK && rightOK && left.Status == "measured" && right.Status == "measured" && left.Unit == right.Unit {
			delta.Comparable = true
			delta.Change = right.Value - left.Value
			comparable++
		}
		comparison.Deltas[name] = delta
	}
	comparison.Summary = fmt.Sprintf("Before vs after: %d comparable measured dimension(s); unavailable or unknown dimensions were excluded.", comparable)
	if before.LoadProfile != nil && after.LoadProfile != nil {
		if after.LoadProfile.P99LatencyMS > before.LoadProfile.P99LatencyMS*1.2 || after.LoadProfile.ErrorRate > before.LoadProfile.ErrorRate+0.05 {
			comparison.LoadRegression = true
		}
		status := "stable"
		if comparison.LoadRegression {
			status = "regression detected"
		}
		comparison.LoadSummary = fmt.Sprintf("Load profile (%s): throughput %.1f -> %.1f RPS, p99 latency %.1f -> %.1f ms, error rate %.2f%% -> %.2f%%",
			status,
			before.LoadProfile.ThroughputRPS, after.LoadProfile.ThroughputRPS,
			before.LoadProfile.P99LatencyMS, after.LoadProfile.P99LatencyMS,
			before.LoadProfile.ErrorRate*100, after.LoadProfile.ErrorRate*100,
		)
		comparison.Summary = fmt.Sprintf("%s Load profile: %s", comparison.Summary, comparison.LoadSummary)
	}
	return comparison
}
