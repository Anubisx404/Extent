package config

import (
	"reflect"
	"testing"
)

func TestDefaultsDeterministicAndValid(t *testing.T) {
	a, b := Defaults(), Defaults()
	if !reflect.DeepEqual(a, b) {
		t.Fatal("defaults nondeterministic")
	}
	if err := a.Validate(); err != nil {
		t.Fatal(err)
	}
	if a.Profile.Name != "full" {
		t.Fatalf("default profile = %q, want full", a.Profile.Name)
	}
}
func TestParseRejectsUnknownAndInvalid(t *testing.T) {
	for _, data := range []string{
		"unknown: true\n",
		"version: 2\n",
		"protocol:\n  http: false\n  grpc: false\n",
		"service:\n  port: 0\n",
		"profile:\n  name: unknown\n",
		"service:\n  name: '   '\n",
		"profile:\n  recipes: [' ']\n",
		"---\nversion: 1\n---\nversion: 1\n",
	} {
		if _, err := Parse([]byte(data)); err == nil {
			t.Fatalf("accepted %q", data)
		}
	}
}

func TestParseDoesNotInspectYAMLTextWithSubstrings(t *testing.T) {
	c, err := Parse([]byte("# protocol: {} is documented here\nservice:\n  name: api\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !c.Protocol.HTTP {
		t.Fatal("default HTTP protocol was lost")
	}
}
func TestParseAcceptsOverrides(t *testing.T) {
	c, err := Parse([]byte("profile:\n  name: minimal\nprotocol:\n  grpc: true\nservice:\n  name: api\n  port: 9090\n"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Profile.Name != "minimal" || !c.Protocol.GRPC || c.Service.Port != 9090 {
		t.Fatalf("%+v", c)
	}
}
