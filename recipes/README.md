# Extent Recipes

Recipes describe framework-specific detection and instrumentation intent. The Go
engine can use these files as stable contracts for future AST/codemod patchers
without hardcoding every framework rule in the CLI.

Each recipe keeps the same shape:

```yaml
name: express
runtime: node
detect:
  files:
    - package.json
  dependencies:
    - express
entrypoint:
  patterns:
    - src/index.ts
inject:
  bootstrap:
    file: extent.instrumentation.js
metrics:
  http_red: true
verify:
  request_path: /health
```
