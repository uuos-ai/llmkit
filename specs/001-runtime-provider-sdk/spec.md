# Feature Specification: llmkit 通用 Provider SDK 与三模式运行时

**Feature Branch**: `dev`  
**Created**: 2026-07-30  
**Status**: Approved  
**Input**: 已逐项确认的 llmkit 定位、身份、sidecar、gateway、动态目标、凭据与协议要求

## User Scenarios & Testing

### User Story 1 - 第三方业务统一调用 LLM (Priority: P1)

业务只使用 llmkit 规范请求，不理解各 Provider 的鉴权、流式、错误和 usage 差异。

**Independent Test**: 对任一 conformant adapter 运行相同 golden fixtures，得到相同规范事件、错误和 usage 结构。

### User Story 2 - 三种模式无缝切换 (Priority: P1)

同一 SDK 通过默认配置、环境变量或 CLI 在独占 sidecar、本机共享服务和远程 gateway 间切换。

**Independent Test**: 同一 generate fixture 经三种传输得到等价结果，身份与凭据不交叉。

### User Story 3 - 多业务与动态用户安全隔离 (Priority: P1)

一个 llmkit 服务可供多个业务客户端使用；每个 client 同时绑定一个可变化的内部 user，不关联不同 client 中的现实人物。

**Independent Test**: 两个 client 使用相同 user 字符串仍完全隔离；同一 client 切换用户后旧 token、流和缓存立即失效。

### User Story 4 - 动态可用目标与自定义 Provider (Priority: P1)

客户端启动、场景触发和用户切换时刷新完整目标快照，可选择显式 target 或实时默认 target，并管理 client/user 自定义组合。

**Independent Test**: 原子替换 revision 后默认目标立即变化，旧 binding 的快照不能复用。

### User Story 5 - 可验证的 Provider 覆盖 (Priority: P2)

维护者用统一 conformance suite 证明国内外 Provider 的请求、流、错误、usage 与安全行为。

**Independent Test**: 每个标记 conformant 的 adapter 通过离线 fixture 与 mock server 全套测试。

## Requirements

### Functional Requirements

