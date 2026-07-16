package codemods

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
)

type Bundle struct {
	Files map[string]string
}

type Manifest struct {
	GeneratedBy string   `json:"generatedBy"`
	Targets     []string `json:"targets"`
	Patchers    []string `json:"patchers"`
}

func BuildBundle() Bundle {
	manifest := Manifest{
		GeneratedBy: "extent",
		Targets: []string{
			"http_routes",
			"db_calls",
			"queues",
			"external_http",
			"business_functions",
			"log_statements",
		},
		Patchers: []string{"node-ts-morph", "python-libcst", "dotnet-roslyn", "java-openrewrite", "java-javaparser"},
	}
	manifestJSON, _ := json.MarshalIndent(manifest, "", "  ")
	return Bundle{Files: map[string]string{
		"extent.codemods/manifest.json":                          string(manifestJSON) + "\n",
		"extent.codemods/README.md":                              readme,
		"extent.codemods/node/package.json":                      nodePackageJSON,
		"extent.codemods/node/deep-instrument.mjs":               nodeCodemod,
		"extent.codemods/python/requirements.txt":                "libcst>=1.4.0\n",
		"extent.codemods/python/deep_instrument.py":              pythonCodemod,
		"extent.codemods/dotnet/Extent.Codemods.csproj":          dotnetProject,
		"extent.codemods/dotnet/ExtentRoslynPatcher.cs":          dotnetPatcher,
		"extent.codemods/java/pom.xml":                           javaPom,
		"extent.codemods/java/openrewrite.yml":                   javaOpenRewrite,
		"extent.codemods/java/ExtentJavaParserPatcher.java":      javaParserPatcher,
		"extent.codemods/rules/deep-instrumentation-targets.yml": targetRules,
	}}
}

func Write(root string, overwrite bool) ([]string, error) {
	bundle := BuildBundle()
	paths := make([]string, 0, len(bundle.Files))
	for rel := range bundle.Files {
		paths = append(paths, rel)
	}
	sort.Strings(paths)
	for _, rel := range paths {
		target := filepath.Join(root, filepath.FromSlash(rel))
		if !overwrite {
			if _, err := os.Stat(target); err == nil {
				continue
			}
		}
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(target, []byte(bundle.Files[rel]), 0644); err != nil {
			return nil, err
		}
	}
	return paths, nil
}

const readme = `# Extent Deep Codemods

This folder contains generated AST patchers for deep instrumentation. Review
them before running. They are intentionally committed as local tools so the
target repository owns the exact transformations.

- Node/TypeScript: ts-morph patcher.
- Python: LibCST patcher.
- .NET: Roslyn patcher.
- Java: OpenRewrite recipe plus JavaParser patcher.
`

const nodePackageJSON = `{
  "private": true,
  "type": "module",
  "scripts": {
    "instrument": "node deep-instrument.mjs"
  },
  "dependencies": {
    "ts-morph": "^24.0.0"
  }
}
`

