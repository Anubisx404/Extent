package instrumenter

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstrumentNodeCommonJSInjectsBootstrapAndDependencies(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "package.json"), `{"dependencies":{"express":"^4.18.0","pg":"^8.0.0","redis":"^4.0.0"}}`)
	mustWrite(t, filepath.Join(root, "server.js"), `const express = require("express");
console.log("start");
`)

	result, err := Instrument(root, Options{})
	if err != nil {
		t.Fatal(err)
	}

	assertChanged(t, result, "server.js")
	assertFileContains(t, filepath.Join(root, "server.js"), `require("./extent.instrumentation"); // extent:otel`)
	assertFileContains(t, filepath.Join(root, "extent.instrumentation.js"), "@opentelemetry/sdk-node")
	assertFileContains(t, filepath.Join(root, "extent.instrumentation.js"), "enhancedDatabaseReporting")
	assertFileContains(t, filepath.Join(root, "package.json"), "@opentelemetry/auto-instrumentations-node")
	assertFileContains(t, filepath.Join(root, "package.json"), "@opentelemetry/instrumentation-pg")
	assertFileContains(t, filepath.Join(root, "package.json"), "@opentelemetry/instrumentation-redis")

	second, err := Instrument(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.ChangedFiles) != 0 {
		t.Fatalf("expected idempotent second run, changed %#v", second.ChangedFiles)
	}
}

func TestInstrumentNodeESMUsesImportBootstrap(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "package.json"), `{"type":"module","dependencies":{"express":"^4.18.0"}}`)
	mustWrite(t, filepath.Join(root, "index.js"), `import express from "express";
console.log(express);
`)

	_, err := Instrument(root, Options{})
	if err != nil {
		t.Fatal(err)
	}

	assertFileContains(t, filepath.Join(root, "index.js"), `import "./extent.instrumentation.mjs"; // extent:otel`)
	assertFileContains(t, filepath.Join(root, "extent.instrumentation.mjs"), "@opentelemetry/sdk-node")
}

func TestInstrumentPythonInjectsBootstrapAndRequirements(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "requirements.txt"), "fastapi\nsqlalchemy\npsycopg2\n")
	mustWrite(t, filepath.Join(root, "main.py"), "print('start')\n")

	result, err := Instrument(root, Options{})
	if err != nil {
		t.Fatal(err)
	}

	assertChanged(t, result, "main.py")
	assertFileContains(t, filepath.Join(root, "main.py"), "import extent_instrumentation  # extent:otel")
	assertFileContains(t, filepath.Join(root, "extent_instrumentation.py"), "OTLPSpanExporter")
	assertFileContains(t, filepath.Join(root, "extent_instrumentation.py"), "SQLAlchemyInstrumentor")
	assertFileContains(t, filepath.Join(root, "extent_instrumentation.py"), "RequestsInstrumentor")
	assertFileContains(t, filepath.Join(root, "requirements.txt"), "opentelemetry-exporter-otlp")
	assertFileContains(t, filepath.Join(root, "requirements.txt"), "opentelemetry-instrumentation-sqlalchemy")
	assertFileContains(t, filepath.Join(root, "requirements.txt"), "opentelemetry-instrumentation-psycopg2")
}

func TestInstrumentGoAddsBootstrapWithASTImport(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "go.mod"), "module example.com/payments\n\ngo 1.23\n")
	mustWrite(t, filepath.Join(root, "main.go"), `package main

import "fmt"

func main() {
	fmt.Println("start")
}
`)

	result, err := Instrument(root, Options{})
	if err != nil {
		t.Fatal(err)
	}

	assertChanged(t, result, "main.go")
	assertChanged(t, result, "internal/observability/otel.go")
	assertFileContains(t, filepath.Join(root, "main.go"), `extentotel "example.com/payments/internal/observability"`)
	assertFileContains(t, filepath.Join(root, "main.go"), "defer extentotel.Shutdown()")
	assertFileContains(t, filepath.Join(root, "internal/observability/otel.go"), "otlptracehttp")

	second, err := Instrument(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.ChangedFiles) != 0 {
		t.Fatalf("expected idempotent second run, changed %#v", second.ChangedFiles)
	}
}

