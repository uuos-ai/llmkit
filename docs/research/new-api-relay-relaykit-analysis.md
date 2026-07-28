# new-api relay、RelayKit 与 TokenHub 分析

## 文档信息

- 状态：研究基线
- 日期：2026-07-28
- new-api 检查提交：`afe16c64cd73853da1eda3bf236f15d69637b4bf`
- 用途：确定 uukit 的可复用范围和独立 SDK 边界

## 结论

new-api 的 `relay + relaykit` 是完整 new-api 产品中的 Provider 请求执行核心，但不是可脱离宿主使用的完整 Provider SDK。

- RelayKit 已经是独立 Go module，适合协议 DTO、请求/响应、流式事件、usage 和 finish reason 转换。
- relay 覆盖 Adapter 选择、上游 URL/Header、HTTP 请求、流式/非流式处理和 Provider 特有逻辑。
- 初始渠道选择、Token 鉴权、数据库配置、配额、重试、故障转移和审计仍依赖 router、middleware、controller、model、service 和 setting。
- relay 的 Adapter 接口直接依赖 Gin、RelayInfo、数据库模型、计费服务和全局配置，不是稳定 SDK 边界。

因此，uukit 不复制 new-api relay，而是定义自己的纯 Go Adapter、Transport、Codec、Error 和 Usage 接口。

## new-api 请求链

```text
router
  -> TokenAuth / RateLimit / Distribute
  -> middleware selects Channel
  -> controller creates RelayInfo and controls retries
  -> relay selects Provider Adapter
  -> relaykit converts protocol DTO/events
  -> adapter sends HTTP/SSE and parses response
  -> service settles quota and records logs
```

`relay + relaykit` 单独缺少：

- Provider 配置和凭据持久化；
- 初始路由和数据区域授权；
- 完整错误分类、熔断和跨 Provider 故障转移；
- attempt chain；
- 宿主业务计费、幂等结算和客户端通知；
- 与宿主审计模型一致的可观测性。

## RelayKit 可借鉴能力

- OpenAI Chat、OpenAI Responses、Anthropic Messages、Gemini 转换；
- 非流式和流式响应转换；
- Tool Calling、推理内容、常见多模态内容；
- usage、finish reason、转换路径和质量等级。

RelayKit 不负责 HTTP、SSE 传输、Provider 选择、模型映射、重试、鉴权、计费或数据库。

## relay 可借鉴能力

- Provider URL 构造；
- Header 和鉴权差异；
- Provider 请求/响应结构；
- SSE 解析细节；
- Embedding、Rerank、Chat、Responses 的适配方式；
- usage 提取和错误码映射。

不能直接迁移的部分包括 Gin Context、new-api Channel 数据模型、quota、倍率、用户组、全局 setting 和自动渠道选择。

## TokenHub 的参考价值

TokenHub 更适合参考：

- Provider 健康状态机；
- 错误分类和冷却；
- circuit breaker；
- 成本、延迟、失败率和质量候选评分；
- 路由模拟和实时决策事件。

uukit 只提供实现这些能力需要的标准化信号。最终路由、用户授权、区域边界和计费仍由宿主决定。

## 许可证结论

new-api 和 RelayKit 使用 AGPL-3.0。uukit 使用 Apache-2.0，因此不得复制、改写后搬运或静态链接未获额外授权的 AGPL 代码。实现应依据公开 Provider 协议进行 clean-room 开发，并通过独立 fixtures 和 contract tests 验证。

## 上游参考

- <https://github.com/QuantumNous/new-api>
- <https://github.com/QuantumNous/new-api/tree/main/relay>
- <https://github.com/QuantumNous/new-api/tree/main/relaykit>
- <https://github.com/jordanhubbard/tokenhub>
