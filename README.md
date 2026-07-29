# llmkit

`llmkit` 是一个独立、可嵌入的 Go SDK，用统一接口对接国内外主流 LLM Provider，减少每个项目重复处理协议、鉴权、流式响应、错误分类、用量归一和模型能力差异。

## 定位

`llmkit` 的核心是 SDK，不是 AI 网关产品，也不拥有宿主应用的用户、支付、额度、路由政策或数据库。对于 Rust、Swift、Kotlin 等非 Go 宿主，项目同时提供可选的本地 `llmkit-sidecar`，把同一套 SDK 能力通过受保护的版本化 IPC 暴露给宿主。

```text
Host Application
├── product policy / consent / billing / audit
├── routing and failover policy
└── llmkit
    ├── provider adapters
    ├── protocol codecs
    ├── HTTP and SSE transport
    ├── normalized errors and usage
    ├── capability discovery
    └── optional local sidecar for non-Go hosts
```

## 设计原则

- Provider-neutral：统一能力，不泄漏单一供应商类型到业务层。
- Embeddable：普通 Go module，无 HTTP Server、数据库或管理后台依赖。
- Host-owned policy：SDK 不替宿主决定用户权限、跨区域同意、价格和最终路由。
- Explicit behavior：重试、故障转移、超时和流式终止由显式配置控制。
- Auditable：每次调用返回 Provider、模型、attempt、usage 和标准化错误。
- Replaceable：协议转换、Transport 和 Provider 实现均可替换。
- Permissive licensing：项目采用 Apache-2.0，不复制 AGPL 项目代码。
- One implementation：Go module 与 sidecar 使用相同核心包和版本，不形成第二套 Provider 实现。

## 初始范围

Provider 覆盖规划：

- OpenAI Chat Completions / Responses（已实现）
- Anthropic Claude Messages（已实现）
- Google Gemini generateContent（已实现）
- DeepSeek OpenAI-compatible Chat（已实现）
- Alibaba Cloud DashScope / Qwen OpenAI-compatible（已实现）
- MiniMax、Zhipu GLM、Volcengine Ark、Tencent Hunyuan profiles（已实现）
- Moonshot/Kimi Chat（已实现）

优先能力：Chat、Responses、Embedding、流式输出、Tool Calling、Structured Output 和可信 usage。

## 当前状态

项目已完成首批 Provider SDK 与 sidecar 协议基线。当前包括类型化
Provider/Registry API、请求级凭据、凭据验证、HTTP Transport、SSE parser、
显式同目标 retry、conformance harness，以及国内外首批 Provider 的独立
离线协议合同测试。

- [分析索引](./docs/README.md)
- [new-api relay/relaykit 分析](./docs/research/new-api-relay-relaykit-analysis.md)
- [SDK 架构边界](./docs/architecture/provider-sdk-boundary.md)
- [llmkit-sidecar 需求](./docs/architecture/sidecar-requirements.md)
- [llmkit-sidecar protocol v1](./docs/sidecar-protocol-v1.md)
- [llmkit-sidecar 兼容矩阵](./docs/sidecar-compatibility.md)
- [Provider 兼容矩阵](./docs/provider-compatibility.md)
- [路线图](./docs/roadmap.md)

## Reliability helpers

- `retry` 仅对一个不可变 `Target` 做显式、有限次数重试。
- `reliability` 产生不含内容与凭据的 attempt sample，并维护只读健康信号；它不拦截请求。
- `failover` 只遍历宿主明确提供的目标列表，并要求宿主为每次失败提供继续决策。
- `catalog` 对宿主触发的 capability discovery 做 TTL 缓存和并发请求合并，不自行拉取或选择模型。

## License

Apache License 2.0。
