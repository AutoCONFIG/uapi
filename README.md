# UAPI

Your Unified AI API Gateway.

UAPI uses a strict Gateway / Relay split:

- `uapi-gateway`: Web UI, admin/user API, API-key authentication, policy, billing, routing, and Gateway proxy.
- `uapi-relay`: execution node for upstream providers. It accepts signed Gateway execution requests on `/v1/*` and `/v1beta/*` and does not connect to PostgreSQL.

## Features

- OpenAI-compatible `/v1/*` and Gemini-compatible `/v1beta/*` entrypoints on Gateway.
- Multi-provider routing for OpenAI, Anthropic, Gemini, Codex, Claude Code, Gemini Code, and Antigravity style channels.
- Admin console for channels, accounts, relay nodes, policies, users, plans, logs, and settings.
- Relay runtime config pulled from Gateway, with Gateway-triggered reload notifications.
- Internal Gateway/Relay calls protected by HMAC/internal secret and intended to be IP-allowlisted at Nginx.
- Normal request forwarding uses HTTP/SSE on the original API path. WebSocket is reserved as a provider-native realtime/Codex-style endpoint capability, and should only be enabled through Gateway-mediated forwarding when the downstream client also initiates a WebSocket upgrade.

## Quick Start

```bash
cp config.gateway.example.yaml config.gateway.yaml
docker compose -f docker-compose.gateway.yaml up -d
```

Run a Relay node:

```bash
cp config.relay.example.yaml config.relay.yaml
# Edit gateway.control_url, gateway.relay_node_id, gateway.internal_secret, and security.encryption_key.
docker compose -f docker-compose.relay.yaml up -d
```

For a single-host deployment with Gateway, Relay, and PostgreSQL:

```bash
cp config.gateway.example.yaml config.gateway.yaml
cp config.relay.example.yaml config.relay.yaml
# For local Docker networking, set gateway.control_url in config.relay.yaml to http://gateway:8080.
docker compose up -d
```

For local development with build-from-source images:

```bash
cp config.gateway.example.yaml config.gateway.yaml
cp config.relay.example.yaml config.relay.yaml
docker compose -f docker-compose.dev.yaml up -d --build
```

Development access:

```text
http://127.0.0.1       # dev Nginx -> Gateway
http://127.0.0.1:1240  # direct Gateway
http://127.0.0.1:8081  # direct Relay health/internal debug only
```

## Build

```bash
go build ./cmd/uapi-gateway
go build ./cmd/uapi-relay
```

Docker images:

```bash
docker build -f Dockerfile.gateway -t uapi-gateway:test .
docker build -f Dockerfile.relay -t uapi-relay:test .
```

## Project Layout

```text
cmd/uapi-gateway/        Gateway + embedded Web entrypoint
cmd/uapi-relay/          Relay entrypoint
internal/server/         Gateway HTTP server and routes
internal/relayserver/    Relay internal HTTP server
internal/gateway/        Gateway auth, policy, billing, routing, proxy
internal/relay/          Relay execution engine and provider handling
internal/admin/          Admin API
internal/user/           User API
web/                     Next.js frontend embedded into uapi-gateway
server/                  Split deployment examples
docs/                    Local docs
```

## Internal Paths

Gateway exposes user-facing APIs:

```text
/api/*
/v1/*
/v1beta/*
```

Relay calls Gateway:

```text
GET  /internal/config
POST /internal/usage
POST /internal/account
POST /internal/dumps
```

Gateway calls Relay:

```text
POST /v1/*
POST /v1beta/*
POST /internal/reload
```

Normal `/v1/*` and `/v1beta/*` requests are not converted to WebSocket internally. If an upstream provider has an optional WebSocket protocol, Relay should only use it for matching downstream WebSocket/realtime paths after Gateway auth/routing support exists, while ordinary Responses/Chat requests stay on HTTP/SSE.

See [docs/current/gateway-relay.md](docs/current/gateway-relay.md) and [docs/deployment/nginx.md](docs/deployment/nginx.md).
