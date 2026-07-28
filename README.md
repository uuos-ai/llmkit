# uukit

`uukit` 是一个独立、可嵌入的 Go SDK，用统一接口对接国内外主流 LLM Provider，减少每个项目重复处理协议、鉴权、流式响应、错误分类、用量归一和模型能力差异。

## 定位

`uukit` 是 SDK，不是 AI 网关产品，也不拥有宿主应用的用户、支付、额度、路由政策或数据库。

```text
Host Application
├── product policy / consent / billing / audit
├── routing and failover policy
└── uukit
    ├── provider adapters
    ├── protocol codecs
    ├── HTTP and SSE transport
    ├── normalized errors and usage
    └── capability discovery
```

## 设计原则

- Provider-neutral：统一能力，不泄漏单一供应商类型到业务层。
- Embeddable：普通 Go module，无 HTTP Server、数据库或管理后台依赖。
- Host-owned policy：SDK 不替宿主决定用户权限、跨区域同意、价格和最终路由。
- Explicit behavior：重试、故障转移、超时和流式终止由显式配置控制。
- Auditable：每次调用返回 Provider、模型、attempt、usage 和标准化错误。
- Replaceable：协议转换、Transport 和 Adapter 均可替换。
- Permissive licensing：项目采用 Apache-2.0，不复制 AGPL 项目代码。

## 初始范围

计划优先覆盖：

- OpenAI-compatible
- Anthropic Claude
- Google Gemini
- DeepSeek
- Alibaba Cloud DashScope / Qwen
- Volcengine Ark / Doubao
- Zhipu GLM
- Moonshot Kimi
- MiniMax
- Tencent Hunyuan

优先能力：Chat、Responses、Embedding、流式输出、Tool Calling、Structured Output 和可信 usage。

## 当前状态

项目处于架构基线阶段。当前已建立最小 Provider/Registry 接口，并沉淀对 new-api `relay`、`relaykit` 和 TokenHub 的分析。

- [分析索引](./docs/README.md)
- [new-api relay/relaykit 分析](./docs/research/new-api-relay-relaykit-analysis.md)
- [SDK 架构边界](./docs/architecture/provider-sdk-boundary.md)
- [路线图](./docs/roadmap.md)

## License

Apache License 2.0。
