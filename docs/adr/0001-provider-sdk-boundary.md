# ADR 0001: Keep llmkit as a capability-oriented Provider SDK

- Status: Accepted
- Date: 2026-07-28

## Context

new-api relay demonstrates broad Provider coverage but couples adapters to a
gateway's HTTP framework, channel database, users, billing, and routing.
RelayKit demonstrates auditable protocol conversion and stream finalization,
but is AGPL-3.0 and exposes gateway-oriented DTOs. TokenHub demonstrates small
capability interfaces and useful retry signals, but returns raw responses and
places routing behavior above its Provider layer.

llmkit must be safe to embed in host applications that retain ownership of
authorization, region consent, routing, pricing, billing, and durable audit.

## Decision

llmkit will:

- expose a small `Provider` interface and optional capability interfaces;
- execute exactly one immutable, host-selected `Target`;
- accept credentials only through a request-scoped handle;
- separate typed host API, Provider codec, shared transport, and stream parser;
- return normalized responses, events, usage, errors, and adaptation reports;
- make retries explicit, bounded, and limited to the same Target;
- require each Provider to pass a reusable conformance suite;
- implement Provider protocols clean-room from official documentation.

llmkit will not:

- select a Provider, model, region, endpoint, or account for the host;
- perform implicit fallback or endpoint round-robin;
- own users, budgets, pricing, billing, or durable state;
- expose raw Provider DTOs as its stable public API;
- read global credentials or emit telemetry without host configuration.

## Consequences

Provider adapters require more normalization work than transparent proxies, but
host applications receive stable semantics and retain policy control. New
Provider features must be represented in typed common APIs, explicit
provider-qualified options, or an observable unsupported/adaptation result.

The core Provider layer remains unchanged by deployment transports. The later
[ADR 0002](./0002-llmkitd-deployment-modes.md) adds optional local-service and
gateway transports while preserving host ownership of business policy.
