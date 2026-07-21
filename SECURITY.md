# Security Policy

## Supported Versions

Security fixes land on the latest tagged release. Older tags do not receive backports.

## Reporting a Vulnerability

Report vulnerabilities privately via [GitHub Security Advisories](https://github.com/monstercameron/GoGRPCBridge/security/advisories/new). Do not open a public issue for sensitive details until a fix is available. Reports are acknowledged on a best-effort basis.

## Automated Security Gates

Every push and release runs `gosec` (high severity, high confidence — build-failing) and `govulncheck` (reachable-vulnerability analysis), plus CodeQL analysis on a schedule.

## Security Documentation

- Threat model: [docs/core/THREAT_MODEL.md](./docs/core/THREAT_MODEL.md)
- Security release checklist: [docs/core/SECURITY_RELEASE_CHECKLIST.md](./docs/core/SECURITY_RELEASE_CHECKLIST.md)
- Security fuzz process: [docs/core/SECURITY_FUZZ_PROCESS.md](./docs/core/SECURITY_FUZZ_PROCESS.md)
