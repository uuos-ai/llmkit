# llmkit-sidecar compatibility matrix

The Go module and sidecar are built from the same release tag. A release
manifest records both versions, the source commit, the supported IPC range,
and SHA-256 digests for every binary archive and SBOM.

| Sidecar release | IPC versions | Host compatibility | Platforms |
|---|---|---|---|
| Unreleased baseline | 1.0 | Hosts that negotiate exactly 1.0 | macOS arm64/amd64, Windows amd64, Linux amd64 |

Protocol `1.0` uses additive optional fields within the same major version.
Breaking field semantics, framing, authentication, or event-order changes
require a new protocol major. Once a second stable minor exists, releases will
support the current and immediately preceding minor unless a documented
security issue requires retirement.

Release archives have signed GitHub/Sigstore provenance and SBOM attestations.
The aggregate `checksums.txt` and `release-manifest.json` are also attested.
Consumers can verify an asset with GitHub CLI:

```sh
gh attestation verify PATH_TO_ASSET --repo uuos-ai/llmkit
```
