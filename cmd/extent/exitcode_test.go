package main

import (
	"errors"
	"fmt"
	"testing"

	"github.com/Anubisx404/Extent/internal/fileops"
)

// TestExitCodeTable pins the exit-code contract documented above exitCode. Each
// error kind must keep its code, and wrapped errors must classify the same way.
func TestExitCodeTable(t *testing.T) {
	cases := []struct {
		name    string
		err     error
		want    int
		message string
	}{
		{"usage", usageError("bad flag"), 2, "bad flag"},
		{"safety", safetyError("conflicting flags"), 3, "conflicting flags"},
		{"unavailable", unavailableError("docker missing"), 4, "docker missing"},
		{"verification", verificationError("checks failed"), 5, "checks failed"},
		{"unclassified", errors.New("disk on fire"), 1, "disk on fire"},
		{"wrapped usage", fmt.Errorf("parse: %w", usageError("bad mode")), 2, "parse: bad mode"},
		{"wrapped verification", fmt.Errorf("report: %w", verificationError("warnings")), 5, "report: warnings"},
		{"unsafe path", fmt.Errorf("write plan: %w", fileops.ErrUnsafePath), 3, "write plan: unsafe path"},
		{"unsafe path joined", fmt.Errorf("apply: %w", errors.Join(fileops.ErrUnsafePath)), 3, "apply: unsafe path"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := exitCode(tc.err); got != tc.want {
				t.Fatalf("exitCode(%v) = %d, want %d", tc.err, got, tc.want)
			}
			if tc.err.Error() != tc.message {
				t.Fatalf("message = %q, want %q", tc.err.Error(), tc.message)
			}
		})
	}
}
