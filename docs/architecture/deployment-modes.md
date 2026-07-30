# llmkitd 三种部署模式与安全边界

完整批准需求见 [spec-kit feature spec](../../specs/001-runtime-provider-sdk/spec.md)。本文只说明部署视图。

| 模式 | 默认传输 | client 身份 | user 绑定 | 凭据与状态 |
| --- | --- | --- | --- | --- |
| `sidecar`（默认） | UDS / named pipe | 父 SDK 启动 token | 当前进程 | 无状态，request-scoped |
| `local-service` | UDS / named pipe | enrollment 后的 `llmk_l1_` | 默认 instance 内，可选共享 store | 默认无状态；可选 SQLite + OS Keychain |
| `gateway` | HTTPS JSON/SSE | OIDC/业务凭证 exchange 后的 `llmk_g1_` | 集群共享强一致 store | 外部 Config/Secret/Session/UserBinding/RateLimit/Audit Store |

## 多实例

每个进程使用不同 `instance_id + endpoint`。进程名称只用于显示，不参与发现、隔离或认证。sidecar 由一个父 SDK 独占；多个业务共享进程使用 local-service；跨机器使用 gateway。

配置优先级固定为：内置默认 < 严格 YAML/JSON < `LLMKIT_*` 环境变量 < CLI。安全配置只在启动时加载，密钥不得通过普通配置、命令行或环境变量传入。

local-service 的 `client_token_hash_file` 保存受限权限的一次性 `llmk_le1_` token 哈希及待签发 scopes；客户端完成 `enroll_client` 后改用返回的 `llmk_l1_` / `llmk_lr1_`。gateway 的 `business_service_token_file` 只保存文件路径，文件内容在每次业务 API 请求时读取，因此短期 service token 可原子轮换且不经环境变量或命令行传递。

## 身份模型

`client_id` 表示一个独立业务。一个 client 同时最多绑定一个当前 `user_id`，但 user 可切换；`user_id` 只在 client 内有意义：

```text
client_a/user_1 != client_b/user_1
```

同一个现实人物在不同业务中的不同 user ID 永不关联。用户切换原子递增 `binding_version`，旧 access/refresh token、目标快照、凭据租约和在途流立即失效。同一 client 的多个 `client_instance_id` 共享当前 user；每个 instance 最多一个活动会话。

## 可用目标清单

统一使用“可用目标清单”，不使用“Provider 目录”。本地方法为 `get_available_targets`，gateway 为 `GET /v1/available-targets`；旧的 `resolve_provider_options` 和 `/v1/provider-options` 仅是 protocol v1 兼容别名。

快照包含 revision、binding_version、generated_at、refresh_after、stale_until、default_target_id 和 targets。客户端在启动、用户切换、场景触发及配置变更后原子刷新完整快照。显式 target 严格选择；省略使用最新默认。OpenAI 兼容接口用 `model: llmkit-default` 表达动态默认。

启用同步时，业务 API 返回 business/client/user 三层的完整快照并作为唯一权威，llmkit 只缓存。禁用同步时合并本地内置、client 自定义和当前 user 自定义；默认 fresh 15 分钟，刷新失败可 stale 30 分钟。

## 凭据模式

- sidecar：request-scoped、workload、none。
- local-service：同 sidecar；启用 managed 后支持内部 credential_ref + OS Keychain。
- gateway：managed/workload/none；request-scoped 只有业务策略显式允许才开放。

target 显式绑定 credential mode，不跨 business/client/user scope 回退。credential_ref 只存在于 ConfigStore → SecretStore 执行链路，不返回客户端。managed secret 使用 pending → active → retired 生命周期。

## 控制面与数据面

数据面包含目标读取、推理、流、blob 与 token refresh。控制面包含 client/enrollment、Provider/凭据写入、runtime policy 与审计。生产 gateway 使用必填且不同的 `listen` / `admin_listen`；local-service 使用独立 admin IPC；sidecar 即使共用 pipe 也使用独立 namespace 与 scopes。

gateway 的 `storage.coordination_store`（或 `LLMKIT_COORDINATION_STORE` / `--coordination-store`）提供强一致的 SessionStore、UserBindingStore、RateLimitStore 与 IdentityService HTTP 合同。每次认证都校验当前 binding；推理前获取跨副本限流 lease，结束后提交或释放。所有业务 store 请求同时携带 mTLS client certificate 与 `business_service_token_file` 中的短期 bearer token。

## 持久化

- sidecar 不持久化业务状态。
- local-service 默认无状态；managed profile 将非秘密元数据写 SQLite、秘密写 OS Keychain。
- gateway 使用业务实现的外部 ports。
- prompt、response 和工具参数默认不进入任何 store/log/trace/audit。
