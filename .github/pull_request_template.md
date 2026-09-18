## Summary

Brief explanation of proposed changes and context.

## Scope

- [ ] Core CLI command or dispatcher
- [ ] Scanner / Analyzer / Contract engine
- [ ] Instrumenter / Codemods / Dependency management
- [ ] Verification / Smoke / Baseline / Score engine
- [ ] Templates / Provisioning artifacts
- [ ] Documentation / Governance / CI

## Verification

List commands executed and results:

```powershell
go test ./...
go vet ./...
go build ./cmd/extent
```

## Report & Telemetry Impact

Describe any changes to telemetry ingestion, metrics, Loki logs, Tempo traces, reports, or contracts.

## PR Checklist

- [ ] Verified locally with `go test ./...` and `go vet ./...`
- [ ] Generated files and templates match repository contracts
- [ ] Target-repository mutations remain safe, bounded, and testable
- [ ] No real secrets, API tokens, or proprietary data in test fixtures or reports
- [ ] Follows conventional commit message formatting
