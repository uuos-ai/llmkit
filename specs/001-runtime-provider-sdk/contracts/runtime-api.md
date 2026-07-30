# Runtime API Contract

## Native data plane

- `GET /v1/available-targets`
- `POST /v1/generate`
- `POST /v1/embed`
- 后续同一 major 增加 `/v1/rerank`、`/v1/moderate`
- `POST /v1/blobs`
- `POST /v1/tokens/refresh`
- `POST /v1/session/bind-user`

## OpenAI compatibility

- `GET /v1/models`: ID 为 target_id，包含保留项 `llmkit-default`
- `POST /v1/chat/completions`
- `POST /v1/responses`
- `POST /v1/embeddings`

## Control plane

- custom Provider CRUD、managed credential lifecycle、client enrollment、runtime policy 与审计读取必须使用独立 handler/listener 和 scopes。

## IPC

长度前缀 JSON frame；新客户端使用 `get_available_targets`。`resolve_provider_options` 只作为 protocol v1 兼容别名。握手返回协商版本、instance、client/user/binding 与 methods。

