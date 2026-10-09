package templates

import (
	"strings"
	"testing"

	"github.com/Anubisx404/Extent/internal/stack"
	"gopkg.in/yaml.v3"
)

type parsedCompose struct {
	Services map[string]struct {
		Image      string   `yaml:"image"`
		Privileged bool     `yaml:"privileged"`
		Ports      []string `yaml:"ports"`
		Volumes    []string `yaml:"volumes"`
		DependsOn  []string `yaml:"depends_on"`
	} `yaml:"services"`
	Volumes map[string]any `yaml:"volumes"`
}

func allComposeProfiles() []stack.ProfileConfig {
	var out []stack.ProfileConfig
	for _, name := range goldenProfiles {
		for _, hostMetrics := range []bool{false, true} {
			for _, persistent := range []bool{false, true} {
				out = append(out, stack.ProfileConfig{Name: stack.Profile(name), HostMetrics: hostMetrics, Persistent: persistent})
			}
		}
	}
	return out
}

func TestComposeRenderingIsByteStable(t *testing.T) {
	for _, profile := range allComposeProfiles() {
		first := composeYAMLForProfile(profile)
		for i := 0; i < 5; i++ {
			if again := composeYAMLForProfile(profile); again != first {
				t.Fatalf("profile %+v rendered differently on run %d", profile, i+1)
			}
		}
	}
}

func TestComposePublishedPortsAreLoopbackOnlyInEveryProfile(t *testing.T) {
	for _, profile := range allComposeProfiles() {
		var doc parsedCompose
		if err := yaml.Unmarshal([]byte(composeYAMLForProfile(profile)), &doc); err != nil {
			t.Fatal(err)
		}
		for name, service := range doc.Services {
			for _, port := range service.Ports {
				if !strings.HasPrefix(port, "127.0.0.1:") {
					t.Fatalf("profile %+v: service %s publishes %q without 127.0.0.1", profile, name, port)
				}
			}
		}
	}
}

func TestComposeCAdvisorIsPrivilegedOnlyWithHostMetrics(t *testing.T) {
	for _, profile := range allComposeProfiles() {
		var doc parsedCompose
		if err := yaml.Unmarshal([]byte(composeYAMLForProfile(profile)), &doc); err != nil {
			t.Fatal(err)
		}
		cadvisor, hasCadvisor := doc.Services["cadvisor"]
		privileged := hasCadvisor && cadvisor.Privileged
		if profile.HostMetrics != hasCadvisor {
			t.Fatalf("profile %+v: cadvisor present=%t, want %t", profile, hasCadvisor, profile.HostMetrics)
		}
		if privileged != profile.HostMetrics {
			t.Fatalf("profile %+v: cadvisor privileged=%t", profile, privileged)
		}
		for name, service := range doc.Services {
			if name != "cadvisor" && service.Privileged {
				t.Fatalf("profile %+v: service %s is privileged", profile, name)
			}
		}
		if _, ok := doc.Services["node-exporter"]; ok != profile.HostMetrics {
			t.Fatalf("profile %+v: node-exporter presence=%t", profile, ok)
		}
	}
}

func TestComposeDependsOnNeverReferencesAbsentServices(t *testing.T) {
	for _, profile := range allComposeProfiles() {
		var doc parsedCompose
		if err := yaml.Unmarshal([]byte(composeYAMLForProfile(profile)), &doc); err != nil {
			t.Fatal(err)
		}
		for name, service := range doc.Services {
			for _, dep := range service.DependsOn {
				if _, ok := doc.Services[dep]; !ok {
					t.Fatalf("profile %+v: %s depends on absent service %s", profile, name, dep)
				}
			}
		}
		if profile.Persistent {
			for name, service := range doc.Services {
				for _, volume := range service.Volumes {
					src := strings.SplitN(volume, ":", 2)[0]
					if !strings.HasPrefix(src, ".") && !strings.HasPrefix(src, "/") {
						if _, ok := doc.Volumes[src]; !ok {
							t.Fatalf("profile %+v: %s mounts undeclared volume %s", profile, name, src)
						}
					}
				}
			}
		}
	}
}
