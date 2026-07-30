# llmkit-sidecar 需求

> 本文只约束 `llmkitd --mode sidecar` 和兼容命令
> `llmkit-sidecar`。本地常驻服务与远程 gateway 的新增边界见
> [llmkitd 部署模式](./deployment-modes.md)。

## 文档信息

- 状态：需求基线
- 版本：v1.0
- 更新时间：2026-07-28
- 适用范围：非 Go 桌面宿主调用 llmkit

## 目标

`llmkit-sidecar` 是随宿主桌面应用安装和启动的本地辅助进程。它让 Rust、Swift、Kotlin 等非 Go 宿主复用 llmkit 的 Provider adapter、协议、流式传输、错误分类和 usage 归一能力，而无需通过 FFI 或在其他语言中维护第二套 Provider 实现。

sidecar 是 llmkit 的可选发布产物，不是独立 AI 网关产品，不提供公网服务、用户系统、数据库、管理后台或业务路由。

## 典型调用链

```text
Desktop host
  -> credential store
  -> authenticated local IPC
  -> llmkit-sidecar
  -> llmkit core
  -> selected LLM Provider
```

宿主必须先选择并授权单个 Provider/模型。sidecar 不得自行选择或切换 Provider。

## 功能需求

### 生命周期与握手

- 宿主启动和监管 sidecar；父进程退出后 sidecar 必须自动退出。
- sidecar 必须提供 `Handshake`、`Health` 和优雅 `Shutdown`。
- 握手返回 sidecar 版本、llmkit 版本、协议版本、构建信息和能力集合。
- 协议不兼容、构建身份校验失败或会话认证失败时必须 fail closed。
- sidecar 崩溃后的重启次数由宿主限制；不得自动重放未确认是否已到达 Provider 的生成请求。

### Provider 调用

- 提供 `ListCapabilities`、`ValidateCredential`、`Generate`、`Embed` 和 `Cancel`。
- `Generate` 支持非流式与流式响应、tool call、structured output、usage、finish reason 和 Provider request ID。
- 所有调用支持 deadline、context cancellation、最大请求/响应限制和背压。
- 错误必须使用 llmkit 标准错误分类，并保留安全的 retryable、Retry-After 和 Provider request ID 元数据。
- Provider 凭据必须是请求级输入；sidecar 不提供凭据创建、列表或持久化接口。

### IPC Transport

- macOS/Linux 优先 Unix domain socket，Windows 优先 named pipe。
- 只有平台能力不足时才允许随机 loopback 端口；禁止固定端口和非 loopback 监听。
- 协议使用长度分帧或成熟的流式 RPC framing，必须防止消息截断、无限 body、慢速发送和并发流串线。
- 启动会话密钥应通过继承的匿名 pipe/受限标准输入等一次性通道传递，不得进入命令行、环境变量或文件。
- 每条连接和请求都必须绑定当前启动会话；跨会话重放必须被拒绝。

## 安全与隐私

- API Key、access token、prompt、response、tool arguments 和 embedding 输入不得写入磁盘、日志、指标、panic 或崩溃报告。
- sidecar 不读取全局 Provider 环境变量；凭据由宿主按请求显式提供。
- 日志默认关闭正文，只允许版本、Provider ID、模型 ID、耗时、标准错误类别和随机 correlation ID 等脱敏元数据。
- 敏感 byte buffer 使用后应尽快清空并释放；不得承诺 Go GC 无法保证的绝对内存擦除。
- sidecar 不执行宿主传入的任意 URL。自定义 endpoint 必须经过 scheme、host、端口和私网访问策略校验，防止 SSRF。
- 发布二进制必须提供签名校验材料、SHA-256 checksums 和 SBOM；宿主应在启动前验证其随包完整性。

## 职责边界

### sidecar 负责

- 将版本化 IPC 消息映射为 llmkit 公共 API；
- 维护单次本地会话、并发请求和流式事件；
- 调用 llmkit adapter 并返回标准结果；
- 保证本地进程边界的认证、限制、取消和脱敏。

### 宿主负责

- 用户认证、授权和 Provider/模型选择；
- OS 凭据库存取和凭据生命周期；
- 数据接收方/区域同意和内容披露；
- 跨 Provider 故障转移和重试政策；
- 定价、额度、账本、持久 attempt chain 和 UI 提示；
- sidecar 的启动、签名验证、升级、重启限制和错误展示。

## 明确非目标

- 不提供远程、多租户或常驻系统服务模式；
- 不提供兼容 OpenAI 的公网 HTTP gateway；
- 不保存 Provider Key、请求历史或模型响应；
- 不实现用户、RBAC、计费、配额、数据库或管理 UI；
- 不根据健康、成本或权重自动跨 Provider fallback；
- 不替宿主绕过 Provider 地域、条款或用户授权边界。

## 协议与版本兼容

- IPC 使用独立语义版本；握手协商双方共同支持的最高协议版本。
- 同一 major 内新增字段必须向后兼容，接收方忽略未知可选字段。
- 删除字段、改变含义或事件顺序保证必须升级 major。
- sidecar release 与 Go module 来自同一个 Git tag，并在 release manifest 中记录二者版本和协议范围。
- 至少支持当前和前一个稳定 minor 的宿主协议；安全原因停止兼容时必须在 release notes 明确说明。

## 跨平台发布

首批发布目标：

- macOS arm64
- macOS amd64
- Windows amd64
- Linux amd64（用于开发、CI 和后续桌面宿主）

每个目标生成独立二进制、checksum、SBOM 和签名/证明材料。sidecar 不在运行时自行更新，由宿主应用的可信更新链统一升级。

## 验收标准

1. Go module 与 sidecar 对同一 golden fixture 产生等价的响应、流事件、错误和 usage。
2. 非本会话连接、错误会话密钥、版本不兼容和非本地监听配置全部被拒绝。
3. 凭据和模型内容不会出现在参数、环境变量、文件、日志、指标和测试快照中。
4. 流式取消能中止上游请求；慢消费者受背压或明确失败，不造成无限内存增长。
5. 父进程退出后 sidecar 自动退出；崩溃不会导致宿主崩溃或静默重复调用。
6. macOS arm64/amd64 与 Windows amd64 完成打包、签名验证和最小端到端测试。
7. fuzz、race、畸形 framing、超大消息、并发取消、Provider 超时和崩溃恢复测试通过。

## 实施分解

1. 定义协议消息、错误、事件顺序和兼容策略。
2. 实现 session handshake、Unix socket/named pipe transport 和请求调度。
3. 接入 llmkit Generate/Embed/ValidateCredential，完成 streaming/cancel/backpressure。
4. 提供 Rust 测试客户端和最小宿主示例，验证非 Go 集成体验。
5. 建立跨平台构建、签名、checksum、SBOM 和 release manifest。
6. 运行安全、协议、Provider conformance 与桌面端到端验收。