const nodeCodemod = `import fs from "node:fs";
import path from "node:path";
import { Project, SyntaxKind } from "ts-morph";

const root = path.resolve(process.argv[2] ?? ".");
const dryRun = process.argv.includes("--dry-run");
const backupDir = path.join(root, ".extent-backup", "codemods", "node");
const project = new Project({
  skipAddingFilesFromTsConfig: true,
  tsConfigFilePath: fs.existsSync(path.join(root, "tsconfig.json")) ? path.join(root, "tsconfig.json") : undefined,
});
project.addSourceFilesAtPaths([
  path.join(root, "src/**/*.{ts,tsx,js,jsx}"),
  path.join(root, "*.{ts,js}"),
]);

function backup(sourceFile) {
  const filePath = sourceFile.getFilePath();
  if (!fs.existsSync(filePath) || dryRun) return;
  const rel = path.relative(root, filePath);
  const target = path.join(backupDir, rel);
  fs.mkdirSync(path.dirname(target), { recursive: true });
  fs.copyFileSync(filePath, target);
}

function ensureImport(sourceFile, named, moduleName) {
  const existing = sourceFile.getImportDeclaration(moduleName);
  if (existing) return;
  sourceFile.addImportDeclaration({ namedImports: named, moduleSpecifier: moduleName });
}

function injectHttpRouteSpans(sourceFile) {
  sourceFile.forEachDescendant((node) => {
    if (!node.isKind(SyntaxKind.CallExpression)) return;
    const expr = node.getExpression().getText();
    if (!/\b(app|router)\.(get|post|put|patch|delete)\b/.test(expr)) return;
    ensureImport(sourceFile, ["trace", "SpanStatusCode"], "@opentelemetry/api");
    const args = node.getArguments();
    const handler = args[args.length - 1];
    if (!handler || !handler.getText().includes("extent.deep")) {
      const routeName = expr + " " + (args[0]?.getText() ?? "\"/\"");
      const replacement = "async (req, res, next) => {\n" +
        "  const span = trace.getTracer(\"extent.deep\").startSpan(" + JSON.stringify(routeName) + ");\n" +
        "  try {\n" +
        "    return await (" + handler.getText() + ")(req, res, next);\n" +
        "  } catch (error) {\n" +
        "    span.recordException(error);\n" +
        "    span.setStatus({ code: SpanStatusCode.ERROR, message: String(error?.message ?? error) });\n" +
        "    throw error;\n" +
        "  } finally {\n" +
        "    span.setAttribute(\"http.response.status_code\", res.statusCode);\n" +
        "    span.end();\n" +
        "  }\n" +
        "}";
      node.replaceWithText(node.getText().replace(handler.getText(), replacement));
    }
  });
}

function injectDbCalls(sourceFile) {
  sourceFile.forEachDescendant((node) => {
    if (!node.isKind(SyntaxKind.CallExpression)) return;
    const text = node.getExpression().getText();
    if (!/(prisma\.\w+|\.query|\.execute|\.findMany|\.findUnique)/.test(text)) return;
    ensureImport(sourceFile, ["trace"], "@opentelemetry/api");
    node.addSyntheticLeadingComment(SyntaxKind.MultiLineCommentTrivia, " extent.deep db_calls: sanitized db.statement, db.system, row_count, duration span ", true);
  });
}

function injectQueuesExternalHttpBusinessAndLogs(sourceFile) {
  sourceFile.forEachDescendant((node) => {
    if (!node.isKind(SyntaxKind.CallExpression)) return;
    const text = node.getExpression().getText();
    if (/(axios|fetch|got|undici)/.test(text)) node.addSyntheticLeadingComment(SyntaxKind.MultiLineCommentTrivia, " extent.deep external_http: destination, status, timeout, retries ", true);
    if (/(queue\.add|worker\.process|channel\.publish|consumer\.run)/.test(text)) node.addSyntheticLeadingComment(SyntaxKind.MultiLineCommentTrivia, " extent.deep queues: producer/consumer span, lag, retry count ", true);
    if (/(logger|pino|winston)\.(info|warn|error|debug)/.test(text)) {
      ensureImport(sourceFile, ["trace"], "@opentelemetry/api");
      node.addSyntheticLeadingComment(SyntaxKind.MultiLineCommentTrivia, " extent.deep log_statements: inject trace_id/span_id/service.name/env metadata using trace.getActiveSpan() ", true);
    }
  });
}

for (const sourceFile of project.getSourceFiles()) {
  backup(sourceFile);
  injectHttpRouteSpans(sourceFile);
  injectDbCalls(sourceFile);
  injectQueuesExternalHttpBusinessAndLogs(sourceFile);
}

if (dryRun) {
  for (const sourceFile of project.getSourceFiles()) {
    if (!sourceFile.isSaved()) console.log(sourceFile.getFilePath());
  }
} else {
  await project.save();
}
`

