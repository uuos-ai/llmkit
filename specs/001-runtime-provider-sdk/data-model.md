# Data Model

## Identity

`Principal(client_id, client_instance_id, user_id, binding_version, scopes)`；client 是业务隔离主键，user 仅在 client 内有效。`UserBinding` 每次切换递增版本，所有旧版本 token 和请求失效。

## Token family

access/refresh 都由公开 token_id 与 256-bit secret 组成。服务端保存 HMAC、pepper key version、family、generation、expiry、used 状态；refresh 原子轮换两者。相同 Idempotency-Key 在 60 秒内读取加密结果，不同 key 重用触发 family 撤销。

## Available target snapshot

快照包含 revision、binding_version、generated_at、refresh_after、stale_until、default_target_id 和 targets。target_id 唯一指向 Provider/model/endpoint/region、owner scope、credential mode、capabilities 和 limits。

## Credentials

managed secret 使用稳定 credential_ref 和递增 secret_version，但 reference 只在执行/存储边界内部出现。写入状态为 pending、active、retired；请求取得有界 lease。

## Stream

事件按 sequence 排序，从 response.created 开始，以 response.completed/failed/cancelled 唯一结束。文本/reasoning、工具参数和 usage 使用独立事件种类。

