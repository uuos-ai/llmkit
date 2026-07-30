# llmkit Repository Rules

These rules apply to the entire repository.

1. llmkit is an embeddable Go SDK with one optional `llmkitd` transport binary. `llmkitd` supports sidecar, client-isolated local-service, and remote gateway modes, but must not absorb the embedding product's user, billing, pricing, consent, or routing policy.
2. Core packages must not depend on Gin, Echo, database drivers, web UI frameworks, or a global configuration singleton.
3. Host applications own users, authorization, consent, pricing, billing, routing policy, and gateway storage implementations. Gateway mode requires external ConfigStore, SecretStore, and AuditStore ports.
4. Provider adapters own only provider-specific protocol, authentication headers, endpoint construction, response parsing, usage normalization, and error classification.
5. Network behavior must accept `context.Context`; every request must support timeout and cancellation.
6. Automatic retry and failover must be opt-in and bounded. SDK code must never cross a host-declared provider or region boundary.
7. Secrets must be supplied through interfaces or request-scoped handles and must never appear in errors, logs, fixtures, or snapshots.
8. Public API changes require compatibility notes and tests. Avoid exposing upstream DTOs in stable host-facing interfaces.
9. Each supported Provider requires contract tests for non-streaming, streaming, errors, usage, tool calls, and malformed responses.
10. Do not copy AGPL code into this Apache-2.0 repository. Clean-room implementations may use public protocol documentation and independently written tests.
11. Sidecar and local-service modes expose versioned protected IPC. Gateway mode exposes HTTPS JSON/SSE only, requires per-client authentication and managed custom-provider synchronization, and persists no prompt/response content by default.
12. SDK and sidecar releases must be generated from the same source tag and publish a protocol compatibility matrix plus signed checksums/SBOMs for supported platforms.
