# llmkit-sidecar protocol v1

Status: implementation baseline (`1.0`).

## Transport and framing

- macOS/Linux use an absolute Unix domain socket path created with mode `0600`.
- Windows uses a local `\\.\pipe\NAME` named pipe whose ACL grants access only
  to the user running the sidecar. Remote pipe clients are rejected.
- Sidecar mode passes a random session key of at least 32 bytes through
  standard input, terminated by one newline. The key is not accepted through a
  flag, environment variable, or file.
- Every message is one four-byte unsigned big-endian length followed by a UTF-8
  JSON object. The default maximum frame is 8 MiB. Zero, oversized, truncated,
  and malformed frames fail closed.
- The first request on each connection must be an authenticated `handshake`.
  Every later request repeats the current token and protocol version.
  Local-service maps each token family to a trusted client identity and client-local current user binding.

After refresh or user switching, the previous access token cannot execute data
operations. For a lost response, a client may reconnect within the 60-second
idempotency window using handshake `purpose: "token_recovery"`; that session is
hard-restricted to `refresh_token` and `bind_user`. Exact idempotency keys return
the encrypted cached token/binding result, while a different refresh key is
treated as replay.

## Envelope

Requests contain `version`, `session_key`, `request_id`, `method`, and optional
`payload`. Responses contain `version`, `request_id`, `type`, and either a
typed `payload` or normalized `error`.

Response types are:

- `result`: one unary result;
- `event`: one normalized streaming event;
- `end`: normal completion of a stream;
- `error`: safe normalized failure; a stream receiving this does not also
  receive `end`.

`request_id` values must be unique among active requests on a connection.
`cancel` targets one active request ID. Event frames are written synchronously,
so a slow local consumer applies bounded backpressure to upstream `Recv`.

## Implemented methods

- `handshake`
- `health`
- `shutdown`
- `cancel`
- `enroll_client` (local-service bootstrap only)
- `refresh_token` (local-service)
- `bind_user` (local-service; returns a replacement token pair)
- `list_capabilities`
- `list_models`
- `validate_credential`
- `resolve_provider_options`
- `upsert_custom_provider` (managed local-service only)
- `delete_custom_provider` (managed local-service only)
- `generate` (unary and streaming)
- `embed`
- `rerank`
- `moderate`

`validate_credential` performs the provider's non-generation model discovery
request (or model metadata request for Gemini). Success means that the
request-scoped credential was accepted for the selected provider endpoint;
authentication, permission, model, throttling, and transport failures use the
same normalized error envelope as generation.

`list_models` performs host-triggered, credential-scoped provider catalog
enumeration and returns a normalized page plus an opaque provider cursor. The
SDK never refreshes catalogs in the background or selects a returned model.
Hosts that use `catalog.ModelCache` must provide a non-secret scope identifier
so results cannot cross credential or authorization boundaries.

Provider credentials are request-scoped. v1 accepts Bearer credentials or an
explicit allowlisted authentication header (`Authorization`, `x-api-key`, or
`x-goog-api-key`). Credential values are byte arrays in the JSON protocol and
temporary Go buffers are cleared after use.

`resolve_provider_options` returns business built-ins plus client-local custom
Provider/target combinations, the current `default_target_id`, a `revision`,
and `generated_at`. In disabled-sync mode the request may carry non-secret
`local_custom_providers` (A2); they remain only in that client's in-memory
view. Later calls may provide strict `target_id`; omission uses the default
from that client's latest refresh.

Custom Provider endpoints are denied unless the embedding host installs an
explicit `EndpointPolicy`. `llmkitd` installs a public-HTTPS policy; embedding
hosts should replace it with a business allowlist. The legacy
`llmkit-sidecar` command continues to deny custom endpoints.

## Current platform boundary

`llmkitd` implements Unix-domain-socket and Windows named-pipe runtimes.
Sidecar mode requires a host `--parent-pid` and exits with that process;
local-service remains resident for multiple isolated clients.
Tagged releases build all supported targets with SHA-256 checksums, SPDX JSON
SBOMs, a machine-readable release manifest, and signed GitHub/Sigstore
provenance and SBOM attestations. See `docs/sidecar-compatibility.md`.

The Rust crate in `examples/rust-sidecar-client` now exposes a reusable typed,
bounded synchronous protocol client over any `Read + Write` transport, plus a
Unix socket executable. Canonical JSON Schema and generated TypeScript/Python
DTOs live under `schema/` and `sdk/`.
