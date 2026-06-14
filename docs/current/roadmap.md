# 当前范围和暂缓项

本文只记录当前项目范围，不作为长期产品路线承诺。

## 已在当前代码中落地

- 用户注册、登录、refresh token、资料读取、密码和邮箱修改。
- 用户 API Key 创建、列表、删除、IP 白名单、过期时间、模型和 endpoint permission 限制。
- 用户套餐、兑换码、用量汇总和日志查询。
- 管理员初始化、登录、refresh token。
- 管理后台 dashboard、用户、套餐、access policy、兑换码、系统设置、日志、审计日志。
- 渠道、账号、Relay 节点、节点-渠道绑定管理。
- OAuth 渠道创建、授权、导入、绑定、metadata/quota 展示支持。
- Gateway API Key 鉴权、模型列表、模型别名、策略限制、并发限制、预扣费和调度。
- Remote Relay 运行时配置拉取、HMAC 执行请求、usage event 上报和 account update 回写。
- OpenAI Chat Completions、OpenAI Responses、Anthropic Messages、Gemini generateContent 的文本类转换。
- OpenAI-compatible Images/Audio/Embeddings/Moderations/Realtime HTTP/Video 的明确支持矩阵。
- Antigravity image generation/edit/variation 转换。
- 结构化 stdout 日志和数据库请求日志。
- Next.js 静态前端。

## 当前保留边界

- 单 Gateway 是当前控制面边界。
- 远程 Relay 是执行节点，不独立拥有业务配置。
- 用户请求模型列表只读本地数据库。
- 管理端手动触发 channel model sync。
- 套餐绑定用户，不绑定 API Key。
- `token_plans` 表名保留，但业务含义是用户套餐订阅。

## 暂缓项

- 多 Gateway 控制面。
- 分布式限流和分布式计费锁。
- Redis 作为 Relay 热路径状态。
- Relay 配置长连接推送。
- Gateway -> Relay 独立 `RelayClient`，支持应用层优先 HTTP/3、失败退回 HTTP/2。
- Provider-native WebSocket/realtime 桥接的完整支持矩阵和 dump 关联增强。
- mTLS。
- usage event 和 OAuth account update 的持久重试队列。
- 节点级凭据加密。
- 在线支付。
- 邮箱验证。
- 多租户组织。
- 管理员侧独立 API Key 产品页面。

## 文档维护规则

已经移除或尚未实现的功能不写成当前事实。需要保留想法时只能放在“暂缓项”，不能混入实现说明。
