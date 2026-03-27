# Release Checklist

Use this checklist before creating or publishing a release tag.

## Quality

- [ ] `go run ./tools/runner.go quality` passed on release candidate commit.
- [ ] `go run ./tools/runner.go quality-trend` ran and produced `bin/quality/trend.json`.
- [ ] `go run ./tools/api_compat_guard check` passed.
- [ ] `go run ./tools/runner.go canonical-publish-check` passed.
- [ ] Required CI lanes passed (`lint`, `unit`, `wasm`, `browser`, integration smoke, full gate where applicable).
- [ ] Canonical clean-consumer smoke passes: `go mod init <tmp> && go get github.com/monstercameron/GoGRPCBridge@latest`.

## Documentation

- [ ] README and docs index links are valid and current.
- [ ] Troubleshooting entries reflect current failure modes.
- [ ] Threat model and auth boundary docs reflect current architecture.

## Performance

- [ ] Baseline and trend files are reviewed (`benchmarks/quality_baseline.json`, `bin/quality/trend.json`).
- [ ] Release notes include `RELEASE_PERFORMANCE_TEMPLATE.md` section with filled deltas.
- [ ] Regressions are documented with owner, mitigation plan, and target fix release.

## Security

- [ ] `SECURITY_RELEASE_CHECKLIST.md` is fully signed off.
- [ ] No unresolved high-severity/high-confidence security findings block release.
- [ ] TLS/WSS and origin policy guidance remains production-safe and unchanged unless explicitly noted.
- [ ] Release artifacts include checksum and verifiable provenance attestation.
- [ ] Default branch protection enforces required status checks and CODEOWNERS review.

## Migration and Compatibility

- [ ] `MIGRATION.md` updated for any changed or deprecated API usage.
- [ ] `API_COMPATIBILITY.md` policy remains accurate for this release.
- [ ] `api_compatibility_baseline.json` reviewed and updated only when intended API changes are approved.
- [ ] Release notes include compatibility-impact summary.

## Final Approval

- [ ] Changelog entry is complete.
- [ ] Release owner sign-off recorded.
- [ ] API owner sign-off recorded.
- [ ] Security owner sign-off recorded.
