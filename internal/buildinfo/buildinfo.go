package buildinfo

import (
	"runtime"
	"runtime/debug"
)

var (
	Version = "dev"
	Commit  = "unknown"
	Date    = "unknown"
)

type Info struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Date    string `json:"date"`
	Go      string `json:"go"`
	OS      string `json:"os"`
	Arch    string `json:"arch"`
}

func Current() Info {
	bi, ok := debug.ReadBuildInfo()
	version, commit, date := resolve(Version, Commit, Date, bi, ok)
	return Info{
		Version: version,
		Commit:  commit,
		Date:    date,
		Go:      runtime.Version(),
		OS:      runtime.GOOS,
		Arch:    runtime.GOARCH,
	}
}

// resolve keeps ldflags-injected values when present. When they are still the
// defaults (binaries built with `go install` or `go build` without ldflags), it
// falls back to the module version and VCS settings embedded by the Go
// toolchain.
func resolve(version, commit, date string, bi *debug.BuildInfo, ok bool) (string, string, string) {
	if !ok || bi == nil {
		return orDefault(version, "dev"), orDefault(commit, "unknown"), orDefault(date, "unknown")
	}
	if isDefault(version, "dev") && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		version = bi.Main.Version
	}
	for _, setting := range bi.Settings {
		switch setting.Key {
		case "vcs.revision":
			if isDefault(commit, "unknown") && setting.Value != "" {
				commit = setting.Value
			}
		case "vcs.time":
			if isDefault(date, "unknown") && setting.Value != "" {
				date = setting.Value
			}
		}
	}
	return orDefault(version, "dev"), orDefault(commit, "unknown"), orDefault(date, "unknown")
}

func orDefault(value, def string) string {
	if value == "" {
		return def
	}
	return value
}

func isDefault(value, def string) bool {
	return value == "" || value == def
}
