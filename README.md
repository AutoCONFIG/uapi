# UAPI

Your Unified AI API Gateway.

UAPI 是一个统一的 AI API 网关，支持 OpenAI、Anthropic、Google Gemini 等多家大模型服务商。通过 UAPI，你可以用一套 API 接口管理所有上游渠道，统一鉴权、计费和流量调度。

## 特性

- **多供应商支持** — OpenAI Chat Completions API、OpenAI Responses API、Anthropic Messages API、Gemini API，统一转为内部格式并按需互转
- **OpenAI 兼容接口** — 下游客户端只需对接 `/v1/chat/completions`，即可路由到任意供应商
- **多账号池 & 加权轮询** — 同一渠道可挂载多个上游账号，按权重自动调度
- **API Key 管理** — 普通用户默认一个密钥，支持查看、复制、IP 白名单、过期时间、模型限制和端点权限
- **用量计费** — 预扣费 / 结算 / 退款，按 token 精确计量
- **管理后台** — 渠道/账号凭据、节点、用户、套餐、日志、系统设置和操作审计
- **Gateway / Relay 架构** — Gateway 统一鉴权、策略、计费和调度；Relay 节点只执行转发
- **用户控制台** — 注册登录、密钥管理、用量查询、套餐订阅
- **Code 客户端接入** — 支持 Codex、Gemini Code、Claude Code、Antigravity 等 OAuth 登录、账号元数据同步和自动刷新
- **本地模型目录** — 下游模型列表从本地渠道配置读取，管理员可手动同步上游模型并设置模型重定向
- **流式转发** — SSE 流式响应透明转发，支持流式转非流式

## 快速开始

### 前置条件

- Go 1.26+
- PostgreSQL 17+
- Node.js 20+ (前端)

### 启动后端

```bash
# 配置数据库
cp config.example.yaml config.yaml
# 编辑 config.yaml，填入数据库连接和密钥

# 启动开发数据库
make dev

# 编译运行
make build
./bin/uapi -config config.yaml
```

### Docker Compose 部署

默认单机部署，运行 PostgreSQL、Gateway/API、本机 Relay 和前端。只有前端 `80` 端口暴露到宿主机，数据库、Gateway 内部端口和本机 Relay 都不对外暴露：

```bash
cp config.example.yaml config.yaml
# 首次启动会自动写入随机 jwt_secret/encryption_key/internal_secret
docker compose up -d --build
```

同一台机器模拟 Gateway/Relay 分离：

```bash
cp config.gateway.example.yaml config.gateway.yaml
# 首次启动会自动写入随机 jwt_secret/encryption_key/internal_secret
docker compose -f docker-compose.gateway.yaml up -d --build

cp config.relay.example.yaml config.relay.yaml
# 在后台创建 Relay Node，base_url 填 http://relay:8081
# 把 Gateway 的 security.encryption_key、gateway.internal_secret
# 以及节点 ID 写入 config.relay.yaml
docker compose -f docker-compose.relay.yaml up -d --build
```

远端机器运行 Relay，不需要 PostgreSQL/Redis；此时 Relay 需要对 Gateway 可达：

```bash
cp config.relay.example.yaml config.relay.yaml
# 编辑 gateway.control_url、gateway.relay_node_id，并复制 Gateway 的
# gateway.internal_secret 与 security.encryption_key
docker compose -f docker-compose.relay.remote.yaml up -d --build
```

### 启动前端

```bash
npm --prefix web install
npm --prefix web run dev
```

生产构建：

```bash
npm --prefix web run build
npm --prefix web run serve:static
```

## 项目结构

```
cmd/uapi/          程序入口
internal/
  server/          HTTP 服务器 & 路由
  gateway/         Gateway 鉴权、策略、节点调度、反向代理
  relay/           Relay 执行引擎 (格式转换、上游转发、流式)
    provider/      上游适配器 (OpenAI / Anthropic / Gemini)
  admin/           管理后台 API
  user/            用户系统 API
  auth/            JWT 认证
  db/              数据模型 (GORM)
  crypto/          AES-256-GCM 加密
  config/          配置加载
web/               Next.js 前端
docs/              项目文档
```

## API 概览

| 路径前缀 | 说明 |
|----------|------|
| `/v1/*` | 中继接口 (OpenAI 兼容) |
| `/v1beta/*` | Gemini 兼容中继接口 |
| `/api/user/*` | 用户 API |
| `/api/admin/*` | 管理后台 API |

## 技术栈

- **后端**: Go / fasthttp / GORM / PostgreSQL / AT/RT JWT (HS256) / AES-256-GCM
- **前端**: Next.js 15 / React / TypeScript / 纯 CSS

## 部署

推荐优先使用 Docker Compose：

```bash
docker compose up -d --build
```

架构细节见 [docs/current/gateway-relay.md](docs/current/gateway-relay.md)。Nginx/Systemd 方式见 [docs/deployment/nginx.md](docs/deployment/nginx.md)。

## 文档

- [文档索引](docs/README.md)
- [项目交接文档](docs/current/handoff.md)
- [前端文档](docs/current/frontend.md)
- [平台设计](docs/current/platform-design.md)
- [阶段范围与路线](docs/current/roadmap.md)
- [Code 渠道对齐](docs/current/code-channels.md)

## License

Private. All rights reserved.