const pythonCodemod = `import libcst as cst
from libcst.metadata import MetadataWrapper

SPAN_HELPER = """
from opentelemetry import trace

def extent_deep_span(name, attributes=None):
    return trace.get_tracer("extent.deep").start_as_current_span(name, attributes=attributes or {})
"""

class ExtentDeepTransformer(cst.CSTTransformer):
    def leave_Call(self, original_node, updated_node):
        text = original_node.func.code if hasattr(original_node.func, "code") else ""
        markers = []
        if any(name in text for name in ["route", "get", "post", "put", "delete"]):
            markers.append("http_routes")
        if any(name in text for name in ["execute", "query", "session", "select"]):
            markers.append("db_calls")
        if any(name in text for name in ["requests.", "httpx.", "aiohttp."]):
            markers.append("external_http")
        if any(name in text for name in ["delay", "apply_async", "send_task"]):
            markers.append("queues")
        if any(name in text for name in ["logger.", "logging."]):
            markers.append("log_statements")
        if not markers:
            return updated_node
        return updated_node.with_changes(
            whitespace_before_args=cst.ParenthesizedWhitespace(
                first_line=cst.TrailingWhitespace(
                    whitespace=cst.SimpleWhitespace(""),
                    comment=cst.Comment("# extent.deep " + ",".join(markers)),
                    newline=cst.Newline(),
                )
            )
        )

def patch_file(path):
    source = open(path, "r", encoding="utf-8").read()
    module = cst.parse_module(source)
    wrapper = MetadataWrapper(module)
    patched = wrapper.visit(ExtentDeepTransformer())
    return patched.code

def patch_tree(root, dry_run=False):
    import pathlib
    import shutil
    root_path = pathlib.Path(root)
    backup_root = root_path / ".extent-backup" / "codemods" / "python"
    changed = []
    for path in list(root_path.rglob("*.py")):
        if ".venv" in path.parts or "venv" in path.parts or ".extent-backup" in path.parts:
            continue
        source = path.read_text(encoding="utf-8")
        patched = patch_file(str(path))
        if patched != source:
            changed.append(str(path))
            if not dry_run:
                backup_path = backup_root / path.relative_to(root_path)
                backup_path.parent.mkdir(parents=True, exist_ok=True)
                shutil.copy2(path, backup_path)
                path.write_text(patched, encoding="utf-8")
    return changed

if __name__ == "__main__":
    import argparse
    parser = argparse.ArgumentParser()
    parser.add_argument("root", nargs="?", default=".")
    parser.add_argument("--dry-run", action="store_true")
    args = parser.parse_args()
    for changed in patch_tree(args.root, args.dry_run):
        print(changed)
`

const dotnetPatcher = `using Microsoft.CodeAnalysis;
using Microsoft.CodeAnalysis.CSharp;
using Microsoft.CodeAnalysis.CSharp.Syntax;

public sealed class ExtentRoslynPatcher : CSharpSyntaxRewriter
{
    public static int Main(string[] args)
    {
        var root = args.Length > 0 ? args[0] : ".";
        foreach (var file in System.IO.Directory.EnumerateFiles(root, "*.cs", System.IO.SearchOption.AllDirectories))
        {
            if (file.Contains("\\bin\\") || file.Contains("\\obj\\") || file.Contains(".extent-backup")) continue;
            var source = System.IO.File.ReadAllText(file);
            var tree = CSharpSyntaxTree.ParseText(source);
            var rootNode = tree.GetRoot();
            var patched = new ExtentRoslynPatcher().Visit(rootNode)?.NormalizeWhitespace().ToFullString() ?? source;
            if (patched != source)
            {
                var backup = System.IO.Path.Combine(root, ".extent-backup", "codemods", "dotnet", System.IO.Path.GetRelativePath(root, file));
                System.IO.Directory.CreateDirectory(System.IO.Path.GetDirectoryName(backup)!);
                System.IO.File.Copy(file, backup, true);
                System.IO.File.WriteAllText(file, patched);
                System.Console.WriteLine(file);
            }
        }
        return 0;
    }

    public override SyntaxNode? VisitInvocationExpression(InvocationExpressionSyntax node)
    {
        var text = node.Expression.ToString();
        var target = text.Contains("MapGet") || text.Contains("MapPost") ? "http_routes" :
            text.Contains("SaveChanges") || text.Contains("FromSql") ? "db_calls" :
            text.Contains("SendAsync") || text.Contains("HttpClient") ? "external_http" :
            text.Contains("LogInformation") || text.Contains("LogError") ? "log_statements" :
            text.Contains("Enqueue") || text.Contains("Publish") ? "queues" : "";
        if (target == "") return base.VisitInvocationExpression(node);
        return node.WithLeadingTrivia(node.GetLeadingTrivia().Add(SyntaxFactory.Comment("// extent.deep " + target)));
    }
}
`

