# Provider SDK 架构边界

## 目标

llmkit 提供从“宿主已经选择并授权一个 Provider/模型”到“获得标准化响应、usage 或错误”的完整单次调用能力。

一次调用的 `Target` 必须明确 Provider、模型、region 和可选 endpoint。Provider 实现不得在调用过程中更换其中任何边界。

## 已采用的核心设计

### 小接口和能力发现

`Provider` 只提供 ID 和能力发现。Generate、Stream 和 Embed 分别由 `Generator`、`StreamGenerator` 和 `Embedder` 表达。Registry 通过类型断言发现能力，不要求每个 Provider 实现无关方法。

### 类型化公共 API

宿主使用统一的 Message、ContentPart、Tool、ToolCall、ResponseFormat、Usage、Response 和 StreamEvent。Provider 原始 DTO 只能存在于 adapter/codec 内部；公共请求、响应和事件不使用 `any`。

Provider 特有选项必须使用 provider-qualified namespace，并以明确的 JSON payload 作为 escape hatch。通用能力不得依赖此 escape hatch 才能工作。

### 请求级凭据

每个 Call 携带 `CredentialHandle`。Handle 在当前请求和明确 Target 上应用鉴权，可以连接宿主 Vault/KMS 或短期凭据服务。adapter 不读取全局环境变量、不持久化 handle，也不得把 credential 放入错误、日志或诊断元数据。

### Transport 与 Codec 分离

```text
typed host API
  -> Provider adapter
      -> native Codec
      -> CredentialHandle
      -> injectable Transport
      -> stream parser/state/finalizer
  -> normalized Response/Event/Usage/ProviderError
```

Codec 不隐式访问网络。需要解析远程媒体时，由宿主显式注入受 context、大小限制、region 和 SSRF policy 约束的 resolver。

### 流生命周期

`EventStream.Recv` 只在正常完成全部 finalize 工作后返回 `io.EOF`。上游截断、畸形事件和 Provider 流内错误必须返回结构化错误。`Close` 必须安全终止网络读取并可重复调用。

标准事件覆盖 message/content start、text、reasoning、tool call、tool arguments、usage 和 finish。事件不得夹带原始 Provider DTO。

### Usage、Error 和转换报告

Usage 区分 Provider reported、estimated 和 missing，保留 input/output/total、cache read/write、reasoning 及 text/image/audio 计数。SDK 不计算价格或账单。

ProviderError 包含 Provider、模型、错误种类、transport phase、HTTP status、retryable、Retry-After、Provider code 和 request ID。只允许安全消息，不保存完整请求或响应 body。

任何近似或有损协议适配都通过 `Adaptation` 返回 field、action、quality 和 detail；不支持的能力返回明确错误，不能静默删除字段。

## 包结构目标

```text
llmkit
├── provider.go              stable host-facing API
├── registry.go              explicit adapter registration
├── errors.go                normalized error taxonomy
├── cmd/llmkitd/              optional three-mode transport runtime
├── gateway/                  HTTPS JSON/SSE adapter with injected stores
├── routing/                  non-secret provider/target catalog
├── managed/                  Config/Secret/Audit storage ports
├── sidecar/protocol/        versioned IPC messages and compatibility
├── sidecar/server/          lifecycle, session auth and stream bridge
├── codec/                   protocol DTO and semantic conversion
├── transport/               HTTP, SSE, timeout and cancellation
├── providers/
│   ├── openai/
│   ├── anthropic/
│   ├── gemini/
│   ├── deepseek/
│   ├── dashscope/
│   ├── volcengine/
│   ├── zhipu/
│   ├── moonshot/
│   ├── minimax/
│   └── hunyuan/
└── conformance/             reusable adapter contract suite
```

## llmkit 负责

- Provider endpoint、Header 和签名；
- Provider 特有模型和能力发现；
- Chat、Responses、Embedding、流式、Tool Calling 和 Structured Output；
- HTTP/SSE Transport、context 取消和明确超时；
- 请求/响应协议转换；
- usage 和 finish reason 归一；
- 错误分类、Retry-After 和 retryable 信号；
- 单次调用的 Provider request ID 和安全诊断元数据；
- 可选、显式、有限的同目标瞬时重试工具。

## 宿主负责

- 用户、组织、Token 和 RBAC；
- Provider Policy、候选过滤和权重；
- 数据接收方与跨区域同意；
- 跨 Provider/模型故障转移；
- 价格、折扣、额度、支付和账本；
- durable task、attempt chain 和审计存储；
- UI 提示、内容安全政策和业务遥测。

## 明确禁止

- SDK 内部读取全局环境变量决定业务行为；
- 隐式跨 Provider fallback；
- 在错误或日志中暴露 API Key、正文或完整请求；
- 依赖 Gin Context 或宿主数据库模型；
- 把 Provider 原始 DTO 暴露为稳定公共 API；
- 使用 raw `json.RawMessage` 或 `io.ReadCloser` 作为最终 host-facing 响应；
- 把调用者取消错误分类为可重试 Provider 故障；
- 在一个 adapter 内对多个 endpoint、region 或 Provider 做隐式轮询；
- 在未经宿主声明的情况下发送遥测。

## Conformance 要求

每个声明支持的 Provider 能力都必须通过统一合同测试：

- 非流式和流式/finalize；
- tool calling、structured output 和声明支持的多模态；
- usage、finish reason 和 Provider request ID；
- 错误分类和 Retry-After；
- timeout、cancellation 和 Close；
- malformed JSON/SSE、截断和流内错误；
- body size limit 和 secret redaction；
- capability 声明与实际行为一致。

fixtures 必须根据 Provider 官方协议独立编写，并记录来源；不得从 AGPL 项目复制 snapshot。

## 故障转移协作

llmkit 对每个失败返回标准化 `ProviderError`。宿主根据错误种类、Provider 健康、授权区域和策略选择下一 Target，并为每次调用生成新的 attempt。SDK 不自行选择另一个 Provider。

这使宿主能够明确展示：原 Provider/模型、失败原因、实际 Provider/模型、区域和额度影响。

## 非 Go 宿主

非 Go 应用不得重新实现 Provider adapter。它们可以随应用分发同版本的
`llmkitd`，通过本地 IPC 调用 llmkit。sidecar 模式仍只是能力映射；
local-service/gateway 增加经过身份隔离的目录解析与传输，但不拥有业务路由、
故障转移、计费或授权政策。详细要求见
[部署模式](./deployment-modes.md) 与 [sidecar 需求](./sidecar-requirements.md)。
