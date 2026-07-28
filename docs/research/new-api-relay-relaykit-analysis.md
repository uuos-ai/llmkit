# new-api relay、RelayKit 与 TokenHub Provider 实现分析

## 文档信息

- 状态：源码级研究基线
- 日期：2026-07-28
- new-api 检查提交：`afe16c64cd73853da1eda3bf236f15d69637b4bf`
- TokenHub 检查提交：`7ba47728397ecc5e44263683e709084b5fd78658`（v0.2.35）
- 用途：提炼可由 llmkit clean-room 实现的 Provider SDK 设计，不复制上游代码

## 结论

new-api、RelayKit 和 TokenHub 分别解决了不同层次的问题：

| 项目/模块 | 主要职责 | 最值得借鉴 | 不应进入 llmkit Core |
|---|---|---|---|
| new-api `relay` | 网关内的渠道执行和协议适配 | 大量真实 Provider 差异、URL/Header、SSE、usage、边界案例 | Gin、渠道数据库、用户/分组、倍率、计费、自动重试和路由 |
| RelayKit | 四种主流文本协议之间的 DTO 和语义转换 | 转换注册表、路径/质量元数据、流状态和 Finalize、golden tests | AGPL 代码、产品 DTO、动态 `any` 公共 API、全局 resolver |
| TokenHub Provider | 为路由引擎发送请求 | 小核心接口、可选能力接口、动态凭据 resolver、Retry-After 和 request ID | raw response、字符串错误识别、自动 fallback、健康/成本路由 |
| TokenHub Router/Health | 选择、观测和故障转移 | llmkit 应输出的标准信号定义 | 路由评分、熔断、模型禁用、预算和跨 Provider fallback |

llmkit 应负责从“宿主已选择且已授权的单个 Target”到“标准化结果/流事件/usage/错误”的一次调用。宿主仍负责用户、授权、区域、路由、重试策略、价格、计费和持久化审计。

## new-api relay 分析

### 请求链和边界

```text
router
  -> authentication / rate limit / channel selection
  -> controller creates RelayInfo and controls retries
  -> relay.GetAdaptor(apiType)
  -> adaptor converts request and builds URL/Header
  -> adaptor sends HTTP/SSE request
  -> adaptor converts response and extracts usage
  -> service settles quota and writes product logs
```

`relay/channel.Adaptor` 同时包含：

- 初始化请求状态；
- URL 和 Header 构造；
- Chat、Responses、Claude、Gemini、Embedding、Rerank、Audio、Image 转换；
- HTTP 发送；
- 响应写回；
- usage 返回；
- 模型列表和渠道名称。

这使 relay 能直接服务 new-api 网关，但不是适合第三方 Go 应用的稳定 SDK 接口。

### 优点

#### 1. Provider 覆盖广且包含大量生产差异

relay 不只覆盖 OpenAI、Anthropic 和 Gemini，还包含 DashScope、Baidu、DeepSeek、Moonshot、MiniMax、Volcengine、Tencent、Zhipu、Xunfei、SiliconFlow、Vertex、Bedrock、Ollama、vLLM 类兼容服务，以及图片、音频、rerank 和异步任务平台。

对 llmkit 最有价值的是它暴露出的测试问题集合：

- 同一 Provider 的原生协议和 OpenAI-compatible 协议并不等价；
- endpoint 可能由 region、deployment、API version 或模型共同决定；
- tool call ID、arguments delta、reasoning、finish reason 和 usage 的字段存在差异；
- HTTP 200 仍可能携带业务错误；
- 流式 usage 可能只在尾帧出现，或需要跨帧合并；
- cache read/write、reasoning、audio/image token 需要保留语义；
- Provider 的错误码、限流和 quota exhausted 不能只按 HTTP status 判断。

这些应转化为 llmkit 的独立协议 fixtures 和 contract cases，而不是复制 relay 实现。

#### 2. URL、Header 和转换逻辑按 Provider 分包

每个渠道包保存自身 constants、DTO、adapter 和协议实现。这个组织方式有利于：

- 将 Provider 变更限制在单一目录；
- 单独测试签名、URL、Header 和 DTO；
- 避免把所有 Provider 条件分支集中在一个 HTTP client；
- 对 OpenAI-compatible Provider 保留必要的 dialect 差异。

llmkit 应采用独立的 `providers/<provider>` package，但通过显式 Registry 注册，避免中央 `switch apiType` 成为扩展瓶颈。

