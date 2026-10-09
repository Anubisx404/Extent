package main

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var updateGolden = flag.Bool("update", false, "rewrite golden files under testdata/golden")

const goldenDir = "testdata/golden"

// checkGolden compares got with testdata/golden/<name>.golden. Run
// `go test ./cmd/extent -run Golden -update` to regenerate the files after an
// intentional change to help text, then review the diff.
func checkGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join(goldenDir, name+".golden")
	if *updateGolden {
		if err := os.MkdirAll(goldenDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("missing golden file %s (run with -update): %v", path, err)
	}
	if string(want) != got {
		t.Fatalf("output differs from %s (run with -update if the change is intended)\n--- want\n%s\n--- got\n%s", path, want, got)
	}
}

func TestGoldenHelpCommand(t *testing.T) {
	out, errOut, err := captureStreams(t, func() error { return run([]string{"help"}) })
	if err != nil {
		t.Fatalf("help: %v", err)
	}
	if errOut != "" {
		t.Fatalf("help wrote to stderr: %s", errOut)
	}
	checkGolden(t, "help", out)
}

// TestGoldenSubcommandHelp captures what each command prints for -h. The flag
// package writes its usage to stderr, so both streams are part of the golden.
func TestGoldenSubcommandHelp(t *testing.T) {
	cases := []struct {
		golden string
		args   []string
	}{
		{"version-h", []string{"version", "-h"}},
		{"scan-h", []string{"scan", "-h"}},
		{"analyze-h", []string{"analyze", "-h"}},
		{"plan-h", []string{"plan", "-h"}},
		{"apply-h", []string{"apply", "-h"}},
		{"instrument-h", []string{"instrument", "-h"}},
		{"deps-h", []string{"deps", "-h"}},
		{"smoke-h", []string{"smoke", "-h"}},
		{"report-h", []string{"report", "-h"}},
		{"baseline-h", []string{"baseline", "-h"}},
		{"cardinality-h", []string{"cardinality", "-h"}},
		{"score-h", []string{"score", "-h"}},
		{"stack-h", []string{"stack", "-h"}},
		{"stack-up-h", []string{"stack", "up", "-h"}},
		{"stack-down-h", []string{"stack", "down", "-h"}},
		{"stack-status-h", []string{"stack", "status", "-h"}},
		{"verify-h", []string{"verify", "-h"}},
		{"doctor-h", []string{"doctor", "-h"}},
	}
	for _, tc := range cases {
		t.Run(tc.golden, func(t *testing.T) {
			out, errOut, err := captureStreams(t, func() error { return run(tc.args) })
			if err != nil {
				t.Fatalf("%s: %v", strings.Join(tc.args, " "), err)
			}
			checkGolden(t, tc.golden, "stdout:\n"+out+"stderr:\n"+errOut)
		})
	}
}
