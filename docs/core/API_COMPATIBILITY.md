# API Compatibility and Deprecation Policy

This policy defines compatibility guarantees for `github.com/monstercameron/GoGRPCBridge`.

## Scope

Primary API scope:
- `pkg/grpctunnel` typed API

Compatibility-only scope:
- `Dial`
- `DialContext`
- `Wrap`
- `Serve`
- `ListenAndServe`

The primary API is the default for all new integrations. Compatibility-only APIs remain available to support migration.

## Semantic Versioning

Releases follow semantic versioning:
- Major version: breaking API or behavior changes
- Minor version: backward-compatible features and additive API fields/options
- Patch version: backward-compatible bugfixes, hardening, and documentation updates

Breaking changes are not introduced in minor or patch releases.

## Compatibility Guarantees

Within a major version:
- Existing exported primary API function signatures remain compatible.
- Existing exported config fields preserve wire-level and behavioral compatibility unless explicitly documented as bugfix corrections.
- New config fields and helper functions are additive and optional.
- Default behavior changes that can affect runtime behavior are called out in changelog and migration notes.

## Deprecation Lifecycle

When an API is deprecated:
1. Add `Deprecated:` notice in GoDoc with replacement API.
2. Add migration mapping in `MIGRATION.md`.
3. Call out the deprecation in release notes and changelog.

Removal policy:
- Deprecated APIs remain available for at least two minor releases and at least 90 days after deprecation notice.
- Deprecated APIs are removed only in a major release.

## Support Window

Maintenance target:
- current minor release
- previous minor release

Security fixes may be backported further when risk severity warrants it.

## Change Control Requirements

Any API-impacting pull request must include:
- compatibility assessment (additive, behavioral, or breaking)
- test updates for changed behavior
- migration notes if consumers need action
- changelog entry for externally visible changes

## CI and Release Enforcement

Before release tags:
- quality gates pass (`go run ./tools/runner.go quality`)
- migration docs are updated for deprecated or moved entry points
- release notes include compatibility and deprecation status