#### 3. 流式实现考虑了真实运行时问题

relay 的 stream scanner 处理了：

- 上下文取消和客户端断开；
- streaming timeout；
- ping/keepalive；
- writer 串行化；
- scanner、handler 和 ping goroutine 的停止协调；
- panic 和非正常结束原因记录。

llmkit 应借鉴这些失败模式，但不复用其面向 Gin response writer 的并发结构。SDK 更适合把解析后的事件交给调用者，并让宿主负责下游写回和 ping。

#### 4. usage 语义覆盖细

new-api 区分：

- input/output/total tokens；
- cached read 和 cache creation；
- 5 分钟/1 小时 cache write；
- reasoning tokens；
- text/image/audio token；
- Gemini thoughts、tool-use prompt 和 candidate token；
- Provider reported 与 estimated usage。

llmkit 的 Usage 不应只保留三个整数，应保留细分值、来源和 Provider 原始安全元数据，但不能包含价格、倍率或最终账单。

### 缺点

#### 1. Adaptor 接口过大

所有 adapter 被要求认识多种互不相关的 modality 和下游协议，产生大量无关方法和 `any` 返回值。它违反接口隔离，也难以表达“该 Provider 不支持某能力”。

llmkit 应拆分成能力接口：

```go
type Provider interface {
    ID() ProviderID
}

type Generator interface {
    Generate(context.Context, GenerateRequest) (GenerateResponse, error)
}

type StreamGenerator interface {
    Stream(context.Context, GenerateRequest) (EventStream, error)
}

type Embedder interface {
    Embed(context.Context, EmbedRequest) (EmbedResponse, error)
}
```

#### 2. 与 Gin 和产品状态深度耦合

`Adaptor` 直接接收 `*gin.Context`；`RelayInfo` 同时包含 API key、渠道、用户、分组、配额、订阅、价格、计费会话、重试、WebSocket、日志和转换状态。Provider adapter 因而可以读取远超协议执行所需的状态。

llmkit 的 adapter 输入只能包含 Target、request-scoped credential、协议请求和安全的调用选项。

#### 3. 可变 Init 和大状态对象增加并发风险

`Init(info)` 把每次请求状态写入 adapter。即使调用方当前每次创建新 adapter，这个契约仍容易被误用为共享实例。llmkit adapter 应尽量不可变，所有请求状态通过方法参数或独立 stream state 传递。

#### 4. Transport、Codec 和产品响应写回混在一起

adapter 既转换 DTO，又发送 HTTP，还直接处理下游响应。这让：

- transport 难以注入和统一测试；
- secret redaction、timeout 和 tracing 难以形成一致策略；
- codec 无法脱离网关运行；
- Provider contract tests 必须启动更大范围的产品组件。

llmkit 应分离 `Adapter -> Codec -> Transport`，Provider package 组合它们，但稳定接口不暴露宿主 Web framework。

#### 5. 中央 switch 和整数类型注册不利于第三方扩展

`GetAdaptor(apiType int)` 依赖中央 switch、新增常量和主仓修改。llmkit 应使用类型安全的 `ProviderID` 和显式 Registry，不使用数据库整数作为稳定 API。

## RelayKit 分析

### 实际边界

RelayKit 是 new-api 仓库中的独立 Go module，不依赖 Gin、数据库或主模块。它负责：

- OpenAI Chat Completions；
- OpenAI Responses；
- Anthropic Messages；
- Gemini `generateContent`；
- 请求、非流式响应和流式事件之间的转换；
- usage 和 finish/stop reason 映射。

它不负责 Provider 选择、模型映射、鉴权、HTTP、SSE framing、重试和计费执行。

### 优点

#### 1. 转换是显式、可审计的

转换结果包含：

- `From` 和 `To`；
- converter ID；
- direct 或 multi-hop `Steps`；
- `Good`、`Fair`、`Discouraged` 质量等级；
- 统一 usage；
- 是否为 streaming。

这是非常适合 llmkit 的设计。跨协议转换不是无损的，SDK 应让宿主知道发生了什么，而不是假装所有 Provider 完全等价。

#### 2. Stream state 和 Finalize 是一等概念

RelayKit 为每条流创建独立状态，逐 chunk 转换，并在 EOF 后显式 Finalize。这样才能正确处理：

