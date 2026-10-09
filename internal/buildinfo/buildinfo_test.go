package buildinfo

import (
	"runtime/debug"
	"testing"
)

func TestCurrentIncludesDeterministicDefaultsAndPlatform(t *testing.T) {
	got := Current()
	if got.Version == "" || got.Commit == "" || got.Date == "" || got.Go == "" || got.OS == "" || got.Arch == "" {
		t.Fatalf("incomplete build info: %#v", got)
	}
}

func TestResolveKeepsLdflagsValues(t *testing.T) {
	bi := &debug.BuildInfo{
		Main:     debug.Module{Version: "v9.9.9"},
		Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "abc"}, {Key: "vcs.time", Value: "2026-01-01T00:00:00Z"}},
	}
	v, c, d := resolve("1.1.0", "fromldflags", "2025-05-05", bi, true)
	if v != "1.1.0" || c != "fromldflags" || d != "2025-05-05" {
		t.Fatalf("ldflags values overridden: %s %s %s", v, c, d)
	}
}

func TestResolveFallsBackToModuleAndVCSInfo(t *testing.T) {
	bi := &debug.BuildInfo{
		Main: debug.Module{Version: "v1.1.0"},
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "0123456789abcdef"},
			{Key: "vcs.time", Value: "2026-03-04T05:06:07Z"},
		},
	}
	v, c, d := resolve("dev", "unknown", "unknown", bi, true)
	if v != "v1.1.0" || c != "0123456789abcdef" || d != "2026-03-04T05:06:07Z" {
		t.Fatalf("fallback = %s %s %s", v, c, d)
	}
}

func TestResolveIgnoresDevelModuleVersion(t *testing.T) {
	bi := &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}}
	v, c, d := resolve("", "", "", bi, true)
	if v != "dev" || c != "unknown" || d != "unknown" {
		t.Fatalf("devel should not be used as version; defaults expected, got %q %q %q", v, c, d)
	}
}

func TestResolveWithoutBuildInfoReturnsDefaults(t *testing.T) {
	v, c, d := resolve("dev", "unknown", "unknown", nil, false)
	if v != "dev" || c != "unknown" || d != "unknown" {
		t.Fatalf("got %q %q %q", v, c, d)
	}
}
