# Tasks: llmkit 通用 Provider SDK 与三模式运行时

## Completed foundation

- [x] T001 初始化 spec-kit 与项目 constitution
- [x] T002 实现三模式 `llmkitd`、配置优先级与多实例 endpoint
- [x] T003 实现 Provider registry、国内 P0 profiles 和基础 conformance
- [x] T004 实现 client/user Principal、动态 UserBinding 与 binding_version
- [x] T005 实现模式 token 前缀、token family 双轮换、重放撤销和幂等恢复参考实现
- [x] T006 实现完整目标快照类型、原子 client cache 和动态默认
- [x] T007 实现 owner/credential scope、managed storage ports 和 request-scoped handles
- [x] T008 实现规范错误词汇、usage 明细和 stream state normalizer
- [x] T009 实现 gateway scopes、可用目标与 `/v1/models` target 映射
- [x] T010 实现端点 HTTPS/私网/路径/重定向基础防护

## Remaining delivery work

- [x] T011 增加原生 rerank/moderate interfaces、IPC 与 gateway handlers
- [ ] T011A [P1] 增加首个 rerank/moderate Provider adapter 与 conformance fixtures
- [x] T012 [P1] 完成 OpenAI chat/responses/embeddings HTTP compatibility handlers
- [x] T013 将 gateway data/control handler 接入独立生产 listener
- [x] T014 [P1] 实现外部 SessionStore/UserBindingStore/RateLimitStore HTTP adapters
- [x] T015 实现 bounded blob upload/store 与 client/user/binding 隔离测试
- [ ] T016 [P1] 实现 local enrollment 与 gateway OIDC exchange endpoints
- [ ] T017 [P1] 为所有 P0 Provider 补齐成熟度 manifest 与完整统一 conformance matrix
- [ ] T018 [P1] 完成 Qianfan/SiliconFlow/Azure/Bedrock/Vertex P1 adapters
- [ ] T019 [P1] 扩展 Rust client；生成 TypeScript/Python schema DTO
- [ ] T020 [P1] 完整 race、跨平台、secret leak、SSRF/DNS rebinding 与升级测试
