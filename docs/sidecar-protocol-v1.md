# llmkit-sidecar protocol v1

Status: implementation baseline (`1.0`).

## Transport and framing

- macOS/Linux use an absolute Unix domain socket path created with mode `0600`.
- Windows uses a local `\\.\pipe\NAME` named pipe whose ACL grants access only
  to the user running the sidecar. Remote pipe clients are rejected.
- The host passes a random session key of at least 32 bytes through sidecar
  standard input, terminated by one newline. The key is not accepted through a
  flag, environment variable, or file.
- Every message is one four-byte unsigned big-endian length followed by a UTF-8
  JSON object. The default maximum frame is 8 MiB. Zero, oversized, truncated,
  and malformed frames fail closed.
- The first request on each connection must be an authenticated `handshake`.
  Every later request repeats the current process session key and protocol
  version. Authentication comparisons use constant-time comparison.

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
- `list_capabilities`
- `generate` (unary and streaming)
- `embed`

Provider credentials are request-scoped. v1 accepts Bearer credentials or an
explicit allowlisted authentication header (`Authorization`, `x-api-key`, or
`x-goog-api-key`). Credential values are byte arrays in the JSON protocol and
temporary Go buffers are cleared after use.

Custom Provider endpoints are denied unless the embedding host installs an
explicit `EndpointPolicy`. The standalone binary installs no custom endpoint
policy and therefore uses only adapter-owned official defaults.

## Current platform boundary

The command implements Unix-domain-socket and Windows named-pipe runtimes,
requires a host `--parent-pid`, and exits when that process disappears.
ValidateCredential semantics, packaging attestations, and signed release
artifacts remain part of the sidecar acceptance work.

The dependency-free Rust framing/handshake example lives in
`examples/rust-sidecar-client` and is intended for protocol smoke tests rather
than as a production client library.
