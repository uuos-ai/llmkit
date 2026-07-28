# uukit Roadmap

## Phase 0：基线

- [x] 初始化 Apache-2.0 Go module
- [x] 定义 Adapter、Request、Response、Usage、StreamEvent 和 ProviderError
- [x] 沉淀 new-api relay/relaykit 与 TokenHub 分析
- [ ] 建立 ADR、贡献指南、安全策略和版本兼容政策
- [ ] 建立 CI、lint、race、coverage、dependency review 和 release workflow
- [ ] 批准 uukit-sidecar IPC、安全、兼容和发布需求基线

## Phase 1：协议与 Transport

- [ ] 定义稳定的消息、内容块、工具和 Structured Output 类型
- [ ] 实现可注入 HTTP Transport、SSE parser、超时和取消
- [ ] 实现 OpenAI Chat/Responses 基线 Codec
- [ ] 建立 Provider conformance test suite 和脱敏 fixtures

## Phase 2：首批 Provider

- [ ] OpenAI-compatible / DeepSeek / Moonshot
- [ ] Anthropic Claude
- [ ] Google Gemini
- [ ] Alibaba DashScope / Qwen
- [ ] Volcengine Ark / Doubao
- [ ] Embedding、Tool Calling、Structured Output 和流式矩阵

## Phase 3：国内 Provider 扩展

- [ ] Zhipu GLM
- [ ] MiniMax
- [ ] Tencent Hunyuan
- [ ] Provider capability discovery 和模型目录缓存

## Phase 4：可靠性工具

- [ ] 标准错误分类和 Retry-After
- [ ] 显式同目标 retry helper
- [ ] Health sample、circuit-breaker signal 和 metrics hooks
- [ ] 宿主可控的候选评分与 failover 辅助包
- [ ] 性能、故障注入和兼容性报告

## Phase 5：非 Go 宿主 sidecar

- [ ] 定义 sidecar v1 协议、framing、事件顺序和兼容矩阵
- [ ] 实现 session handshake、Unix domain socket 和 Windows named pipe
- [ ] 实现 Generate、Embed、ValidateCredential、Cancel、Health 和 Shutdown
- [ ] 实现 streaming 背压、deadline、消息大小限制和崩溃隔离
- [ ] 提供 Rust 测试客户端与最小桌面宿主示例
- [ ] 建立 macOS arm64/amd64、Windows amd64 和 Linux amd64 构建
- [ ] 发布签名校验材料、checksums、SBOM 和 release manifest
- [ ] 完成安全、fuzz、race、协议兼容及端到端测试