- tool call arguments 跨 chunk 拼接；
- start/delta/stop 事件补齐；
- 最终 finish reason；
- 尾帧 usage；
- 没有自然落在某个上游 chunk 上的下游终止事件。

llmkit 的标准 EventStream 也应有明确生命周期：`Recv`、`Close` 和 terminal summary；内部 parser 必须执行 finalize，流中断不能退化成正常 EOF。

#### 3. 请求级转换选项

`convmeta.Options` 是每个请求的配置快照，零值禁用额外适配。它避免 converter 直接读取宿主全局设置。这与 llmkit 的 explicit behavior 原则一致。

#### 4. 媒体解析通过依赖注入提供

RelayKit 自身不下载图片 URL，而要求宿主提供 media resolver。llmkit 也不应在 codec 中隐式发起第二次网络请求；远程媒体获取必须显式注入，并继承 context、region、size limit 和 SSRF policy。

#### 5. 测试策略成熟

RelayKit 使用：

- conversion matrix golden tests；
- boundary tests；
- source/target 类型检查；
- zero-value DTO tests；
- stream finalize tests；
- usage 细分测试。

llmkit 应借鉴这种测试结构，并增加 transport、错误、取消、secret redaction 和 malformed SSE 的合同测试。

### 缺点和限制

#### 1. AGPL-3.0 许可证不兼容

RelayKit 虽是独立 module，但仍属于 AGPL-3.0 仓库。Apache-2.0 的 llmkit 不得复制、改写搬运、静态链接或以其为依赖分发。只能根据公开 Provider 协议进行 clean-room 实现，并独立编写 fixtures 和 tests。

#### 2. `any` 和运行时 DTO 推断削弱类型安全

转换入口接收和返回 `any`，通过具体 DTO 类型选择 converter。它适合网关内部支持多种外部协议，但不适合作为 llmkit 的主要 host-facing API。llmkit 的稳定 API 应使用统一强类型 Request/Event/Response，Provider 原始 DTO 只留在 adapter 内部。

#### 3. DTO 范围仍带有产品概念

RelayKit 的 `dto` 还包含 pricing、channel settings、user settings、notify 等并非协议转换所必需的类型。llmkit 应保持 protocol DTO、host-facing DTO 和产品 DTO 的严格边界。

#### 4. 全局 MediaResolver 不适合多租户 SDK

全局 resolver 难以表达请求级区域、认证和安全策略。llmkit 应把 resolver 放入请求选项或不可变 client 实例，不使用 package global。

#### 5. 多跳转换可能放大语义损失

某些 Claude/Gemini 路径需要经过中间协议。llmkit 应优先执行统一语义模型到 Provider 原生协议的直接转换；只有显式 compatibility API 才允许协议到协议转换，并返回 loss report。

## TokenHub Provider 实现分析

### 实际 Provider 覆盖

检查版本内只有三个 adapter：

- OpenAI/OpenAI-compatible；
- Anthropic；
- vLLM。

TokenHub 的竞争力主要来自路由、健康、预算、观测和编排，而不是 Provider 协议覆盖。

### 优点

#### 1. 小核心接口和可选能力接口

核心 `Sender` 只有 ID、Send 和 ClassifyError；stream、health probe、raw Anthropic 和 embeddings 使用独立可选接口。这比 new-api 的大 Adaptor 更适合能力发现。

llmkit 应进一步改进：核心只标识 Provider，Generate、Stream、Embed、Rerank、Models 等均为独立能力接口，并提供结构化 `Capabilities`。

#### 2. 每次请求调用动态 KeyFunc

TokenHub 的 adapter 在每次发送前调用 key resolver，支持 Vault 解锁和 key rotation，不需要重启。这比构造时读取环境变量更好。

但 resolver 仍绑定 adapter，无法天然表达不同租户同时调用。同类思想在 llmkit 中应升级为 request-scoped `CredentialHandle`：

```go
type CredentialProvider interface {
    Resolve(context.Context, Target) (Credential, error)
}
```

#### 3. 共享 HTTP helper 提供一致行为

公共 helper 统一处理：

- `http.NewRequestWithContext`；
- JSON marshal；
- Content-Type 和 Provider Header；
- request ID；
- W3C trace context；
- 非 2xx error body；
- `Retry-After` 秒数和 HTTP-date；
- stream body close 时结束 span。

