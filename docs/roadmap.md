# llmkit Roadmap

## Phase 0：基线

- [x] 初始化 Apache-2.0 Go module
- [x] 采用小 Provider/能力接口、类型化 Request/Response/Event、Usage 和 ProviderError
- [x] 采用 request-scoped CredentialHandle、显式 Target 和转换 Adaptation 报告
- [x] 沉淀 new-api relay/relaykit 与 TokenHub 分析
- [x] 建立贡献指南、安全策略和版本兼容政策
- [x] 建立 CI、format、vet、race、coverage 和 dependency review
- [x] 建立 Provider SDK 边界 ADR
- [x] 建立 release workflow
- [ ] 批准 llmkit-sidecar IPC、安全、兼容和发布需求基线

## Phase 1：协议与 Transport

- [x] 定义消息、内容块、工具和 Structured Output 基线类型
- [x] 实现可注入 HTTP Transport、SSE parser、超时和取消
- [x] 实现 OpenAI Chat/Responses 基线 Codec
- [x] 建立 Provider conformance test suite 和脱敏 fixtures

## Phase 2：首批 Provider

- [x] OpenAI native Chat Completions / Responses
- [x] OpenAI-compatible profiles（DeepSeek、DashScope、Moonshot 及国内首批厂商）
- [x] Anthropic Claude
- [x] Google Gemini
- [x] Alibaba DashScope / Qwen
- [x] Volcengine Ark / Doubao Responses profile
- [x] 建立 Embedding、Tool Calling、Structured Output 和流式矩阵

## Phase 3：国内 Provider 扩展

- [x] Zhipu GLM Chat profile
- [x] MiniMax Chat profile
- [x] Tencent Hunyuan Chat/Embedding profile
- [x] Provider capability discovery 缓存（host-triggered、TTL、并发合并）
- [ ] 远端模型目录枚举与 Provider 特定刷新器

## Phase 4：可靠性工具

- [x] 标准错误分类和 Retry-After 基线
- [x] 显式同目标 retry helper
- [x] Health sample、circuit-breaker signal 和 metrics hooks
- [x] 宿主显式控制的 failover 辅助包
- [ ] 性能、故障注入和兼容性报告

## Phase 5：非 Go 宿主 sidecar

- [x] 定义 sidecar v1 协议、framing、事件顺序和兼容基线
- [ ] 实现 session handshake、Unix domain socket 和 Windows named pipe（Unix 与父进程退出监控已完成）
- [ ] 实现 Generate、Embed、ValidateCredential、Cancel、Health 和 Shutdown（ValidateCredential 待定义）
- [x] 实现 streaming 背压、context cancellation 和消息大小限制
- [x] 提供无第三方运行时依赖的 Rust Unix socket 测试客户端
- [ ] 建立 macOS arm64/amd64、Windows amd64 和 Linux amd64 构建
- [ ] 发布签名校验材料、checksums、SBOM 和 release manifest
- [ ] 完成安全、fuzz、race、协议兼容及端到端测试
