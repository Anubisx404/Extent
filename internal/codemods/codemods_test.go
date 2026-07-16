package codemods

import (
	"strings"
	"testing"
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
