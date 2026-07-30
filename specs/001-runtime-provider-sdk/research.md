# Research Decisions

- 借鉴 new-api/relaykit 的 adapter 拆分、渠道复用和兼容入口，但拒绝把业务路由、账户、计费与 Provider codec 耦合。
- 借鉴 TokenHub 的多模型聚合和 OpenAI 生态兼容，但 llmkit 保持 SDK/sidecar 定位，不成为完整业务平台。
- 国内平台的“OpenAI compatible”存在参数、流和错误差异，因此使用公共 codec + 独立 Provider profile + fixtures，而不是一个 base URL 表。
- Go runtime plugin 因跨平台和 ABI 风险不进入 v1；高级动态扩展保留独立进程/WASM 方向。
