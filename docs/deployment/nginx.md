# UAPI Nginx Reverse Proxy Configuration

UAPI is designed to run behind an nginx reverse proxy with HTTPS. In the current
deployment shape, nginx serves the exported Next.js frontend from `web/out` and
proxies backend traffic to the Go API server.

## Basic Configuration

```nginx
map $http_upgrade $connection_upgrade {
    default upgrade;
    '' close;
}

server {
    listen 443 ssl http2;
    server_name relay.example.com;

    ssl_certificate     /etc/ssl/certs/relay.example.com.pem;
    ssl_certificate_key /etc/ssl/private/relay.example.com.key;

    root /opt/uapi/web/out;
    index index.html;
    client_max_body_size 256m;

    # Backend health check.
    location = /healthz {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }

    # Static frontend export.
    location / {
        try_files $uri $uri/ /index.html;
    }

    # Go API server. config.example.yaml uses port 8080.
    location /api/ {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }

    # OpenAI-compatible relay traffic.
    location /v1/ {
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection $connection_upgrade;
        proxy_buffering off;
        proxy_request_buffering off;
        proxy_cache off;
        proxy_read_timeout 3600s;
        proxy_send_timeout 3600s;
    }

    # Gemini-compatible relay traffic.
    location /v1beta/ {
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection $connection_upgrade;
        proxy_buffering off;
        proxy_request_buffering off;
        proxy_cache off;
        proxy_read_timeout 3600s;
        proxy_send_timeout 3600s;
    }

    # Remote Relay control endpoints. Restrict this location to trusted Relay
    # node IPs or a private network; UAPI still requires the internal secret.
    location /internal/relay/ {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}
```

## Key Headers

| Header | Purpose |
|--------|---------|
| `X-Forwarded-Proto` | UAPI uses this to detect HTTPS and generate correct OAuth callback URLs |
| `X-Real-IP` | For logging real client IPs |
| `Host` | Required for generating correct redirect URLs |

## Request Body Size

Keep nginx `client_max_body_size` aligned with `server.max_body_size_mb` in the
UAPI config. The default example is `256m`, leaving room for base64 expansion
and JSON wrapping so Anthropic/Gemini/Responses-format requests with PDFs or
long document fields reach UAPI instead of being rejected by nginx with an HTML
`413 Request Entity Too Large` response.

The repository-local `config.yaml` is ignored by git and is the file mounted by
the default Docker Compose deployment. Keep its `server.max_body_size_mb` aligned
with `web/nginx.conf` when testing large uploads locally.

## OAuth Completion Flow

Code channel OAuth currently uses provider-specific manual callback redirect
URIs that match the official clients. The admin UI starts the session, opens the
provider URL, then completes the session with
`POST /api/admin/channels/oauth/complete` using either the returned callback URL
or token JSON. The public `GET /api/admin/channels/oauth/callback` route remains
available for UAPI-hosted callback flows, so keep `X-Forwarded-Proto: https`
configured correctly when deploying those flows behind nginx.

## Responses WebSocket

All-in-one deployments can upgrade `/v1/responses` to WebSocket. The `/v1/`
location must keep `proxy_http_version 1.1`, `Upgrade`, and `Connection`
headers. Split gateway/relay deployments do not currently tunnel Responses
WebSocket turns across relay nodes; use all-in-one mode for that path.

## Security Recommendations

- Enable HTTPS with a valid certificate. Let's Encrypt is a good default.
- Use `proxy_set_header X-Forwarded-Proto $scheme` to ensure correct URL generation.
- Restrict admin access with an IP allowlist or another edge control if required by
  the deployment environment.

## Systemd Service

Create `/etc/systemd/system/uapi.service`:

```ini
[Unit]
Description=UAPI
After=network.target

[Service]
Type=simple
User=uapi
WorkingDirectory=/opt/uapi
ExecStart=/opt/uapi/bin/uapi -config /opt/uapi/config.yaml
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
```

Then:

```bash
sudo systemctl daemon-reload
sudo systemctl enable uapi
sudo systemctl start uapi
```
