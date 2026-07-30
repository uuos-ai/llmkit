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

- `identity`: token-to-client authentication, client-local user binding and context propagation;
- `routing`: unified business/custom available-target snapshots, dynamic defaults, isolated
  session views, and caller-triggered client cache replacement;
- `managed`: external ConfigStore, SecretStore, AuditStore, and custom-provider
  synchronization ports;
- `localstore`: optional SQLite metadata plus OS keyring managed profile;
- `gateway`: normalized HTTPS JSON/SSE transport;
- `runtimeconfig`: strict three-mode startup configuration.

IPC v1 adds optional handshake identity/instance fields, optional `target_id`
fields, `get_available_targets`, and the v1 alias `resolve_provider_options`. Existing explicit raw-target sidecar
calls remain valid. The legacy `llmkit-sidecar` command remains buildable;
release artifacts move to the three-mode `llmkitd` binary before v1.0.

## Unreleased identity and protocol normalization

- `tenant_id` is removed before v1.0. `client_id` is the business isolation key;
  `user_id` is client-local and `binding_version` fences user switches.
- Existing pre-v1 local SQLite files with `tenant_id` are not silently reinterpreted
  as users. Export/re-enroll through the managed API before adopting this schema.
- New local SQLite files carry `schema_metadata.version=1`; unknown future
  versions fail closed. Future migrations must back up first and use one
  transaction before incrementing the version.
- New clients use `get_available_targets` and `/v1/available-targets`; the old
  provider-options names remain v1 aliases during migration.
- Error string values now use the approved normalized vocabulary. Go constant
  compatibility names remain available, but serialized clients must migrate.
- Public streams use `response.*`, `content.*`, `tool_call.*` and `usage.updated`
  events with sequence numbers and a unique terminal state.

## Unreleased additive hardening APIs

- `identity.TokenManagerConfig` accepts `CurrentPepperVersion` and
  `PreviousPeppers`; `TokenManager.RotatePepper` atomically changes the current
  HMAC key. Existing tokens remain valid only while their recorded pepper
  version stays in the retained key ring.
- `managed.Target.ProviderAccount` is an optional, opaque quota dimension. The
  gateway falls back to the Provider ID when the business ConfigStore omits it.
- `managed/httpbackend.NewWithClient` is an additive constructor for custom
  trust roots, service meshes, and offline contract tests. The existing `New`
  constructor continues to require mTLS certificate files.
- Gateway inference leases now include `client_id`, `user_id`, `target_id`, and
  `provider_account`, then commit reported input/output token usage. A stream
  that reaches EOF without a normalized terminal event is accounted and
  audited as `protocol_error`, never as successful completion.
- OpenAI-compatible non-streaming tool `arguments` are now normalized from the
  upstream JSON string into the raw JSON bytes expected by `ToolCall.Arguments`;
  the previous pre-release behavior retained the wire string's quoting and
  escapes.
- `local-service` may now use the external coordination endpoint as a shared
  `UserBindingStore`. Request authentication is fenced by the current binding,
  and binding changes cancel active operations through the store watch.
