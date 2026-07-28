# Contributing to llmkit

## Development checks

Before opening a pull request:

```sh
gofmt -w .
go vet ./...
go test -race ./...
go build ./...
```

Public API changes require:

- an entry in `docs/compatibility.md`;
- tests covering old or intentionally changed behavior;
- an update to the Provider capability matrix when support changes.

## Provider implementations

Provider adapters must:

- accept `context.Context` for every network operation;
- use the shared transport and request-scoped `CredentialHandle`;
- target exactly one host-selected Provider, model, region, and endpoint;
- return typed responses, stream events, usage, and `ProviderError`;
- reject unknown options and unsupported capabilities explicitly;
- pass the reusable conformance suite;
- include fixtures for non-streaming, streaming, tools, usage, errors,
  cancellation, malformed responses, and secret redaction.

Adapters must not add routing, billing, durable storage, global configuration,
or implicit cross-Provider fallback.

## Clean-room protocol work

llmkit is Apache-2.0. Do not copy or adapt AGPL implementation code, fixtures,
or snapshots. Provider behavior must be implemented from official public
protocol documentation and independently authored tests. Record the applicable
documentation URL and retrieval date in the Provider package documentation or
compatibility matrix.

Never commit real credentials, prompts, model responses, account identifiers,
or production request IDs.
