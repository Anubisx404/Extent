package instrumenter

import (
	"bytes"
	"errors"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Anubisx404/Extent/internal/fileops"
	"golang.org/x/mod/modfile"
)

var goDependencyPins = pins.Go.Modules

func goPlan(root string, opts Options) ([]fileops.Step, error) {
	goModPath := filepath.Join(root, "go.mod")
	module := goModulePath(goModPath)
	if strings.TrimSpace(module) == "" {
		return nil, errors.New("go.mod has no valid module path")
	}
	goModBefore, err := os.ReadFile(goModPath)
	if err != nil {
		return nil, err
	}
	goModAfter, err := pinGoDependencies(goModBefore)
	if err != nil {
		return nil, err
	}
	steps := []fileops.Step{}
	if !bytes.Equal(goModBefore, goModAfter) {
		steps = append(steps, fileops.Step{Path: "go.mod", Action: fileops.Update, Data: goModAfter, Mode: 0644})
	}
	ep, e := selectEntrypoint(root, opts, goEntrypoints(root))
	if e != nil {
		return nil, e
	}
	bootstrap := goBootstrap
	if ep != "" {
		if src, err := os.ReadFile(filepath.Join(root, ep)); err == nil && strings.Contains(string(src), strconv.Quote("os/signal")) {
			bootstrap = goBootstrapWithoutSignalHandler()
		}
	}
	rel := "internal/observability/otel.go"
	if old, e := os.ReadFile(filepath.Join(root, rel)); e != nil || string(old) != bootstrap {
		a := fileops.Create
		if e == nil {
			a = fileops.Update
		}
		steps = append(steps, fileops.Step{Path: rel, Action: a, Data: []byte(bootstrap), Mode: 0644})
	}
	if ep != "" {
		old, _ := os.ReadFile(filepath.Join(root, ep))
		imp := module + "/internal/observability"
		if !strings.Contains(string(old), strconv.Quote(imp)) || !strings.Contains(string(old), "extentotel.Shutdown()") {
			tmp := filepath.Join(root, ep)
			if e := addGoObservabilityLifecyclePure(tmp, imp, &old); e != nil {
				return nil, e
			}
			steps = append(steps, fileops.Step{Path: ep, Action: fileops.Update, Data: old, Mode: 0644})
		}
	}
	return steps, nil
}

func pinGoDependencies(data []byte) ([]byte, error) {
	parsed, err := modfile.Parse("go.mod", data, nil)
	if err != nil {
		return nil, fmt.Errorf("parse go.mod: %w", err)
	}
	existing := make(map[string]string, len(parsed.Require))
	for _, requirement := range parsed.Require {
		existing[requirement.Mod.Path] = requirement.Mod.Version
	}
	for _, pin := range goDependencyPins {
		if version, ok := existing[pin.Path]; ok {
			if version != pin.Version {
				return nil, fmt.Errorf("incompatible Go OpenTelemetry requirement %s %s; expected %s", pin.Path, version, pin.Version)
			}
			continue
		}
		if err := parsed.AddRequire(pin.Path, pin.Version); err != nil {
			return nil, fmt.Errorf("pin %s: %w", pin.Path, err)
		}
	}
	formatted, err := parsed.Format()
	if err != nil {
		return nil, fmt.Errorf("format go.mod: %w", err)
	}
	return formatted, nil
}