func TestInstrumentDeepWritesCodemodBundle(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "package.json"), `{"dependencies":{"express":"^4.18.0"}}`)
	mustWrite(t, filepath.Join(root, "server.js"), `const express = require("express");`)

	result, err := Instrument(root, Options{Mode: "deep"})
	if err != nil {
		t.Fatal(err)
	}

	assertChanged(t, result, "extent.codemods/manifest.json")
	assertChanged(t, result, "extent.codemods/node/deep-instrument.mjs")
	assertFileContains(t, filepath.Join(root, "extent.codemods", "node", "deep-instrument.mjs"), "ts-morph")
	assertFileContains(t, filepath.Join(root, "extent.codemods", "python", "deep_instrument.py"), "libcst")
}

func TestInstrumentDotnetFlatProject(t *testing.T) {
	root := t.TempDir()
	csproj := `<Project Sdk="Microsoft.NET.Sdk.Web">
  <PropertyGroup>
    <TargetFramework>net8.0</TargetFramework>
  </PropertyGroup>
</Project>`
	program := `var builder = WebApplication.CreateBuilder(args);
var app = builder.Build();
app.Run();
`
	mustWrite(t, filepath.Join(root, "MyApi.csproj"), csproj)
	mustWrite(t, filepath.Join(root, "Program.cs"), program)

	result, err := Instrument(root, Options{})
	if err != nil {
		t.Fatal(err)
	}

	assertChanged(t, result, "Program.cs")
	assertChanged(t, result, "MyApi.csproj")
	assertChanged(t, result, "ExtentObservabilityExtensions.cs")

	assertFileContains(t, filepath.Join(root, "Program.cs"), "builder.Services.AddExtentObservability(builder.Configuration);")
	assertFileContains(t, filepath.Join(root, "MyApi.csproj"), "OpenTelemetry.Extensions.Hosting")
	assertFileContains(t, filepath.Join(root, "MyApi.csproj"), "OpenTelemetry.Instrumentation.AspNetCore")
	assertFileContains(t, filepath.Join(root, "MyApi.csproj"), "OpenTelemetry.Exporter.OpenTelemetryProtocol")
	assertFileContains(t, filepath.Join(root, "ExtentObservabilityExtensions.cs"), "AddExtentObservability")
	assertFileContains(t, filepath.Join(root, "ExtentObservabilityExtensions.cs"), "http://localhost:4318")

	second, err := Instrument(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.ChangedFiles) != 0 {
		t.Fatalf("expected idempotent second run, changed %#v", second.ChangedFiles)
	}

	undoRes, err := Instrument(root, Options{Undo: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(undoRes.Messages) == 0 {
		t.Fatal("expected undo messages")
	}
	if exists(filepath.Join(root, "ExtentObservabilityExtensions.cs")) {
		t.Fatal("expected ExtentObservabilityExtensions.cs to be removed on undo")
	}
	progData, err := os.ReadFile(filepath.Join(root, "Program.cs"))
	if err != nil || string(progData) != program {
		t.Fatalf("expected Program.cs restored, got %s", string(progData))
	}
}

func TestInstrumentDotnetCleanArchitecture(t *testing.T) {
	root := t.TempDir()

	domainCsproj := `<Project Sdk="Microsoft.NET.Sdk">
  <PropertyGroup>
    <TargetFramework>net8.0</TargetFramework>
  </PropertyGroup>
</Project>`

	infraCsproj := `<Project Sdk="Microsoft.NET.Sdk">
  <ItemGroup>
    <PackageReference Include="Microsoft.EntityFrameworkCore" Version="8.0.0" />
  </ItemGroup>
</Project>`

	webApiCsproj := `<Project Sdk="Microsoft.NET.Sdk.Web">
  <PropertyGroup>
    <TargetFramework>net8.0</TargetFramework>
  </PropertyGroup>
  <ItemGroup>
    <ProjectReference Include="..\Infrastructure\Infrastructure.csproj" />
  </ItemGroup>
</Project>`

	webApiProgram := `using Microsoft.AspNetCore.Builder;

var builder = WebApplication.CreateBuilder(args);

builder.Services.AddControllers();

var app = builder.Build();

app.MapControllers();

app.Run();
`

	testsCsproj := `<Project Sdk="Microsoft.NET.Sdk">
  <PropertyGroup>
    <TargetFramework>net8.0</TargetFramework>
  </PropertyGroup>
</Project>`

	mustWrite(t, filepath.Join(root, "src", "Domain", "Domain.csproj"), domainCsproj)
	mustWrite(t, filepath.Join(root, "src", "Infrastructure", "Infrastructure.csproj"), infraCsproj)
	mustWrite(t, filepath.Join(root, "src", "WebApi", "WebApi.csproj"), webApiCsproj)
	mustWrite(t, filepath.Join(root, "src", "WebApi", "Program.cs"), webApiProgram)
	mustWrite(t, filepath.Join(root, "tests", "UnitTests", "UnitTests.csproj"), testsCsproj)

	result, err := Instrument(root, Options{})
	if err != nil {
		t.Fatal(err)
	}

	assertChanged(t, result, "src/WebApi/Program.cs")
	assertChanged(t, result, "src/WebApi/WebApi.csproj")
	assertChanged(t, result, "src/WebApi/ExtentObservabilityExtensions.cs")

	assertFileContains(t, filepath.Join(root, "src", "WebApi", "Program.cs"), "builder.Services.AddExtentObservability(builder.Configuration);")
	assertFileContains(t, filepath.Join(root, "src", "WebApi", "WebApi.csproj"), "OpenTelemetry.Instrumentation.EntityFrameworkCore")
	assertFileContains(t, filepath.Join(root, "src", "WebApi", "ExtentObservabilityExtensions.cs"), "AddEntityFrameworkCoreInstrumentation()")
	assertFileContains(t, filepath.Join(root, "src", "WebApi", "ExtentObservabilityExtensions.cs"), "http://localhost:4318")

	domainData, _ := os.ReadFile(filepath.Join(root, "src", "Domain", "Domain.csproj"))
	if string(domainData) != domainCsproj {
		t.Fatal("expected Domain.csproj to remain untouched")
	}

	second, err := Instrument(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.ChangedFiles) != 0 {
		t.Fatalf("expected idempotent second run, changed %#v", second.ChangedFiles)
	}

	_, err = Instrument(root, Options{Undo: true})
	if err != nil {
		t.Fatal(err)
	}
	if exists(filepath.Join(root, "src", "WebApi", "ExtentObservabilityExtensions.cs")) {
		t.Fatal("expected src/WebApi/ExtentObservabilityExtensions.cs to be removed on undo")
	}
	restoredProgram, _ := os.ReadFile(filepath.Join(root, "src", "WebApi", "Program.cs"))
	if string(restoredProgram) != webApiProgram {
		t.Fatalf("expected Program.cs restored on undo, got: %s", string(restoredProgram))
	}
}

func TestInstrumentDotnetExplicitEntrypoint(t *testing.T) {
	root := t.TempDir()

	mustWrite(t, filepath.Join(root, "src", "ApiOne", "ApiOne.csproj"), `<Project Sdk="Microsoft.NET.Sdk.Web"></Project>`)
	mustWrite(t, filepath.Join(root, "src", "ApiOne", "Program.cs"), `var builder = WebApplication.CreateBuilder(args);
var app = builder.Build();
app.Run();`)

	mustWrite(t, filepath.Join(root, "src", "ApiTwo", "ApiTwo.csproj"), `<Project Sdk="Microsoft.NET.Sdk.Web"></Project>`)
	mustWrite(t, filepath.Join(root, "src", "ApiTwo", "Program.cs"), `var builder = WebApplication.CreateBuilder(args);
var app = builder.Build();
app.Run();`)

	_, err := Instrument(root, Options{})
	if err == nil || !strings.Contains(err.Error(), "ambiguous entrypoints") {
		t.Fatalf("expected ambiguous entrypoints error, got %v", err)
	}

	result, err := Instrument(root, Options{Entrypoint: "src/ApiTwo/Program.cs"})
	if err != nil {
		t.Fatal(err)
	}
	assertChanged(t, result, "src/ApiTwo/Program.cs")
	assertChanged(t, result, "src/ApiTwo/ApiTwo.csproj")
	assertChanged(t, result, "src/ApiTwo/ExtentObservabilityExtensions.cs")
}

func TestInstrumentDotnetSlnxSolution(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "CleanArch.slnx"), `<Solution><Project Path="src/Web/Web.csproj" /></Solution>`)
	mustWrite(t, filepath.Join(root, "src", "Web", "Web.csproj"), `<Project Sdk="Microsoft.NET.Sdk.Web"><PropertyGroup><TargetFramework>net9.0</TargetFramework></PropertyGroup></Project>`)
	mustWrite(t, filepath.Join(root, "src", "Web", "Program.cs"), `var builder = WebApplication.CreateBuilder(args);
var app = builder.Build();
app.Run();`)

	result, err := Instrument(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	assertChanged(t, result, "src/Web/Program.cs")
	assertChanged(t, result, "src/Web/Web.csproj")
	assertChanged(t, result, "src/Web/ExtentObservabilityExtensions.cs")
}

func TestInstrumentDotnetCentralPackageManagement(t *testing.T) {
	root := t.TempDir()
	dpp := `<Project>
  <PropertyGroup>
    <ManagePackageVersionsCentrally>true</ManagePackageVersionsCentrally>
  </PropertyGroup>
</Project>`
	webCsproj := `<Project Sdk="Microsoft.NET.Sdk.Web">
  <PropertyGroup>
    <TargetFramework>net8.0</TargetFramework>
  </PropertyGroup>
</Project>`
	program := `var builder = WebApplication.CreateBuilder(args);
var app = builder.Build();
app.Run();`

	mustWrite(t, filepath.Join(root, "Directory.Packages.props"), dpp)
	mustWrite(t, filepath.Join(root, "src", "Web", "Web.csproj"), webCsproj)
	mustWrite(t, filepath.Join(root, "src", "Web", "Program.cs"), program)

	result, err := Instrument(root, Options{})
	if err != nil {
		t.Fatal(err)
	}

	assertChanged(t, result, "Directory.Packages.props")
	assertChanged(t, result, "src/Web/Web.csproj")
	assertChanged(t, result, "src/Web/Program.cs")
	assertChanged(t, result, "src/Web/ExtentObservabilityExtensions.cs")

	assertFileContains(t, filepath.Join(root, "Directory.Packages.props"), `<PackageVersion Include="OpenTelemetry.Extensions.Hosting" Version="1.11.2" />`)
	assertFileContains(t, filepath.Join(root, "src", "Web", "Web.csproj"), `<PackageReference Include="OpenTelemetry.Extensions.Hosting" />`)

	extData, err := os.ReadFile(filepath.Join(root, "src", "Web", "ExtentObservabilityExtensions.cs"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(extData), "namespace Microsoft.Extensions.DependencyInjection\n{") && !strings.Contains(string(extData), "namespace Microsoft.Extensions.DependencyInjection\r\n{") {
		t.Fatalf("expected block-scoped namespace, got: %s", string(extData))
	}

	undoRes, err := Instrument(root, Options{Undo: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(undoRes.Messages) == 0 {
		t.Fatal("expected undo messages")
	}

	restoredDpp, _ := os.ReadFile(filepath.Join(root, "Directory.Packages.props"))
	if string(restoredDpp) != dpp {
		t.Fatalf("expected Directory.Packages.props restored, got: %s", string(restoredDpp))
	}
	restoredCsproj, _ := os.ReadFile(filepath.Join(root, "src", "Web", "Web.csproj"))
	if string(restoredCsproj) != webCsproj {
		t.Fatalf("expected Web.csproj restored, got: %s", string(restoredCsproj))
	}
	if exists(filepath.Join(root, "src", "Web", "ExtentObservabilityExtensions.cs")) {
		t.Fatal("expected ExtentObservabilityExtensions.cs deleted on undo")
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func assertChanged(t *testing.T, result Result, rel string) {
	t.Helper()
	for _, changed := range result.ChangedFiles {
		if changed == rel {
			return
		}
	}
	t.Fatalf("expected %s in changed files %#v", rel, result.ChangedFiles)
}

func assertFileContains(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), want) {
		t.Fatalf("expected %s to contain %q, got:\n%s", path, want, string(data))
	}
}

func TestInstrumentDotnetGenericHostBuilder(t *testing.T) {
	root := t.TempDir()
	webCsproj := `<Project Sdk="Microsoft.NET.Sdk.Web">
  <PropertyGroup>
    <TargetFramework>net8.0</TargetFramework>
  </PropertyGroup>
</Project>`
	program := `var builder = Host.CreateApplicationBuilder(args);
var app = builder.Build();
app.Run();`

	mustWrite(t, filepath.Join(root, "App.csproj"), webCsproj)
	mustWrite(t, filepath.Join(root, "Program.cs"), program)

	result, err := Instrument(root, Options{})
	if err != nil {
		t.Fatal(err)
	}

	assertChanged(t, result, "Program.cs")
	assertFileContains(t, filepath.Join(root, "Program.cs"), "builder.Services.AddExtentObservability(builder.Configuration);")
}

func TestInstrumentDotnetOneLinerBuilder(t *testing.T) {
	root := t.TempDir()
	webCsproj := `<Project Sdk="Microsoft.NET.Sdk.Web">
  <PropertyGroup>
    <TargetFramework>net8.0</TargetFramework>
  </PropertyGroup>
</Project>`
	program := `var app = WebApplication.CreateBuilder(args).Build();
app.MapGet("/", () => "OK");
app.Run();`

	mustWrite(t, filepath.Join(root, "App.csproj"), webCsproj)
	mustWrite(t, filepath.Join(root, "Program.cs"), program)

	result, err := Instrument(root, Options{})
	if err != nil {
		t.Fatal(err)
	}

	assertChanged(t, result, "Program.cs")
	progData, err := os.ReadFile(filepath.Join(root, "Program.cs"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(progData)
	if !strings.Contains(content, "var builder = WebApplication.CreateBuilder(args);") {
		t.Fatalf("expected builder declaration, got: %s", content)
	}
	if !strings.Contains(content, "builder.Services.AddExtentObservability(builder.Configuration);") {
		t.Fatalf("expected AddExtentObservability, got: %s", content)
	}
	if !strings.Contains(content, "var app = builder.Build();") {
		t.Fatalf("expected app assignment from builder.Build(), got: %s", content)
	}
}

func TestInstrumentDotnetLegacyStartup(t *testing.T) {
	root := t.TempDir()
	webCsproj := `<Project Sdk="Microsoft.NET.Sdk.Web">
  <PropertyGroup>
    <TargetFramework>net8.0</TargetFramework>
  </PropertyGroup>
</Project>`
	program := `public class Program
{
    public static void Main(string[] args) => CreateHostBuilder(args).Build().Run();
    public static IHostBuilder CreateHostBuilder(string[] args) =>
        Host.CreateDefaultBuilder(args)
            .ConfigureWebHostDefaults(webBuilder =>
            {
                webBuilder.UseStartup<Startup>();
            });
}`
	startup := `public class Startup
{
    public Startup(IConfiguration configuration)
    {
        Configuration = configuration;
    }
    public IConfiguration Configuration { get; }
    public void ConfigureServices(IServiceCollection services)
    {
        services.AddControllers();
    }
}`

	mustWrite(t, filepath.Join(root, "App.csproj"), webCsproj)
	mustWrite(t, filepath.Join(root, "Program.cs"), program)
	mustWrite(t, filepath.Join(root, "Startup.cs"), startup)

	result, err := Instrument(root, Options{})
	if err != nil {
		t.Fatal(err)
	}

	assertChanged(t, result, "Startup.cs")
	assertFileContains(t, filepath.Join(root, "Startup.cs"), "services.AddExtentObservability(Configuration);")

	progAfter, _ := os.ReadFile(filepath.Join(root, "Program.cs"))
	if string(progAfter) != program {
		t.Fatal("expected Program.cs untouched when routing to Startup.cs")
	}

	undoRes, err := Instrument(root, Options{Undo: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(undoRes.Messages) == 0 {
		t.Fatal("expected undo messages")
	}

	startupRestored, _ := os.ReadFile(filepath.Join(root, "Startup.cs"))
	if string(startupRestored) != startup {
		t.Fatalf("expected Startup.cs restored on undo, got: %s", string(startupRestored))
	}
}