const dotnetProject = `<Project Sdk="Microsoft.NET.Sdk">
  <PropertyGroup>
    <OutputType>Exe</OutputType>
    <TargetFramework>net8.0</TargetFramework>
    <Nullable>enable</Nullable>
    <ImplicitUsings>enable</ImplicitUsings>
  </PropertyGroup>
  <ItemGroup>
    <PackageReference Include="Microsoft.CodeAnalysis.CSharp" Version="4.10.0" />
  </ItemGroup>
</Project>
`

const javaOpenRewrite = `type: specs.openrewrite.org/v1beta/recipe
name: extent.DeepInstrumentation
displayName: Extent deep OpenTelemetry instrumentation markers
recipeList:
  - org.openrewrite.java.search.FindMethods:
      methodPattern: "org.springframework.web.bind.annotation.* *(..)"
  - org.openrewrite.java.search.FindMethods:
      methodPattern: "java.sql.Statement execute*(..)"
  - org.openrewrite.java.search.FindMethods:
      methodPattern: "org.slf4j.Logger *(..)"
`

const javaParserPatcher = `import com.github.javaparser.StaticJavaParser;
import com.github.javaparser.ast.CompilationUnit;
import com.github.javaparser.ast.expr.MethodCallExpr;

public final class ExtentJavaParserPatcher {
  public static void main(String[] args) throws Exception {
    java.nio.file.Path root = java.nio.file.Paths.get(args.length > 0 ? args[0] : ".");
    java.nio.file.Path backupRoot = root.resolve(".extent-backup").resolve("codemods").resolve("java");
    try (java.util.stream.Stream<java.nio.file.Path> files = java.nio.file.Files.walk(root)) {
      files.filter(p -> p.toString().endsWith(".java"))
        .filter(p -> !p.toString().contains(".extent-backup"))
        .forEach(p -> {
          try {
            String source = java.nio.file.Files.readString(p);
            String patched = patch(source).toString();
            if (!source.equals(patched)) {
              java.nio.file.Path backup = backupRoot.resolve(root.relativize(p));
              java.nio.file.Files.createDirectories(backup.getParent());
              java.nio.file.Files.copy(p, backup, java.nio.file.StandardCopyOption.REPLACE_EXISTING);
              java.nio.file.Files.writeString(p, patched);
              System.out.println(p);
            }
          } catch (Exception e) {
            throw new RuntimeException(e);
          }
        });
    }
  }

  public static CompilationUnit patch(String source) {
    CompilationUnit unit = StaticJavaParser.parse(source);
    for (MethodCallExpr call : unit.findAll(MethodCallExpr.class)) {
      String name = call.getNameAsString();
      if (name.matches("execute|executeQuery|query|send|publish|info|error|warn")) {
        call.setLineComment("extent.deep JavaParser db_calls/external_http/queues/log_statements");
      }
    }
    return unit;
  }
}
`

const javaPom = `<project xmlns="http://maven.apache.org/POM/4.0.0" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xsi:schemaLocation="http://maven.apache.org/POM/4.0.0 https://maven.apache.org/xsd/maven-4.0.0.xsd">
  <modelVersion>4.0.0</modelVersion>
  <groupId>extent</groupId>
  <artifactId>extent-java-codemods</artifactId>
  <version>1.0.0</version>
  <dependencies>
    <dependency>
      <groupId>com.github.javaparser</groupId>
      <artifactId>javaparser-core</artifactId>
      <version>3.26.1</version>
    </dependency>
  </dependencies>
</project>
`

const targetRules = `targets:
  http_routes:
    attributes: [http.route, http.request.method, http.response.status_code, duration]
  db_calls:
    attributes: [db.system, db.operation.name, db.statement.sanitized, db.rows_affected]
  queues:
    attributes: [messaging.system, messaging.destination.name, retry_count, queue_lag_ms]
  external_http:
    attributes: [server.address, http.response.status_code, error.type, timeout]
  business_functions:
    attributes: [code.function, code.namespace, duration, error.type]
  log_statements:
    attributes: [trace_id, span_id, service.name, deployment.environment]
`
