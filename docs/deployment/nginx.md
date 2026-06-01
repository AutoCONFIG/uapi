# 部署说明

本文记录当前仓库支持的部署方式。推荐先使用 Docker Compose，再由宿主机 nginx/Caddy/Traefik 暴露公网入口。

## 单机生产部署

默认 `docker-compose.yaml` 使用 GHCR 镜像，包含：

- PostgreSQL
- UAPI 后端，默认 `server.mode: all`
- 静态前端服务

默认只暴露本机回环端口：

```text
127.0.0.1:3000  前端
127.0.0.1:8080  Gateway/API
```

启动：

```bash
cp config.example.yaml config.yaml
docker compose pull
docker compose up -d
```

首次启动如果 secret 缺失，后端会生成 `security.jwt_secret`、`security.encryption_key` 和 `gateway.internal_secret` 并写回配置文件。

## 开发部署

```bash
cp config.example.yaml config.yaml
docker compose -f docker-compose.dev.yaml up -d --build
```

开发 compose 会本地构建后端和前端，并保留前端 nginx 反代，便于用同一入口测试页面和 API。

也可以本地直接运行：

```bash
go run ./cmd/uapi/
npm --prefix web run dev
```

## 远程 Relay 节点

Gateway 机器：

1. 后台创建 Relay Node，记录节点 ID。
2. 后台把需要执行的 channel 绑定到该节点。
3. 确保 Gateway 可被 Relay 访问，且 `/internal/relay/*` 只暴露给可信网络或有额外边界保护。

Relay 机器：

```bash
cp config.relay.example.yaml config.relay.yaml
docker compose -f docker-compose.relay.yaml up -d --build
```

`config.relay.yaml` 必填：

```yaml
server:
  mode: relay
gateway:
  require_internal: true
  control_url: https://gateway.example.com
  relay_node_id: <后台创建的 Relay Node UUID>
  internal_secret: <与 Gateway 一致>
security:
  encryption_key: <与 Gateway 一致>
```

Relay 节点的 `base_url` 应填写 Gateway 能访问到该 Relay 的地址，例如 `https://relay-1.example.com`。

## 反向代理路径

宿主机反代建议按路径分流：

```text
/                 -> frontend:3000
/_next/static/*   -> frontend:3000
/api/*            -> uapi:8080
/internal/relay/* -> uapi:8080
/v1               -> uapi:8080
/v1/*             -> uapi:8080
/v1beta           -> uapi:8080
/v1beta/*         -> uapi:8080
```

`/internal/relay/*` 是内部控制接口。如果公网可达，必须额外限制来源或放到内网域名。

## nginx 示例

```nginx
server {
    listen 443 ssl http2;
    server_name uapi.example.com;

    client_max_body_size 256m;

    proxy_read_timeout 360s;
    proxy_send_timeout 360s;
    proxy_connect_timeout 30s;

    location /api/ {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }

    location /internal/relay/ {
        allow 10.0.0.0/8;
        allow 172.16.0.0/12;
        allow 192.168.0.0/16;
        deny all;

        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }

    location /v1 {
        proxy_pass http://127.0.0.1:8080;
        proxy_buffering off;
        proxy_request_buffering off;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }

    location /v1beta {
        proxy_pass http://127.0.0.1:8080;
        proxy_buffering off;
        proxy_request_buffering off;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }

    location / {
        proxy_pass http://127.0.0.1:3000;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}
```

流式请求使用 UAPI 应用层 idle timeout。`server.stream_idle_timeout_seconds` 默认 300 秒，因此反代 `proxy_read_timeout` 应大于该值。

## 常见检查

```bash
docker compose ps
docker compose logs -f uapi
curl http://127.0.0.1:8080/healthz
```

如果远程 Relay 无法接收请求，先检查：

- Relay 后台节点状态是否 active/healthy。
- node-channel binding 是否启用。
- Relay `base_url` 是否能从 Gateway 访问。
- Gateway 和 Relay 的 `gateway.internal_secret` 是否一致。
- Relay 是否设置 `gateway.require_internal: true`。
- Relay `gateway.relay_node_id` 是否是后台节点的 UUID。
