# llmkit Constitution

## Core Principles

### I. Library First

llmkit is an embeddable Go Provider SDK. `llmkitd` is an optional transport boundary and MUST reuse the same provider core. Business products retain ownership of users, consent, routing policy, pricing and billing.

### II. Protocol Correctness

Every Provider adapter MUST implement only upstream protocol concerns and MUST normalize requests, stream events, errors and usage into versioned llmkit contracts. Unsupported semantics fail explicitly; lossy adaptation requires an explicit degradation policy and warning.

### III. Isolation and Secret Safety

`client_id` is the primary business isolation boundary. `user_id` is meaningful only within one client. Secrets are request-scoped handles or opaque internal references, never ordinary configuration, logs, fixtures, snapshots or client-visible identifiers. Prompt and response content is not persisted by the core.

### IV. Deterministic Reliability

Every network operation accepts `context.Context`. Retry and fallback are bounded and explicit, never cross host-declared provider/region boundaries, and stop once output begins or request outcome is unknown. Streams have ordered sequence numbers and exactly one terminal event.

### V. Conformance Before Claims

Every built-in Provider requires offline contract tests for requests, non-streaming responses, streaming, tools, errors, usage, cancellation and malformed input. Maturity is `experimental`, `conformant` or time-bounded `verified`; compatibility is never inferred only from an OpenAI-compatible label.

## Architecture and Security Constraints

- One `llmkitd` binary supports `sidecar`, `local-service` and `gateway` via defaults, file, environment and CLI precedence.
- Sidecar is parent-owned and stateless. Local-service is client-isolated and optionally managed. Gateway requires external config, secret, session/binding and audit storage.
- Local transports use protected framed IPC; gateway uses HTTPS JSON/SSE. Control-plane and data-plane authorization are separate.
- Public endpoints enforce SSRF protections. Gateway business APIs use mTLS plus short-lived service authorization.
- Public contracts are versioned independently: native protocol, adapter API, configuration schema and business integration API.
- No AGPL implementation code may be copied into this Apache-2.0 repository.

## Development Workflow and Gates

- Public contract changes require specification updates, compatibility notes and tests.
- `gofmt`, `go test ./...`, `go vet ./...`, race-focused tests, cross-platform builds and workflow linting are release gates.
- Releases publish signed checksums, SBOMs, provenance and a Provider conformance report.
- Storage migrations use expand/migrate/contract for gateway and transactional backup/migration for local SQLite.

## Governance

This constitution supersedes informal design notes. Amendments require an updated version, rationale, migration impact and matching changes to repository rules/specifications. Code review MUST check constitution compliance.

**Version**: 1.0.0 | **Ratified**: 2026-07-30 | **Last Amended**: 2026-07-30
