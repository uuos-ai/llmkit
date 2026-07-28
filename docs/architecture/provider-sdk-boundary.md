# Provider SDK 架构边界

## 目标

uukit 提供从“宿主已经选择并授权一个 Provider/模型”到“获得标准化响应、usage 或错误”的完整单次调用能力。

## 包结构目标

```text
uukit
├── provider.go              stable host-facing API
├── registry.go              explicit adapter registration
├── errors.go                normalized error taxonomy
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

## uukit 负责

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
- 在未经宿主声明的情况下发送遥测。

## 故障转移协作

uukit 对每个失败返回标准化 `ProviderError`。宿主根据错误种类、Provider 健康、授权区域和策略选择下一 Target，并为每次调用生成新的 attempt。SDK 不自行选择另一个 Provider。

这使宿主能够明确展示：原 Provider/模型、失败原因、实际 Provider/模型、区域和额度影响。
