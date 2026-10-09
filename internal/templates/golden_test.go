package templates

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/Anubisx404/Extent/internal/stack"
)

var updateGolden = flag.Bool("update", false, "rewrite golden files under testdata/golden")

const goldenDir = "testdata/golden"

// goldenProfiles are the profile names whose collector output differs.
var goldenProfiles = []string{"minimal", "full", "high-cardinality-safe", "low-resource", "report-heavy"}

// goldenOutputs renders every generated stack file that depends on profile
// selection, for every profile name crossed with HostMetrics and Persistent.
func goldenOutputs() map[string]string {
	out := map[string]string{}
	for _, name := range goldenProfiles {
		out["otel-collector_"+name+".yml"] = collectorYAMLForProfile(name)
		for _, hostMetrics := range []bool{false, true} {
			for _, persistent := range []bool{false, true} {
				cfg := stack.ProfileConfig{Name: stack.Profile(name), HostMetrics: hostMetrics, Persistent: persistent}
				suffix := fmt.Sprintf("%s_hm-%t_persist-%t", name, hostMetrics, persistent)
				out["docker-compose_"+suffix+".yml"] = composeYAMLForProfile(cfg)
				out["prometheus_"+suffix+".yml"] = prometheusYAMLForProfile(cfg)
			}
		}
	}
	return out
}

func TestGoldenOutputs(t *testing.T) {
	outputs := goldenOutputs()
	if *updateGolden {
		if err := os.MkdirAll(goldenDir, 0o755); err != nil {
			t.Fatal(err)
		}
		for name, content := range outputs {
			if err := os.WriteFile(filepath.Join(goldenDir, name), []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		t.Logf("updated %d golden files", len(outputs))
		return
	}
	for name, content := range outputs {
		want, err := os.ReadFile(filepath.Join(goldenDir, name))
		if err != nil {
			t.Fatalf("missing golden %s (run go test ./internal/templates -run TestGoldenOutputs -update): %v", name, err)
		}
		if string(want) != content {
			t.Fatalf("golden %s differs (run with -update after reviewing the change):\n--- want\n%s\n--- got\n%s", name, want, content)
		}
	}
}