llmkit 应提供可注入 Transport，并统一执行 timeout、cancellation、body limit、request ID、safe tracing 和错误 envelope 读取。

#### 4. Provider 自己分类错误

OpenAI、Anthropic 和 vLLM 根据各自 HTTP status/body 识别 rate limit、quota、context overflow、transient 和 fatal。Provider-specific classifier 属于 adapter 的正确职责。

#### 5. 为路由层输出了有用信号

TokenHub 使用 error class、Retry-After、latency、failure outcome 和 health probe 构建健康状态、cooldown 和候选评分。llmkit 不应实现这套路由，但应稳定输出足够信号，让宿主能够自行实现。

### 缺点和限制

#### 1. response 和 stream 都是 raw 数据

非流式返回 `json.RawMessage`，流式返回 `io.ReadCloser`。Provider 层没有统一解析：

- text/reasoning/tool events；
- finish reason；
- usage；
- Provider request ID；
- 流内错误和 malformed event。

因此 TokenHub Provider 层不是完整的 Provider SDK。llmkit 必须返回标准化 Response/Event，而不是把解析责任交给宿主。

#### 2. 错误包含完整 body

`StatusError.Body` 直接进入 `Error()`。上游错误可能回显 prompt、参数、账户信息或其他敏感字段。llmkit 必须限制 error body、解析安全字段并执行 secret/content redaction；完整原始 body 只能在调用者显式提供的受控诊断 sink 中处理。

#### 3. 网络错误依赖字符串匹配

`IsNetworkError` 检查错误字符串，并把 `context.Canceled` 视为 transient。取消是调用者决定，不应被当成可重试 Provider 故障。llmkit 应优先使用 `errors.Is`、`net.Error` 和结构化 transport phase，并把 canceled、deadline、DNS、connect、TLS、read 分开。

#### 4. 流式请求把 HTTP client timeout 设为零

长流不能使用简单总时长 timeout，但完全清零只依赖 context，容易被忘记配置。llmkit 应区分 connect/header timeout、idle/read timeout 和宿主 deadline。

#### 5. 任意 Parameters 直接透传

TokenHub 把 `map[string]any` 合并进 Provider payload，只保护少数 reserved keys。优点是灵活，缺点是：

- 拼写错误不会被发现；
- 不支持的参数可能被静默接受或拒绝；
- Provider-specific 字段污染统一 API；
- 无法生成可靠能力矩阵。

llmkit 应采用类型化通用参数，并为 Provider 扩展提供显式、带 namespace 的 escape hatch；默认未知参数返回错误。

#### 6. adapter 内部包含 endpoint round-robin

vLLM adapter 在多个 endpoint 间轮询，已属于路由行为。llmkit 的一个 Target 应表示一个明确 endpoint/region；多 endpoint 选择留给宿主，避免 SDK 隐式跨边界。

#### 7. Provider 测试不是可复用合同套件

TokenHub 有较好的单元测试，但没有所有 adapter 必须运行的统一 non-streaming、streaming、usage、tool、error、malformed 和 cancellation contract suite。llmkit 应把 conformance package 作为一等交付物。

## llmkit clean-room 借鉴方案

### 1. 分层结构

```text
host-facing typed API
  -> provider adapter
      -> native codec
      -> credential applicator
      -> shared transport
      -> stream parser/state/finalizer
  -> normalized response / events / usage / provider error
```

- Adapter 只处理 Provider 协议差异；
- Transport 统一 HTTP、安全、取消和超时；
- Codec 不访问网络或全局配置；
- Host-facing API 不暴露上游 DTO；
- Router、billing、health persistence 均在宿主。

### 2. 小接口和能力发现

借鉴 TokenHub 的 capability interface，避免 new-api 大接口：

```go
type Provider interface {
    ID() ProviderID
    Capabilities(context.Context, Target) (Capabilities, error)
}

type Generator interface {
    Generate(context.Context, Call) (Response, error)
}

type StreamGenerator interface {
    Stream(context.Context, Call) (EventStream, error)
}

type Embedder interface {
    Embed(context.Context, EmbedCall) (EmbedResponse, error)
}
```

每个方法都接收 `context.Context` 和包含 request-scoped credential 的 Call。

### 3. 标准化流事件

借鉴 RelayKit 的 state/finalize，但定义 llmkit 自有事件：

- message/content block start；
- text delta；
- reasoning delta；
- tool call start/arguments delta/end；
- citation/source；
- usage update；
- finish；
- Provider error。

