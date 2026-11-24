package instrumenter

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Anubisx404/Extent/internal/fileops"
	"github.com/Anubisx404/Extent/internal/scanner"
)

func TestInstrumentTypeScriptEntrypointsCJSAndESM(t *testing.T) {
	cases := []struct {
		name           string
		entrypoint     string
		pkgJSON        string
		wantBootFile   string
		wantImportLine string
	}{
		{
			name:           "root server.ts CJS",
			entrypoint:     "server.ts",
			pkgJSON:        "{\n  \"name\": \"ts-cjs\",\n  \"dependencies\": {\n    \"express\": \"^4.19.0\"\n  }\n}\n",
			wantBootFile:   "extent.instrumentation.js",
			wantImportLine: `import "./extent.instrumentation"; // extent:otel`,
		},
		{
			name:           "root index.ts ESM",
			entrypoint:     "index.ts",
			pkgJSON:        "{\n  \"name\": \"ts-esm\",\n  \"type\": \"module\",\n  \"dependencies\": {\n    \"express\": \"^4.19.0\"\n  }\n}\n",
			wantBootFile:   "extent.instrumentation.mjs",
			wantImportLine: `import "./extent.instrumentation.mjs"; // extent:otel`,
		},
		{
			name:           "nested src/server.ts CJS",
			entrypoint:     "src/server.ts",
			pkgJSON:        "{\n  \"name\": \"ts-nested-cjs\",\n  \"dependencies\": {\n    \"express\": \"^4.19.0\"\n  }\n}\n",
			wantBootFile:   "extent.instrumentation.js",
			wantImportLine: `import "../extent.instrumentation"; // extent:otel`,
		},
		{
			name:           "nested src/main.ts ESM",
			entrypoint:     "src/main.ts",
			pkgJSON:        "{\n  \"name\": \"ts-nested-esm\",\n  \"type\": \"module\",\n  \"dependencies\": {\n    \"express\": \"^4.19.0\"\n  }\n}\n",
			wantBootFile:   "extent.instrumentation.mjs",
			wantImportLine: `import "../extent.instrumentation.mjs"; // extent:otel`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			pkgPath := filepath.Join(root, "package.json")
			if err := os.WriteFile(pkgPath, []byte(tc.pkgJSON), 0644); err != nil {
				t.Fatal(err)
			}
			epPath := filepath.Join(root, filepath.FromSlash(tc.entrypoint))
			if err := os.MkdirAll(filepath.Dir(epPath), 0755); err != nil {
				t.Fatal(err)
			}
			origSrc := "import express from \"express\";\nconst app = express();\n"
			if err := os.WriteFile(epPath, []byte(origSrc), 0644); err != nil {
				t.Fatal(err)
			}

			res, err := Instrument(root, Options{})
			if err != nil {
				t.Fatalf("Instrument failed: %v", err)
			}
			if len(res.ChangedFiles) == 0 {
				t.Fatal("expected changed files")
			}

			bootPath := filepath.Join(root, tc.wantBootFile)
			if _, err := os.Stat(bootPath); err != nil {
				t.Fatalf("expected bootstrap file %s: %v", tc.wantBootFile, err)
			}
			gotSrcBytes, err := os.ReadFile(epPath)
			if err != nil {
				t.Fatal(err)
			}
			gotSrc := string(gotSrcBytes)
			if !strings.HasPrefix(gotSrc, tc.wantImportLine+"\n") {
				t.Fatalf("expected %q at top of %s, got:\n%s", tc.wantImportLine, tc.entrypoint, gotSrc)
			}

			second, err := Instrument(root, Options{})
			if err != nil {
				t.Fatalf("second Instrument failed: %v", err)
			}
			if len(second.ChangedFiles) != 0 {
				t.Fatalf("expected idempotent second run, got changed files: %v", second.ChangedFiles)
			}

			if _, err := Instrument(root, Options{Undo: true}); err != nil {
				t.Fatalf("Undo failed: %v", err)
			}
			restoredSrc, err := os.ReadFile(epPath)
			if err != nil {
				t.Fatal(err)
			}
			if string(restoredSrc) != origSrc {
				t.Fatalf("expected exact source restoration, got:\n%s", string(restoredSrc))
			}
			restoredPkg, err := os.ReadFile(pkgPath)
			if err != nil {
				t.Fatal(err)
			}
			if string(restoredPkg) != tc.pkgJSON {
				t.Fatalf("expected exact package.json restoration, got:\n%s", string(restoredPkg))
			}
			if _, err := os.Stat(bootPath); !os.IsNotExist(err) {
				t.Fatalf("expected %s to be removed on undo", tc.wantBootFile)
			}
		})
	}
}

