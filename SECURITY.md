# Security policy

## Reporting

Do not open a public issue for a suspected vulnerability involving credential
exposure, request-content disclosure, authentication bypass, or unsafe network
behavior. Use GitHub's private security advisory workflow for this repository.

## Supported versions

llmkit has not published its first stable release. Security fixes currently
target the default branch. A supported-version table will be published with the
first release.

## Security boundaries

- Credentials are host-owned and request-scoped.
- Errors and logs must not contain credentials or model content.
- Provider endpoints and regions are host-selected and must not change
  implicitly.
- Codec code must not perform implicit network fetches.
- Streaming and non-streaming response bodies are bounded.
- Cancellation must stop network work and must not trigger automatic retry.
- Sidecar and local-service use protected local IPC. Local-service authenticates
  every client token into an isolated tenant/client identity.
- Gateway binds HTTPS only, forces managed custom-provider synchronization, and
  requires external ConfigStore, SecretStore, and AuditStore implementations.
- Prompt/response content is not persisted by default. Secrets remain
  request-scoped or are opened from a Vault/KMS-backed SecretStore reference.
