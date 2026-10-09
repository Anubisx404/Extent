package scanner

import (
	"sort"
	"testing"

	"github.com/Anubisx404/Extent/recipes"
)

// detectionOnly lists frameworks the scanner reports but Extent does not
// instrument yet. Each must not have a recipe. Adding a recipe removes the
// entry; leaving a stale entry fails the test.
var detectionOnly = map[string]string{
	"svelte":    "detected for reporting; no bootstrap recipe yet",
	"sveltekit": "detected for reporting; no bootstrap recipe yet",
	"chi":       "detected for reporting; no bootstrap recipe yet",
}

func TestEveryDetectableFrameworkHasRecipeOrDetectionOnlyEntry(t *testing.T) {
	all, err := recipes.All()
	if err != nil {
		t.Fatal(err)
	}
	recipeNames := map[string]bool{}
	for _, recipe := range all {
		recipeNames[recipe.Name] = true
	}
	emitted := map[string]bool{}
	for _, framework := range DetectableFrameworks() {
		emitted[framework] = true
		if recipeNames[framework] {
			if _, ok := detectionOnly[framework]; ok {
				t.Errorf("framework %q has a recipe but is still listed as detection-only", framework)
			}
			continue
		}
		if _, ok := detectionOnly[framework]; !ok {
			t.Errorf("scanner emits framework %q with no recipe and no detectionOnly entry", framework)
		}
	}
	var stale []string
	for name := range detectionOnly {
		if !emitted[name] {
			stale = append(stale, name)
		}
	}
	sort.Strings(stale)
	if len(stale) > 0 {
		t.Errorf("detectionOnly lists frameworks the scanner no longer emits: %v", stale)
	}
}

func TestDetectableFrameworksIncludesEveryTableEntry(t *testing.T) {
	got := map[string]bool{}
	for _, framework := range DetectableFrameworks() {
		got[framework] = true
	}
	for _, framework := range nodeFrameworkDependencies {
		if !got[framework] {
			t.Fatalf("node framework %q missing from DetectableFrameworks", framework)
		}
	}
	for _, framework := range textFrameworkNeedles {
		if !got[framework] {
			t.Fatalf("text framework %q missing from DetectableFrameworks", framework)
		}
	}
	if !got["nethttp"] {
		t.Fatal("nethttp missing from DetectableFrameworks")
	}
}