func TestInstrumentPyprojectPEP621AndPoetry(t *testing.T) {
	t.Run("PEP 621 project.dependencies", func(t *testing.T) {
		root := t.TempDir()
		origPyproject := "[project]\nname = \"svc\"\nversion = \"0.1.0\"\ndependencies = [\n  \"fastapi==0.115.0\",\n]\n"
		origMain := "from fastapi import FastAPI\napp = FastAPI()\n"
		if err := os.WriteFile(filepath.Join(root, "pyproject.toml"), []byte(origPyproject), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "main.py"), []byte(origMain), 0644); err != nil {
			t.Fatal(err)
		}

		res, err := Instrument(root, Options{})
		if err != nil {
			t.Fatalf("Instrument failed: %v", err)
		}
		if len(res.ChangedFiles) == 0 {
			t.Fatal("expected changed files")
		}
		updated, err := os.ReadFile(filepath.Join(root, "pyproject.toml"))
		if err != nil {
			t.Fatal(err)
		}
		updatedText := string(updated)
		if !strings.Contains(updatedText, `"opentelemetry-api==`+pythonDependencyPins["opentelemetry-api"]+`",`) {
			t.Fatalf("missing opentelemetry-api pin in pyproject.toml:\n%s", updatedText)
		}
		if !strings.Contains(updatedText, `"opentelemetry-instrumentation-fastapi==`+pythonDependencyPins["opentelemetry-instrumentation-fastapi"]+`",`) {
			t.Fatalf("missing fastapi instrumentation in pyproject.toml:\n%s", updatedText)
		}

		second, err := Instrument(root, Options{})
		if err != nil {
			t.Fatalf("second Instrument failed: %v", err)
		}
		if len(second.ChangedFiles) != 0 {
			t.Fatalf("expected idempotent second run, got: %v", second.ChangedFiles)
		}

		if _, err := Instrument(root, Options{Undo: true}); err != nil {
			t.Fatalf("Undo failed: %v", err)
		}
		restored, err := os.ReadFile(filepath.Join(root, "pyproject.toml"))
		if err != nil {
			t.Fatal(err)
		}
		if string(restored) != origPyproject {
			t.Fatalf("expected exact pyproject.toml restoration, got:\n%s", string(restored))
		}
	})

	t.Run("Poetry tool.poetry.dependencies", func(t *testing.T) {
		root := t.TempDir()
		origPyproject := "[tool.poetry]\nname = \"svc\"\n\n[tool.poetry.dependencies]\npython = \"^3.11\"\nflask = \"^3.0.0\"\n"
		origMain := "from flask import Flask\napp = Flask(__name__)\n"
		if err := os.WriteFile(filepath.Join(root, "pyproject.toml"), []byte(origPyproject), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "app.py"), []byte(origMain), 0644); err != nil {
			t.Fatal(err)
		}

		if _, err := Instrument(root, Options{}); err != nil {
			t.Fatalf("Instrument failed: %v", err)
		}
		updated, err := os.ReadFile(filepath.Join(root, "pyproject.toml"))
		if err != nil {
			t.Fatal(err)
		}
		updatedText := string(updated)
		if !strings.Contains(updatedText, `opentelemetry-api = "==`+pythonDependencyPins["opentelemetry-api"]+`"`) {
			t.Fatalf("missing poetry opentelemetry-api pin:\n%s", updatedText)
		}
		if !strings.Contains(updatedText, `opentelemetry-instrumentation-flask = "==`+pythonDependencyPins["opentelemetry-instrumentation-flask"]+`"`) {
			t.Fatalf("missing poetry flask instrumentation:\n%s", updatedText)
		}

		second, err := Instrument(root, Options{})
		if err != nil {
			t.Fatalf("second Instrument failed: %v", err)
		}
		if len(second.ChangedFiles) != 0 {
			t.Fatalf("expected idempotent second run, got: %v", second.ChangedFiles)
		}

		if _, err := Instrument(root, Options{Undo: true}); err != nil {
			t.Fatalf("Undo failed: %v", err)
		}
		restored, err := os.ReadFile(filepath.Join(root, "pyproject.toml"))
		if err != nil {
			t.Fatal(err)
		}
		if string(restored) != origPyproject {
			t.Fatalf("expected exact Poetry pyproject.toml restoration, got:\n%s", string(restored))
		}
	})

	t.Run("Rejects incompatible OTel pin in PEP 621 and Poetry", func(t *testing.T) {
		pepBad := "[project]\ndependencies = [\n  \"opentelemetry-api>=1.0.0\",\n]\n"
		if _, err := mergePyprojectDependencies(pepBad, pythonDependencies); err == nil {
			t.Fatal("expected error for incompatible PEP 621 pin")
		}
		poetryBad := "[tool.poetry.dependencies]\nopentelemetry-api = \"^1.0.0\"\n"
		if _, err := mergePyprojectDependencies(poetryBad, pythonDependencies); err == nil {
			t.Fatal("expected error for incompatible Poetry pin")
		}
	})
}