EventStream 必须保证：

- context cancel 能中止网络读取；
- `Close` 幂等；
- 异常断流不是正常 EOF；
- terminal summary 包含最终 usage、finish reason 和 request ID；
- finalize 阶段补齐必要的结束事件。

### 4. Usage 保留语义和来源

借鉴 new-api 的细粒度 usage，但去掉价格/倍率：

```text
input / output / total
cached read / cached write
reasoning
text / image / audio
provider reported / estimated / missing
provider semantic and safe extensions
```

零值、缺失和 estimated 必须可区分；SDK 不计算最终账单。

### 5. 标准错误和 Transport phase

借鉴 TokenHub 的 provider classifier 和 Retry-After，改进为：

```text
authentication / permission / invalid_request
model_not_found / unsupported_feature / context_length
rate_limit / quota_exhausted / content_blocked
overloaded / timeout / canceled / transport / malformed_response
```

错误同时携带：Provider、model、HTTP status、安全 code、retryable、Retry-After、request ID 和 transport phase。不得携带 credential、完整请求或完整响应。

### 6. 显式转换报告

借鉴 RelayKit 的路径和质量元数据。任何可能损失语义的转换都返回：

- source/target semantic；
- applied adaptations；
- dropped/approximated fields；
- quality/loss level；
- unsupported feature error。

默认不静默删除多模态 part、tool field、reasoning 或 structured-output 约束。

### 7. Conformance suite

每个 Provider 必须通过同一合同：

- non-streaming；
- streaming 和 finalize；
- tool calls；
- structured output；
- multimodal（声明支持时）；
- usage 和 finish reason；
- error taxonomy 和 Retry-After；
- timeout/cancellation；
- malformed JSON/SSE、截断和流内错误；
- body size limit；
- secret redaction；
- capability 声明与实际行为一致。

测试 fixture 必须依据公开 Provider 文档独立编写，不从 AGPL snapshot 搬运。

## 推荐实施顺序

1. 先冻结 Target、CredentialHandle、Transport、ProviderError、Usage 和 Event API。
2. 建立 fake adapter、fake transport 和 conformance harness。
3. 完成 OpenAI Chat/Responses，验证非流式、流式、tool、usage 和 error。
4. 完成 Anthropic Messages，验证跨语义模型和 stream finalization。
5. 完成 Gemini 原生协议，验证 content parts、function call、thinking 和 safety finish。
6. 接入 DeepSeek、DashScope、Volcengine、Zhipu、Moonshot、MiniMax、Hunyuan。
7. 发布逐 Provider/能力的 compatibility matrix。
8. Core 稳定后再实现只做本地 IPC 映射的 llmkit-sidecar。

## 许可证与研究纪律

- new-api 和 RelayKit：AGPL-3.0，不复制、不链接、不改写搬运；
- TokenHub：MIT，可研究其公开接口思想，但 llmkit 仍应独立设计和实现；
- Provider 协议实现以厂商公开文档为规范来源；
- 上游项目只用于发现边界条件和验证设计取舍；
- llmkit fixtures、contract tests 和实现必须具有独立来源记录。

## 上游源码入口

- <https://github.com/QuantumNous/new-api/tree/afe16c64cd73853da1eda3bf236f15d69637b4bf/relay>
- <https://github.com/QuantumNous/new-api/tree/afe16c64cd73853da1eda3bf236f15d69637b4bf/relaykit>
- <https://github.com/QuantumNous/new-api/blob/afe16c64cd73853da1eda3bf236f15d69637b4bf/relay/channel/adapter.go>
- <https://github.com/QuantumNous/new-api/blob/afe16c64cd73853da1eda3bf236f15d69637b4bf/relay/common/relay_info.go>
- <https://github.com/QuantumNous/new-api/blob/afe16c64cd73853da1eda3bf236f15d69637b4bf/relaykit/README.md>
- <https://github.com/jordanhubbard/tokenhub/tree/7ba47728397ecc5e44263683e709084b5fd78658/internal/providers>
- <https://github.com/jordanhubbard/tokenhub/blob/7ba47728397ecc5e44263683e709084b5fd78658/internal/router/engine.go>
- <https://github.com/jordanhubbard/tokenhub/blob/7ba47728397ecc5e44263683e709084b5fd78658/internal/health/tracker.go>
