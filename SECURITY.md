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
- The optional sidecar may use only protected local IPC and must not persist
  credentials or model content.
