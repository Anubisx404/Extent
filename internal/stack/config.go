package stack

import (
	"fmt"
	"runtime"
)

type Profile string

const (
	ProfileCore             Profile = "core"
	ProfileCorePersistent   Profile = "core-persistent"
	ProfileHostMetricsLinux Profile = "host-metrics-linux"
)

type ProfileConfig struct {
	Name             Profile
	Persistent       bool
	HostMetrics      bool
	PrivilegeWarning string
}

func ResolveProfile(name string) (ProfileConfig, error) {
	switch name {
	case "", "core", "core-persistent", "full", "report-heavy", "low-resource", "minimal", "high-cardinality-safe":
		p := ProfileCorePersistent
		if name == "core" {
			p = ProfileCore
		}
		return ProfileConfig{Name: p, Persistent: p == ProfileCorePersistent}, nil
	case "host-metrics-linux":
		if runtime.GOOS != "linux" {
			return ProfileConfig{}, fmt.Errorf("profile %q requires Linux", name)
		}
		return ProfileConfig{Name: ProfileHostMetricsLinux, Persistent: true, HostMetrics: true, PrivilegeWarning: "host metrics requires privileged Linux host mounts"}, nil
	default:
		return ProfileConfig{}, fmt.Errorf("unknown observability profile: %s", name)
	}
}
