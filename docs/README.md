# UAPI 文档索引

本目录只维护和 UAPI 当前代码一致的项目文档。`docs/api-reference/` 是外部协议参考资料，不参与本次项目文档整理，也不作为产品路线说明。

## 阅读顺序

1. [current/handoff.md](current/handoff.md)：当前实现概览、运行模式、命令和已知边界。
2. [current/platform-design.md](current/platform-design.md)：后端架构、数据模型、鉴权、计费和协议转换。
3. [current/gateway-relay.md](current/gateway-relay.md)：Gateway/Relay 分工、调度、配置同步和远程 Relay 行为。
4. [current/frontend.md](current/frontend.md)：前端路由、页面职责和后端 API 对齐。
5. [current/oauth-channels.md](current/oauth-channels.md)：OAuth 渠道、支持的 provider 和刷新规则。
6. [deployment/nginx.md](deployment/nginx.md)：单机、Gateway/Relay、反向代理部署说明。

## 目录说明

- `current/`：当前实现的事实文档。代码变化后应优先更新这里。
- `deployment/`：部署和运维说明。
- `api-reference/`：OpenAI、Gemini、Anthropic 等上游协议资料，仅用于协议对齐。

## 维护规则

- 以代码为准，文档只记录已经实现或明确保留的当前设计。
- 不保留历史交接流水账、旧分支信息、旧目标架构和已经不存在的文件链接。
- 不在项目文档里复制大段外部 API 文档；需要协议细节时链接到 `api-reference/`。
- 新增功能时同步更新对应 `current/` 文档，避免后续实现被过时描述误导。
