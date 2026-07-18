package codemods

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Anubisx404/Extent/internal/fileops"
	"github.com/Anubisx404/Extent/internal/state"
)

func TestBuildBundleIncludesLanguageSpecificASTPatchers(t *testing.T) {
	bundle := BuildBundle()

	requireFileContains(t, bundle, "extent.codemods/node/package.json", "ts-morph")
	requireFileContains(t, bundle, "extent.codemods/node/deep-instrument.mjs", "Project")
	requireFileContains(t, bundle, "extent.codemods/node/deep-instrument.mjs", "injectHttpRouteSpans")
	requireFileContains(t, bundle, "extent.codemods/python/deep_instrument.py", "libcst")
	requireFileContains(t, bundle, "extent.codemods/dotnet/ExtentRoslynPatcher.cs", "Microsoft.CodeAnalysis")
	requireFileContains(t, bundle, "extent.codemods/java/openrewrite.yml", "org.openrewrite.java")
	requireFileContains(t, bundle, "extent.codemods/java/ExtentJavaParserPatcher.java", "JavaParser")
}

func TestBuildBundleDocumentsDeepInjectionTargets(t *testing.T) {
	bundle := BuildBundle()

	for _, target := range []string{
		"http_routes",
		"db_calls",
		"queues",
		"external_http",
		"business_functions",
		"log_statements",
	} {
		requireFileContains(t, bundle, "extent.codemods/manifest.json", target)
	}
}

func TestBuildBundleIncludesRunnablePatchersWithBackups(t *testing.T) {
	bundle := BuildBundle()

	requireFileContains(t, bundle, "extent.codemods/node/deep-instrument.mjs", "process.argv")
	requireFileContains(t, bundle, "extent.codemods/node/deep-instrument.mjs", ".extent-backup")
	requireFileContains(t, bundle, "extent.codemods/node/deep-instrument.mjs", "trace.getActiveSpan")
	requireFileContains(t, bundle, "extent.codemods/python/deep_instrument.py", "if __name__ == \"__main__\"")
	requireFileContains(t, bundle, "extent.codemods/python/deep_instrument.py", ".extent-backup")
	requireFileContains(t, bundle, "extent.codemods/python/deep_instrument.py", "start_as_current_span")
	requireFileContains(t, bundle, "extent.codemods/dotnet/ExtentRoslynPatcher.cs", "static int Main")
	requireFileContains(t, bundle, "extent.codemods/java/ExtentJavaParserPatcher.java", "public static void main")
}

func TestWriteBundleHandlesMixedCreateUpdateIdempotenceAndUndo(t *testing.T) {
	root := t.TempDir()
	manifestPath := filepath.Join(root, "extent.codemods", "manifest.json")
	if err := os.MkdirAll(filepath.Dir(manifestPath), 0o755); err != nil {
		t.Fatal(err)
	}
	original := []byte("user manifest\n")
	if err := os.WriteFile(manifestPath, original, 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := Write(root, false); err == nil {
		t.Fatal("existing bundle file was overwritten without permission")
	}
	changed, err := Write(root, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(changed) != len(BuildBundle().Files) {
		t.Fatalf("changed paths = %d, want %d", len(changed), len(BuildBundle().Files))
	}
	second, err := Write(root, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 0 {
		t.Fatalf("idempotent write reported changes: %#v", second)
	}
	manifest, err := state.Load(root)
	if err != nil || len(manifest.Operations) != 1 {
		t.Fatalf("state = %#v, %v", manifest, err)
	}
	if err := fileops.UndoKind(root, "codemod-bundle"); err != nil {
		t.Fatal(err)
	}
	restored, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(restored, original) {
		t.Fatalf("restored manifest = %q", restored)
	}
	if _, err := os.Stat(filepath.Join(root, "extent.codemods", "README.md")); !os.IsNotExist(err) {
		t.Fatalf("created bundle file survived undo: %v", err)
	}
}

func requireFileContains(t *testing.T, bundle Bundle, path, want string) {
	t.Helper()
	content, ok := bundle.Files[path]
	if !ok {
		t.Fatalf("expected bundle file %s", path)
	}
	if !strings.Contains(content, want) {
		t.Fatalf("expected %s to contain %q, got:\n%s", path, want, content)
	}
}
