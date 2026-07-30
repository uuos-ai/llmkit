# ADR 0002: One llmkitd binary with three deployment modes

- Status: Accepted
- Date: 2026-07-30

## Decision

One `llmkitd` binary supports `sidecar` (default), `local-service`, and
`gateway`. Configuration selects the mode using strict file settings,
environment variables, or CLI overrides. All modes share the same Provider
registry, normalized types, errors, usage, streaming semantics, and request
cancellation.

Sidecar retains one launch-scoped secret and parent-process supervision.
Local-service uses one token per client and isolates tenant/client catalog
state over protected local IPC. Gateway uses HTTPS JSON/SSE, one token per
client, managed custom-provider synchronization, and mandatory external
ConfigStore, SecretStore, and AuditStore implementations.

Provider/model selection is represented by a catalog `target_id`. An explicit
ID is strict. If omitted, the current default captured by the client's latest
catalog refresh is selected. Business built-ins and user custom targets are
always returned through the same catalog API.

## Boundary

The gateway is not a product control plane. It does not own users, billing,
pricing, consent, quotas, or business routing rules. External stores and the
business catalog remain platform responsibilities. Provider adapters still
execute exactly one resolved immutable target and never perform implicit
cross-provider failover.

## Consequences

Applications can move between in-process SDK, local daemon, and remote gateway
without maintaining separate Provider integrations. Deployments must operate
the external gateway stores and TLS trust boundary. The legacy
`llmkit-sidecar` command remains temporarily compatible; new deployments use
`llmkitd`.
