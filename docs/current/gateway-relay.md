# Gateway / Relay 架构

本文记录当前实现，不描述未落地的分布式控制面。

## 分工

Gateway 是唯一控制权威：

- 用户 API Key 鉴权。
- active plan 和 access policy 校验。
- 模型可见性和模型别名解析。
- Relay node、channel、account 的调度。
- 预扣费、结算、退款。
- 管理 API、用户 API、审计和日志。
- 远程 Relay 运行时配置下发。

Relay 是执行节点：

- 接收 Gateway 签名的内部请求。
- 按 Gateway 指定的 channel/account 执行上游请求。
- 做凭据解密、OAuth 刷新、协议转换、流式转发和 usage 解析。
- 回传 usage event 和 OAuth account update。
- 远程 Relay 模式不连接 PostgreSQL，不提供管理 API。

## 请求流

```text
1. Client -> Gateway: /v1/* 或 /v1beta/*，携带 Bearer API Key
2. Gateway 校验 key、IP、权限、模型、active plan 和 access policy
3. Gateway 根据本地 channel/account/node 状态选择 relay_node + channel + account
4. Gateway 预扣估算额度并生成 request_id
5. Gateway HMAC 签名后转发给 Relay
6. Relay 校验签名，执行上游请求
7. Relay 将响应 body/stream 返回 Gateway
8. Relay 上报 usage event
9. Gateway 幂等结算或退款
```

`server.mode: all` 下 Gateway 可直接 fallback 到本地 in-process Relay。`server.mode: gateway` 下通常调度远程 Relay；远程节点不可用时会尝试本地 fallback handler，但纯 gateway 模式没有本地 relayer。

## 节点和绑定

后台管理：

- `/api/admin/relay-nodes` 管理节点名称、地址、地区、出口 IP、权重、最大并发、状态和健康状态。
- `/api/admin/node-channels` 绑定 `relay_node_id + channel_id`。

运行时调度会把 channel-level binding 展开到该 channel 下所有 enabled accounts。Gateway 调度评分综合节点权重、账号权重和当前并发。

可调度条件：

- relay node 未软删，`status = active`，`health_status = healthy`。
- node-channel binding enabled。
- channel enabled 且未软删。
- account enabled 且未软删。
- channel 模型目录或别名支持当前请求模型。
- node 未处于 passive failure cooldown。

## 配置同步

Remote Relay 通过：

```text
GET /internal/relay/config
```

定时拉取分配给自己的运行时配置。间隔来自 `gateway.config_pull_interval`，默认 5 秒。请求热路径只读 Relay 进程内内存。

运行时配置包含：

- 绑定到该节点的 channel。
- channel 下可用 account。
- channel 模型、别名、协议、设置。
- account credential、endpoint、weight、token expiry、metadata 等。

配置有版本号；未变化时 Relay 可跳过更新。删除、禁用或解绑会进入版本计算，避免远程 Relay 长期保留过期路由。

## 内部认证

Gateway 到 Relay 的执行请求使用 `internal/internalauth` HMAC 签名。签名 claims 包含：

- gateway id
- token id
- user id
- model
- estimated tokens
- request id
- selected channel id
- selected account id
- precharged token plan id
- client ip

Remote Relay 应设置：

```yaml
server:
  mode: relay
gateway:
  require_internal: true
```

控制接口 `/internal/relay/config`、`/internal/relay/usage-events`、`/internal/relay/account-update` 使用 `X-UAPI-Internal-Secret`。

## 模型列表

模型列表由 Gateway 本地处理：

- `GET /v1/models`：OpenAI-compatible 响应。
- `GET /v1/models` + `x-api-key`：Anthropic-compatible 模型列表响应。
- `GET /v1beta/models`：Gemini-compatible 响应。

这些 endpoint 不访问上游 provider。

## OAuth 刷新回写

Relay 执行 OAuth account 时会按 provider 规则刷新 token。远程 Relay 成功刷新后：

1. 先更新本地内存中的 runtime account。
2. 再调用 `POST /internal/relay/account-update` 回写 Gateway。
3. Gateway 只接受同 account/channel 的更新，并继续加密存储。

当前回写是 best-effort；Gateway 不可达时 Relay 保留内存中的新 credential 并记录 warning，但没有持久重试。

## 当前限制

- 不使用 Redis 作为 Relay 运行时状态。
- 不实现多 Gateway 控制面、GSLB、mTLS、gRPC stream 或长连接配置推送。
- usage event 回传没有持久重试队列。
- 节点级凭据加密未实现。
- 分离式部署下 `/v1/responses` 仍走 HTTP/SSE Relay 路径；WebSocket turn handling 只在 `all` 模式本地处理。
