# Runtime schema artifacts

`runtime-v1.schema.json` is the language-neutral public DTO schema for target
snapshots, token pairs, usage, errors, and normalized stream events.

Checked-in generated bindings:

- `sdk/typescript/runtime-v1.ts`
- `sdk/python/runtime_v1.py`

The Go and Rust SDKs use native typed contracts. Schema changes require the Go
schema test, TypeScript and Python artifacts, protocol documentation, and the
major/minor compatibility review to change in the same commit.
