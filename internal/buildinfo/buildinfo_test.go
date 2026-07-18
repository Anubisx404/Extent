package buildinfo

import "testing"

func TestCurrentIncludesDeterministicDefaultsAndPlatform(t *testing.T) {
	got := Current()
	if got.Version == "" || got.Commit == "" || got.Date == "" || got.Go == "" || got.OS == "" || got.Arch == "" {
		t.Fatalf("incomplete build info: %#v", got)
	}
}
