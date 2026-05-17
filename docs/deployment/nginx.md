# UAPI Nginx Reverse Proxy Configuration

UAPI is designed to run behind an nginx reverse proxy with HTTPS. In the current
deployment shape, nginx serves the exported Next.js frontend from `web/out` and
proxies backend traffic to the Go API server.

## Basic Configuration

```nginx
server {
    listen 443 ssl http2;
    server_name relay.example.com;

    ssl_certificate     /etc/ssl/certs/relay.example.com.pem;
    ssl_certificate_key /etc/ssl/private/relay.example.com.key;

    root /opt/uapi/web/out;
    index index.html;

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
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;

        # WebSocket support, if relay streaming needs upgrade semantics later.
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
    }
}
```

## Key Headers

| Header | Purpose |
|--------|---------|
| `X-Forwarded-Proto` | UAPI uses this to detect HTTPS and generate correct OAuth callback URLs |
| `X-Real-IP` | For logging real client IPs |
| `Host` | Required for generating correct redirect URLs |

## OAuth Callback Flow

> **Note:** OAuth onboarding is not yet implemented (see known gaps in `docs/current/handoff.md`).
> The `X-Forwarded-Proto` header below will be required once OAuth endpoints are active.

When using browser-based OAuth login from a remote client:

1. User initiates login via Web UI or API.
2. UAPI generates an auth URL with a callback under the backend OAuth endpoint.
3. User's browser navigates to the provider's auth page.
4. After authentication, the provider redirects back to the callback URL.
5. Nginx proxies this request to UAPI.
6. UAPI exchanges the authorization code for tokens.

The `X-Forwarded-Proto: https` header is critical; without it, UAPI would
generate callback URLs with `http://` which OAuth providers would reject.

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
ExecStart=/opt/uapi/bin/cli-relay -config /opt/uapi/config.yaml
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
