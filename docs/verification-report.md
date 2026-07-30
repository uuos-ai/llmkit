# Verification and fault-injection report

Baseline: 2026-07-30, Go 1.24, macOS arm64 (Apple M2 Max). CI repeats the
portable checks on Ubuntu, macOS, and Windows; tagged builds additionally
cross-compile all release targets.

## Automated gates

| Area | Verification |
|---|---|
| Provider contracts | Offline generation, stream, usage, error, malformed response, tool and structured-output fixtures |
| Transport faults | Cancellation is non-retryable; deadline is normalized retryable timeout; error bodies and success JSON are bounded |
| Stream faults | Early EOF is malformed, terminal event ordering is required, oversized SSE events fail closed |
| IPC security | Constant-time session authentication, 8 MiB frame bound, malformed/truncated/oversized frame rejection, custom endpoint denial |
| IPC lifecycle | Concurrent cancellation, synchronous stream backpressure, parent monitoring, health and graceful shutdown |
| Client isolation | Per-token tenant/client identity, cross-client catalog rejection, atomic client-cache replacement |
| Routing | Strict explicit target IDs, dynamic default resolution, A2 non-secret local catalog merge |
| Gateway | Bearer authentication, external target/secret resolution, normalized JSON response and audit metadata |
| Configuration | Strict YAML/JSON, unknown-field rejection, default/file/env/CLI precedence, gateway fail-closed validation |
| Optional local state | SQLite CRUD/isolation, OS-keyring mock, stored request-scoped credential opening and deletion |
| Local runtime | Real child-process handshake/health/shutdown test over Unix socket and Windows named pipe |
| Memory/concurrency | `go test -race ./...`; model/capability refresh coalescing and clone isolation |
| Fuzzing | Continuous smoke fuzzing for sidecar framing and SSE decoding in CI |
| Supply chain | Four-target `llmkitd` builds, SHA-256, SPDX SBOM, release manifest, signed GitHub/Sigstore attestations |

Tests use synthetic markers rather than real credentials or model content and
assert that upstream bodies do not appear in public errors.

## Microbenchmark snapshot

These numbers are a regression reference, not a service-level objective. They
measure local parsing/framing only and exclude provider network latency.

| Benchmark | Result | Allocations |
|---|---:|---:|
| Sidecar codec request round trip | 1,363 ns/op | 936 B/op, 17 allocs/op |
| Normalized SSE fixture decode | 11,339 ns/op | 8,224 B/op, 386 allocs/op |

Reproduce with:

```sh
go test ./sidecar/protocol ./stream/sse -run '^$' -bench . -benchmem
```

## Externally blocked acceptance

- Live Provider calls require project-owned test accounts, regional consent,
  API keys, and budget limits.
- Apple Developer ID notarization and Windows Authenticode require signing
  identities and protected CI secrets. Keyless GitHub/Sigstore artifact
  attestations are implemented independently of those platform certificates.