- **FR-001**: 核心 MUST 是可嵌入 Go SDK；`llmkitd` MUST 是复用核心的可选进程边界。
- **FR-002**: `llmkitd` MUST 支持 `sidecar`、`local-service`、`gateway`，默认 sidecar，优先级为 defaults < file < env < CLI。
- **FR-003**: 多实例 MUST 使用唯一 `instance_id + endpoint`；进程名称不得作为隔离或发现依据。
- **FR-004**: `client_id` MUST 表示独立业务；`user_id` 只在 client 内有意义，身份键为 `client_id + user_id`。
- **FR-005**: 一个 client 同时 MUST 最多绑定一个 user；切换 MUST 原子增加 `binding_version` 并撤销所有旧版本 token/请求。
- **FR-006**: 同一现实人物在不同 client 的不同 user ID MUST 永不关联；相同文本 ID 也 MUST 按不同身份处理。
- **FR-007**: sidecar 绑定作用域为进程；local-service 默认 instance 内、可选共享 UserBindingStore；gateway MUST 集群共享强一致存储。
- **FR-008**: 用户绑定 MUST 经可插拔 Authorizer；授权成功后覆盖本 client 旧用户，授权失败不改变状态。
- **FR-009**: token MUST 按模式和用途使用已批准前缀；错误模式前缀不得回退到其他 authenticator。
- **FR-010**: access/refresh token MUST 同时轮换；旧 access 立即失效；旧 refresh 非幂等重用 MUST 撤销 token family。
- **FR-011**: refresh MUST 只在关联 access 有效期内可用；family 最大寿命不得因刷新重置。
- **FR-012**: refresh secret MUST 以 token_id + HMAC 存储并支持 pepper key version 平滑轮换。
- **FR-013**: refresh/用户切换 MUST 支持 60 秒加密幂等结果恢复。
- **FR-014**: gateway OIDC MUST 只用于 client 身份 exchange；普通推理只接受 llmkit token。
- **FR-015**: local-service MUST 使用一次性 enrollment token 注册 client instance。
- **FR-016**: 可用目标清单 MUST 是完整原子快照，包含 revision、binding_version、刷新时间、默认 target 和目标能力。
- **FR-017**: 显式 target MUST 严格解析；省略时使用最新动态默认；OpenAI 兼容接口使用 `llmkit-default`。
- **FR-018**: 同步启用时业务 API MUST 是完整快照唯一权威；禁用时使用业务内置 + client/user 本地自定义项。
- **FR-019**: 禁用同步默认 freshness 为 15 分钟，失败宽限 30 分钟，绝对最旧 45 分钟。
- **FR-020**: 目标 MUST 支持 business/client/user owner scope 和 managed/request_scoped/workload/none credential mode。
- **FR-021**: 凭据不得跨 scope 隐式回退；不同费用来源 MUST 使用不同 target_id。
- **FR-022**: sidecar 默认 request-scoped；local 可选 OS Keychain managed；gateway 默认外部 SecretStore managed。
- **FR-023**: managed 凭据 MUST 使用 pending → active → retired 生命周期，credential_ref 永不返回执行客户端。
- **FR-024**: 自定义 Provider MUST 使用结构化 profile，禁止任意代码、模板和危险 header。
- **FR-025**: gateway 自定义 endpoint MUST 防御 SSRF、DNS rebinding、危险重定向和凭据跨源转发。
- **FR-026**: 原生协议 MUST 是语义基准；OpenAI-compatible MUST 是独立适配层。
- **FR-027**: v1 原生操作 MUST 包含 generate、embed、rerank、moderate；Provider 按真实能力声明。
- **FR-028**: 流 MUST 有 request_id、response_id、连续 sequence 和唯一 completed/failed/cancelled 终态。
- **FR-029**: 工具参数分片、reasoning、usage 与异常断流 MUST 规范化且可验证。
- **FR-030**: errors MUST 使用批准的稳定分类，安全 message 不含原始正文或秘密。
- **FR-031**: usage MUST 区分 reported/derived/estimated/unavailable，子项不得重复计入总量。
- **FR-032**: capability 不匹配 MUST 默认失败；降级必须逐字段授权并返回 warning。
- **FR-033**: retry/fallback MUST 显式、有界、共享 attempt budget；输出开始或 outcome unknown 后禁止重试。
- **FR-034**: deadline MUST 分 queue/connect/header/stream-idle/total，并传播所有取消源。
- **FR-035**: Provider 熔断 MUST 按 client+target+credential_version+endpoint 隔离。
- **FR-036**: 模型发现 MUST 只返回候选项，不能自动进入可用目标清单。
- **FR-037**: Provider adapter MUST 分离 manifest、endpoint、auth、encoder、transport、decoder、error/usage normalization。
- **FR-038**: v1 不得使用 Go plugin；内置编译注册，自定义优先声明式 profile，未来可用进程/WASM 扩展。
- **FR-039**: maturity MUST 为 experimental/conformant/verified，verified 必须有时间与 API 版本。
- **FR-040**: P0 Provider MUST 包含 OpenAI、Anthropic、Gemini、DeepSeek、Qwen、Doubao、GLM、Kimi、MiniMax、TokenHub。
- **FR-041**: 多模态内容 MUST 使用 inline_data/blob_ref/remote_url；服务不得读取客户端任意 file path。
- **FR-042**: 日志/trace MUST 默认无 prompt、response、工具参数、token、secret、credential_ref；user 使用 HMAC 指纹。
- **FR-043**: 审计 MUST 追加写入并可验证完整性；gateway 必须外部 AuditStore，高风险写入在审计耗尽时 fail closed。
- **FR-044**: 限流 MUST 支持 client/user/target/provider 层级，gateway 跨副本一致。
- **FR-045**: token scopes MUST 最小化；users:bind、credentials:write、providers:write 和 admin 必须独立。
- **FR-046**: 控制面与数据面 MUST 使用不同 handler、权限和生产 listener/network policy。
- **FR-047**: business API MUST 使用 mTLS + 短期服务 token，并按功能拆分 scopes。
- **FR-048**: 原生协议 MUST major/minor 协商；请求未知字段拒绝，响应未知非终态字段可忽略。
- **FR-049**: Go/Rust 为 P0 SDK，TypeScript/Python 为 P1，Java/Kotlin 为 P2；浏览器只连接 gateway。
- **FR-050**: gateway schema 升级 MUST expand/migrate/contract；local SQLite MUST 备份并事务迁移。

### Key Entities

- **Principal**: client、instance、当前 user、binding version 与 scopes。
- **UserBinding**: 一个 client 当前 user 的版本化活动关系。
- **TokenFamily**: generation、created/expires、撤销与 refresh replay 状态。
- **AvailableTargetSnapshot**: 一个绑定下完整、带 revision/freshness 的目标视图。
- **Target**: Provider+model+region+endpoint 与 owner/credential/capability 策略组合。
- **CredentialLease**: 请求级秘密句柄或 managed secret version 租约。
- **AdapterManifest**: Provider 协议版本、操作、能力、鉴权和成熟度声明。

## Success Criteria

- **SC-001**: 所有内置 Provider 通过统一非流、流、错误、usage、工具与畸形响应测试。
- **SC-002**: `go test ./...`、vet、race 关键包和三平台构建全部通过。
- **SC-003**: 交叉 client/user/credential/blob/target 访问测试 100% 被拒绝。
- **SC-004**: 所有流测试证明 sequence 连续且只有一个终态。
- **SC-005**: secret leak fixtures 在日志、错误、审计和快照中零命中。

## Assumptions

- 业务平台实现用户授权、价格、计费、最终路由政策及 gateway 外部存储。
- Provider 模型和价格会变化，运行时快照而非代码常量是业务可用性的权威。
- 第一版高级自定义协议通过重新编译 adapter；运行时 WASM/进程插件后续兼容加入。

