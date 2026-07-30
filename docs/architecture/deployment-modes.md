# llmkitd 部署模式、动态目录与存储边界

## 已批准基线

`llmkitd` 是同一个 Provider SDK 的可选进程边界，不是另一套 Provider
实现。模式通过默认配置和环境变量/CLI 切换：

| 模式 | 传输 | 身份 | 自定义 Provider | 状态 |
| --- | --- | --- | --- | --- |
| `sidecar`（默认） | UDS / named pipe，framed JSON | stdin 单会话密钥 | `disabled`，A2 本地合并 | 无状态；父进程退出即退出 |
| `local-service` | UDS / named pipe，framed JSON | 每客户端 token → tenant/client | `disabled` 或 `managed` | 默认无状态；可注入本地持久化扩展 |
| `gateway` | HTTPS JSON + SSE | 每客户端 token → tenant/client | 强制 `managed` | 必须使用外部 Config/Secret/Audit Store |

同一电脑可运行多个独立实例。每个实例设置不同 `instance_id`，并使用不同
`socket` 或 `listen` 地址；修改进程名不是隔离机制。token 目录和本地目录
快照也必须独立。还可通过不同 OS 用户或容器做更强隔离。握手/健康响应返回
`instance_id`，便于客户端确认连接目标。

## 配置优先级

从低到高固定为：内置默认值 < 严格 YAML/JSON 文件 < `LLMKIT_*` 环境变量
< CLI。未知字段、未知模式、不安全 instance ID、gateway 缺 TLS/存储等都会
在启动前失败。安全字段只在启动时读取；业务目录和默认目标只在客户端触发
刷新时重新读取。Provider 密钥不能作为普通配置、环境变量或 CLI 参数。

最小本地服务配置：

```yaml
mode: local-service
instance_id: desktop-a
socket: /var/run/user/1000/llmkit-desktop-a.sock
client_token_hash_file: /etc/llmkit/clients.json
custom_provider_sync: disabled
```

token 文件权限必须是 `0600`（Windows 使用 ACL），内容只保存 SHA-256：

```json
[
  {
    "tenant_id": "tenant-a",
    "client_id": "editor",
    "token_sha256": "<64 hex chars>",
    "scopes": ["shutdown"]
  }
]
```

gateway 配置：

```yaml
mode: gateway
instance_id: tokyo-1
listen: ":8443"
client_token_hash_file: /etc/llmkit/clients.json
custom_provider_sync: managed
tls:
  certificate_file: /etc/llmkit/tls.crt
  private_key_file: /etc/llmkit/tls.key
storage:
  config_store: https://config.internal
  secret_store: https://vault-adapter.internal
  audit_store: https://audit.internal
```

可用 `LLMKIT_MODE`、`LLMKIT_INSTANCE_ID`、`LLMKIT_SOCKET`、
`LLMKIT_LISTEN` 等同名环境变量覆盖，或使用 `llmkitd --mode ...`。完整字段
以 `runtimeconfig.Config` 和 `llmkitd --help` 为准。

## Provider 目录与动态默认值

本地协议方法 `resolve_provider_options` 与 gateway
`GET /v1/provider-options` 返回统一结构：

```json
{
  "revision": "sha256...",
  "default_target_id": "business-qwen-fast",
  "generated_at": "2026-07-30T00:00:00Z",
  "providers": [
    {
      "id": "business-qwen",
      "source": "business",
      "targets": [
        {
          "id": "business-qwen-fast",
          "target": {"provider": "dashscope", "model": "qwen-plus"}
        }
      ]
    }
  ]
}
```

客户端必须在启动和业务场景触发时刷新并替换本地缓存，不能永久假设默认值。
调用时：

- 明确传 `target_id`：严格使用该 ID；不存在或不属于当前客户端则失败。
- 不传 `target_id`：请求受理时使用该客户端最近一次刷新得到的
  `default_target_id`。
- 每个 Provider 可包含多个目标，因此一个客户端可配置多组
  Provider + model + region + endpoint。

目录快照按可信 tenant/client 键隔离。客户端 A 提交的本地条目不能由客户端
B 解析。

## 自定义 Provider 同步

只有两种模式，不提供 metadata-only 中间态。

### `disabled`（A2）

自定义配置和密钥由客户端自行保存，不同步到业务平台。客户端调用
`resolve_provider_options` 时可在 `local_custom_providers` 中提交不含密钥的
Provider/target 定义；llmkitd 与业务内置目录合并，只在该客户端的进程内存
保存最新快照。真正调用 Provider 时，凭据仍随请求传入并在使用后清理。

### `managed`

完整自定义配置同步到业务平台。gateway 强制此模式，通过：

- `PUT /v1/custom-providers` 新增/更新配置与凭据；
- `DELETE /v1/custom-providers/{provider_id}` 删除；
- `GET /v1/provider-options` 重新取得业务内置项 + 用户自定义项。

gateway 本身不把密钥写入 ConfigStore。`CustomProviderStore` 的业务实现负责
把配置写入业务配置库、把密钥写入 Vault/KMS，并只向执行路径返回
`credential_ref`。HTTP backend 把管理请求转发给业务 ConfigStore 服务，
由业务平台完成原子性、版本冲突与密钥轮换。

## Gateway API 与存储契约

客户端 API：

- `GET /v1/health`
- `GET /v1/provider-options`
- `POST /v1/generate`（`stream=true` 返回 SSE）
- `POST /v1/embed`
- `PUT /v1/custom-providers`
- `DELETE /v1/custom-providers/{provider_id}`

除 health 外均要求 `Authorization: Bearer <client-token>`。生成与 embedding
只接受 `target_id`，不接受客户端 raw target 或 Provider 密钥。响应包含实际
`target_id` 与不可变 `target`；SSE 首事件为 `routing`，随后为规范化 `event`，
最后是 `end` 或规范化 `error`。

`llmkitd` 的 HTTPS backend 使用配置证书做 mTLS 客户端认证，并调用：

- ConfigStore：`GET /v1/provider-options`、`GET /v1/targets/{id|_default}`；
- SecretStore：`POST /v1/credentials:open`，返回请求级 bearer/header 凭据；
- AuditStore：`POST /v1/audit-events`，只含身份、operation、target、outcome、usage；
- 业务自定义配置：`PUT/DELETE /v1/custom-providers...`。

backend 调用携带可信 `X-LLMKit-Tenant-ID` 和 `X-LLMKit-Client-ID`。这些头只能
在 mTLS/服务网格信任边界内使用，公网入口传入的同名头必须被覆盖。核心接口
定义在 `managed` 包，可由业务直接嵌入实现，无需使用 HTTP backend。

## 持久化原则

- sidecar 无状态，不持久化凭据、目录或模型内容。
- local-service 默认无状态；`managed.ConfigStore`/`SecretStore` 是本地 SQLite
  与 OS secret store 等可选适配器的扩展边界。是否缓存由宿主显式配置，
  `disabled` 的 A2 目录默认只驻留内存。
- gateway 必须有外部 ConfigStore、SecretStore、AuditStore，缺任一项启动失败。
- prompt 和 response 默认不进入任何 Store；AuditEvent 只记录非内容元数据与 usage。
