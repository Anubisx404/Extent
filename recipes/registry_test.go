package recipes

import (
	"fmt"
	"testing"
)

func TestEveryEmbeddedRecipeValidates(t *testing.T) {
	rs, err := All()
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 14 {
		t.Fatalf("got %d recipes", len(rs))
	}
	seen := map[string]bool{}
	for _, recipe := range rs {
		key := fmt.Sprintf("%s/%s", recipe.Runtime, recipe.Name)
		if seen[key] {
			t.Fatalf("duplicate recipe %s", key)
		}
		seen[key] = true
		if recipe.Support.Bootstrap == "" || recipe.Support.Deep == "" {
			t.Fatalf("recipe %s has incomplete support: %+v", key, recipe.Support)
		}
	}
}
func TestMalformedAndUnknownRecipeFieldsFail(t *testing.T) {
	if _, err := decode([]byte("name: x\nruntime: node\nunknown: true\n"), "test"); err == nil {
		t.Fatal("accepted unknown field")
	}
	if _, err := decode([]byte("runtime: node\n"), "test"); err == nil {
		t.Fatal("accepted missing name")
	}
	for _, data := range []string{
		"name: x\nruntime: ruby\nsupport:\n  bootstrap: unsupported\n  deep: unsupported\n",
		"name: x\nruntime: node\nsupport:\n  bootstrap: unknown\n  deep: experimental\n",
		"name: x\nruntime: node\nsupport:\n  bootstrap: stable\n  deep: experimental\n---\nname: y\nruntime: node\n",
	} {
		if _, err := decode([]byte(data), "test"); err == nil {
			t.Fatalf("accepted invalid recipe %q", data)
		}
	}
}