func TestPythonDjangoDetectionAndWSGI(t *testing.T) {
	t.Run("manage.py entrypoint and DjangoInstrumentor", func(t *testing.T) {
		root := t.TempDir()
		origManage := "#!/usr/bin/env python\nimport os\nos.environ.setdefault('DJANGO_SETTINGS_MODULE', 'myproj.settings')\n"
		origReq := "django==5.1.0\n"
		if err := os.WriteFile(filepath.Join(root, "manage.py"), []byte(origManage), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "requirements.txt"), []byte(origReq), 0644); err != nil {
			t.Fatal(err)
		}

		steps, err := pythonPlan(root, Options{})
		if err != nil {
			t.Fatalf("pythonPlan failed: %v", err)
		}
		if _, err := fileops.Apply(fileops.Plan{Root: root, Kind: "instrument/bootstrap", Steps: steps}); err != nil {
			t.Fatalf("Apply failed: %v", err)
		}

		boot, err := os.ReadFile(filepath.Join(root, "extent_instrumentation.py"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(boot), "DjangoInstrumentor") {
			t.Fatalf("expected DjangoInstrumentor in extent_instrumentation.py")
		}
		req, err := os.ReadFile(filepath.Join(root, "requirements.txt"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(req), "opentelemetry-instrumentation-django==") {
			t.Fatalf("expected opentelemetry-instrumentation-django in requirements.txt:\n%s", string(req))
		}
		manage, err := os.ReadFile(filepath.Join(root, "manage.py"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(manage), "import extent_instrumentation  # extent:otel") {
			t.Fatalf("expected extent_instrumentation import in manage.py:\n%s", string(manage))
		}

		steps2, err := pythonPlan(root, Options{})
		if err != nil {
			t.Fatalf("second pythonPlan failed: %v", err)
		}
		if len(steps2) != 0 {
			t.Fatalf("expected idempotent second pythonPlan, got %d steps", len(steps2))
		}

		if err := fileops.UndoKind(root, "instrument/bootstrap"); err != nil {
			t.Fatalf("UndoKind failed: %v", err)
		}
		restoredManage, err := os.ReadFile(filepath.Join(root, "manage.py"))
		if err != nil {
			t.Fatal(err)
		}
		if string(restoredManage) != origManage {
			t.Fatalf("expected exact manage.py restoration, got:\n%s", string(restoredManage))
		}
	})

	t.Run("one-level wsgi.py fallback and explicit entrypoint validation", func(t *testing.T) {
		root := t.TempDir()
		if err := os.MkdirAll(filepath.Join(root, "myproj"), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "myproj", "wsgi.py"), []byte("from django.core.wsgi import get_wsgi_application\n"), 0644); err != nil {
			t.Fatal(err)
		}
		ep, err := selectPythonEntrypoint(root, Options{})
		if err != nil {
			t.Fatalf("selectPythonEntrypoint failed: %v", err)
		}
		if ep != "myproj/wsgi.py" {
			t.Fatalf("expected myproj/wsgi.py, got %q", ep)
		}
		if _, err := selectPythonEntrypoint(root, Options{Entrypoint: "../evil.py"}); err == nil {
			t.Fatal("expected traversal entrypoint to be rejected")
		}
	})
}

func TestScannerPyprojectAndNewEntrypoints(t *testing.T) {
	t.Run("uv pyproject without lockfile", func(t *testing.T) {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, "pyproject.toml"), []byte("[project]\nname = \"app\"\n[tool.uv]\ndev-dependencies = []\n"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "manage.py"), []byte("django\n"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "main.ts"), []byte("console.log('hi')\n"), 0644); err != nil {
			t.Fatal(err)
		}
		res, err := scanner.Scan(root)
		if err != nil {
			t.Fatal(err)
		}
		if len(res.PackageManagers) != 1 || res.PackageManagers[0] != "uv" {
			t.Fatalf("expected packageManagers=[uv], got %v", res.PackageManagers)
		}
		joinedEPs := strings.Join(res.Entrypoints, ",")
		if !strings.Contains(joinedEPs, "manage.py") || !strings.Contains(joinedEPs, "main.ts") {
			t.Fatalf("expected manage.py and main.ts in entrypoints, got %v", res.Entrypoints)
		}
	})

	t.Run("pep621 pyproject falls back to pip-pyproject", func(t *testing.T) {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, "pyproject.toml"), []byte("[project]\nname = \"app\"\ndependencies = [\"django\"]\n"), 0644); err != nil {
			t.Fatal(err)
		}
		res, err := scanner.Scan(root)
		if err != nil {
			t.Fatal(err)
		}
		if len(res.PackageManagers) != 1 || res.PackageManagers[0] != "pip-pyproject" {
			t.Fatalf("expected packageManagers=[pip-pyproject], got %v", res.PackageManagers)
		}
	})
}