func addGoObservabilityLifecyclePure(path, imp string, out *[]byte) error {
	if strings.TrimSpace(imp) == "" {
		return errors.New("empty Go import path")
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, *out, parser.ParseComments)
	if err != nil {
		return err
	}
	quotedImport := strconv.Quote(imp)
	foundImport := false
	for _, existing := range f.Imports {
		if existing.Path.Value == quotedImport {
			existing.Name = ast.NewIdent("extentotel")
			foundImport = true
			break
		}
	}
	if !foundImport {
		spec := &ast.ImportSpec{Name: ast.NewIdent("extentotel"), Path: &ast.BasicLit{Kind: token.STRING, Value: quotedImport}}
		for _, declaration := range f.Decls {
			if imports, ok := declaration.(*ast.GenDecl); ok && imports.Tok == token.IMPORT {
				imports.Specs = append(imports.Specs, spec)
				foundImport = true
				break
			}
		}
		if !foundImport {
			f.Decls = append([]ast.Decl{&ast.GenDecl{Tok: token.IMPORT, Specs: []ast.Spec{spec}}}, f.Decls...)
		}
	}
	foundMain := false
	for _, declaration := range f.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if ok && function.Recv == nil && function.Name.Name == "main" && function.Body != nil {
			shutdown := &ast.DeferStmt{Call: &ast.CallExpr{Fun: &ast.SelectorExpr{X: ast.NewIdent("extentotel"), Sel: ast.NewIdent("Shutdown")}}}
			function.Body.List = append([]ast.Stmt{shutdown}, function.Body.List...)
			foundMain = true
			break
		}
	}
	if !foundMain {
		return errors.New("go entrypoint has no main function")
	}
	var output bytes.Buffer
	if err := format.Node(&output, fset, f); err != nil {
		return err
	}
	*out = output.Bytes()
	return nil
}
func goEntrypoints(root string) []string {
	r := []string{"main.go"}
	for _, d := range []string{"cmd"} {
		ents, _ := os.ReadDir(filepath.Join(root, d))
		for _, e := range ents {
			if e.IsDir() {
				r = append(r, "cmd/"+e.Name()+"/main.go")
			}
		}
	}
	return r
}

func goModulePath(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "module" {
			return fields[1]
		}
	}
	return ""
}

const goBootstrap = `// Package observability initializes OpenTelemetry for the service.
// Generated by Extent. Keep this package imported before application code.
//
// Shutdown flushes traces and metrics. It runs from the deferred call in main and
// from a SIGINT/SIGTERM handler. log.Fatal, os.Exit and panics skip deferred calls,
// so return errors from main instead of calling log.Fatal, and let main return
// normally so the deferred Shutdown runs.
package observability

import (
	"context"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

var (
	provider      *sdktrace.TracerProvider
	meterProvider *sdkmetric.MeterProvider
	shutdownOnce  sync.Once
)

func init() {
	serviceName := os.Getenv("OTEL_SERVICE_NAME")
	if serviceName == "" {
		serviceName = "go-service"
	}
	res := resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceName(serviceName),
	)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))

	traceExporter, err := otlptracehttp.New(context.Background())
	if err != nil {
		log.Printf("extent: failed to create OTLP trace exporter: %v", err)
	} else {
		provider = sdktrace.NewTracerProvider(
			sdktrace.WithBatcher(traceExporter),
			sdktrace.WithResource(res),
		)
		otel.SetTracerProvider(provider)
	}

	metricExporter, err := otlpmetrichttp.New(context.Background())
	if err != nil {
		log.Printf("extent: failed to create OTLP metric exporter: %v", err)
	} else {
		meterProvider = sdkmetric.NewMeterProvider(
			sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExporter)),
			sdkmetric.WithResource(res),
		)
		otel.SetMeterProvider(meterProvider)
	}

	go shutdownOnSignal()
}

// shutdownOnSignal flushes telemetry on SIGINT or SIGTERM and exits with the
// conventional status (130 for SIGINT, 143 for SIGTERM). Because Notify is
// registered here, an application that handles these signals itself should
// finish its own shutdown before the signal reaches this goroutine.
func shutdownOnSignal() {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	sig := <-signals
	Shutdown()
	if sig == syscall.SIGINT {
		os.Exit(130)
	}
	os.Exit(143)
}

// Shutdown flushes and stops the tracer and meter providers. It is safe to call
// more than once; only the first call does work.
func Shutdown() {
	shutdownOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if meterProvider != nil {
			if err := meterProvider.Shutdown(ctx); err != nil {
				log.Printf("extent: failed to shut down OpenTelemetry metrics: %v", err)
			}
		}
		if provider != nil {
			if err := provider.Shutdown(ctx); err != nil {
				log.Printf("extent: failed to shut down OpenTelemetry traces: %v", err)
			}
		}
	})
}
`

// goBootstrapWithoutSignalHandler is used when the application already imports
// os/signal. Its own handler would race the generated one, which exits the
// process and could cut a graceful shutdown short, so the application's
// deferred Shutdown call is left to flush telemetry.
func goBootstrapWithoutSignalHandler() string {
	return strings.Replace(goBootstrap, "\tgo shutdownOnSignal()\n", "\t// The application handles SIGINT/SIGTERM itself, so Extent installs no signal handler.\n\t_ = shutdownOnSignal\n", 1)
}
