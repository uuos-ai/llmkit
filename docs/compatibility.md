# Public API compatibility

## Unreleased baseline change: capability-oriented Provider API

The initial repository skeleton exposed one large `Adapter` interface with
`Invoke`, callback-based `Stream`, and several public `any` fields. Before the
first release, that draft API was replaced with the architecture selected after
the new-api, RelayKit, and TokenHub analysis.

### Replacements

| Initial draft | Adopted API |
|---|---|
| `Adapter` | small `Provider` plus `Generator`, `StreamGenerator`, `Embedder`, `CredentialValidator`, and `ModelLister` capability interfaces |
| `Request.Input any` | typed `GenerateRequest` and `EmbedCall` |
| callback `Adapter.Stream` | pull-based `EventStream` with `Recv` and `Close` |
| `StreamEvent.Delta any` | typed text, reasoning, tool, usage, and finish fields |
| `Response.Output any` | typed `Message` |
| `Usage.ProviderRaw map[string]any` | safe numeric `Usage.Extensions` plus explicit source |
| `RetryAfterMS` | `time.Duration` |
| tool result with only call ID | `ToolResult` with call ID and function name, required by name-correlated protocols such as Gemini |

### Behavioral commitments

- A call targets exactly one host-selected Provider, model, region, and endpoint.
- Credentials are applied through a request-scoped `CredentialHandle`.
- Credential validation and model enumeration are explicit, non-generation
  operations; model pages use opaque provider cursors.
- Catalog results are not shared across credentials unless the host supplies
  the same non-secret authorization scope key.
- Provider DTOs do not appear in the stable host-facing API.
- `io.EOF` means a normally finalized stream; truncation is an error.
- Unsupported or lossy protocol adaptations are explicit.
- Errors, logs, and extensions must not contain credentials or model content.

No released version used the replaced draft, so no deprecation window is
required. Future released public API changes require versioned compatibility
notes and tests.

## Unreleased additive change: routing, identity, and deployment ports

The accepted `llmkitd` baseline adds packages without changing Provider adapter
interfaces:

- `identity`: token-to-tenant/client authentication and context propagation;
- `routing`: unified business/custom catalogs, dynamic defaults, isolated
  session views, and caller-triggered client cache replacement;
- `managed`: external ConfigStore, SecretStore, AuditStore, and custom-provider
  synchronization ports;
- `localstore`: optional SQLite metadata plus OS keyring managed profile;
- `gateway`: normalized HTTPS JSON/SSE transport;
- `runtimeconfig`: strict three-mode startup configuration.

IPC v1 adds optional handshake identity/instance fields, optional `target_id`
fields, and `resolve_provider_options`. Existing explicit raw-target sidecar
calls remain valid. The legacy `llmkit-sidecar` command remains buildable;
release artifacts move to the three-mode `llmkitd` binary before v1.0.
