# UAPI Nginx Reverse Proxy Configuration

UAPI is designed to run behind an nginx reverse proxy with HTTPS. In the
production Docker layout, the web container publishes the exported Next.js
frontend on `127.0.0.1:3000`, and nginx proxies backend traffic to the Go API
server on `127.0.0.1:8080`.

## Basic Configuration

```nginx
map $http_upgrade $connection_upgrade {
    default upgrade;
    '' close;
}

# Static asset cache zone. Place this in the http block, outside any server block.
proxy_cache_path /var/cache/nginx/uapi_static
    levels=1:2
    keys_zone=uapi_static:10m
    max_size=1g
    inactive=30d
    use_temp_path=off;

# Rate limit zone for auth endpoints: 10 requests per minute per IP.
# Place this in the http block, outside any server block.
limit_req_zone $binary_remote_addr zone=auth_limit:10m rate=10r/m;

server {
    listen 80;
    listen [::]:80;
    server_name relay.example.com;

    location /.well-known/acme-challenge/ {
        root /var/www/certbot;
    }

    location / {
        return 301 https://$host$request_uri;
    }
}

server {
    listen 443 ssl http2;
    listen [::]:443 ssl http2;
    server_name relay.example.com;

    ssl_certificate     /etc/ssl/certs/relay.example.com.pem;
    ssl_certificate_key /etc/ssl/private/relay.example.com.key;

    client_max_body_size 256m;

    # Security headers
    add_header Strict-Transport-Security "max-age=15552000; includeSubDomains; preload" always;
    add_header X-Content-Type-Options "nosniff" always;
    add_header X-Frame-Options "SAMEORIGIN" always;
    add_header Referrer-Policy "no-referrer" always;

    # Backend health check
    location = /healthz {
        proxy_connect_timeout 10s;
        proxy_read_timeout 10s;
        proxy_send_timeout 10s;
        proxy_next_upstream off;
        proxy_buffering off;
        proxy_cache off;

        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-Host $http_host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header X-Forwarded-Port $server_port;
        proxy_set_header Connection "";

        proxy_pass http://127.0.0.1:8080;
    }

    # Auth endpoints: strict rate limiting to prevent brute force.
    location ~ ^/api/(admin|user)/(login|register|refresh|setup)(?:/|$) {
        limit_req zone=auth_limit burst=5 nodelay;
        limit_req_status 429;

        proxy_connect_timeout 30s;
        proxy_read_timeout 30s;
        proxy_send_timeout 30s;
        proxy_next_upstream off;
        proxy_buffering off;
        proxy_cache off;

        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-Host $http_host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header X-Forwarded-Port $server_port;
        proxy_set_header Connection "";

        proxy_pass http://127.0.0.1:8080;
    }

    # User/Admin/Public API: matches /api and /api/...
    location ~ ^/api(?:/|$) {
        proxy_connect_timeout 30s;
        proxy_read_timeout 300s;
        proxy_send_timeout 300s;
        proxy_next_upstream off;
        proxy_buffering off;
        proxy_cache off;

        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-Host $http_host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header X-Forwarded-Port $server_port;
        proxy_set_header Connection "";

        proxy_pass http://127.0.0.1:8080;
    }

    # Remote Relay control plane
    location ~ ^/internal/relay(?:/|$) {
        proxy_connect_timeout 30s;
        proxy_read_timeout 300s;
        proxy_send_timeout 300s;
        proxy_next_upstream off;
        proxy_buffering off;
        proxy_cache off;

        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-Host $http_host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header X-Forwarded-Port $server_port;
        proxy_set_header Connection "";

        proxy_pass http://127.0.0.1:8080;
    }

    # WebSocket / realtime paths
    location ~ ^/ws(?:/|$) {
        proxy_connect_timeout 60s;
        proxy_read_timeout 3600s;
        proxy_send_timeout 3600s;
        proxy_next_upstream off;

        proxy_http_version 1.1;
        proxy_buffering off;
        proxy_cache off;

        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-Host $http_host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header X-Forwarded-Port $server_port;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection $connection_upgrade;

        proxy_pass http://127.0.0.1:8080;
    }

    # Responses / Realtime API: supports HTTP/SSE and WebSocket upgrade
    location ~ ^/v1/(responses|realtime)(?:/|$) {
        proxy_connect_timeout 60s;
        proxy_read_timeout 3600s;
        proxy_send_timeout 3600s;
        proxy_next_upstream off;

        proxy_http_version 1.1;
        proxy_buffering off;
        proxy_request_buffering off;
        proxy_cache off;
        chunked_transfer_encoding on;

        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-Host $http_host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header X-Forwarded-Port $server_port;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection $connection_upgrade;

        add_header X-Accel-Buffering "no" always;

        proxy_pass http://127.0.0.1:8080;
    }

    # Model API hot paths: /v1 and /v1beta
    location ~ ^/(v1|v1beta)(?:/|$) {
        proxy_connect_timeout 60s;
        proxy_read_timeout 3600s;
        proxy_send_timeout 3600s;
        proxy_next_upstream off;

        proxy_http_version 1.1;
        proxy_buffering off;
        proxy_request_buffering off;
        proxy_cache off;
        chunked_transfer_encoding on;

        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-Host $http_host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header X-Forwarded-Port $server_port;
        proxy_set_header Connection "";

        add_header X-Accel-Buffering "no" always;

        proxy_pass http://127.0.0.1:8080;
    }

    # Next.js static assets
    location /_next/static/ {
        proxy_cache uapi_static;
        proxy_cache_valid 200 365d;
        expires 1y;

        add_header Cache-Control "public, max-age=31536000, immutable" always;
        add_header Strict-Transport-Security "max-age=15552000; includeSubDomains; preload" always;
        add_header X-Content-Type-Options "nosniff" always;

        proxy_pass http://127.0.0.1:3000;
        access_log off;
    }

    # Other frontend static assets
    location ~* \.(ico|png|webp|jpg|jpeg|gif|svg|js|mjs|css|woff2?|ttf|wasm|map)$ {
        expires 30d;
        add_header Cache-Control "public, max-age=2592000, immutable" always;
        add_header Strict-Transport-Security "max-age=15552000; includeSubDomains; preload" always;
        add_header X-Content-Type-Options "nosniff" always;

        proxy_pass http://127.0.0.1:3000;
        access_log off;
    }

    # Frontend pages
    location / {
        proxy_connect_timeout 30s;
        proxy_read_timeout 300s;
        proxy_next_upstream off;

        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-Host $http_host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header X-Forwarded-Port $server_port;
        proxy_set_header Connection "";

        proxy_pass http://127.0.0.1:3000;
    }
}
```

If your nginx build supports HTTP/3/QUIC, add these lines to the HTTPS server:

```nginx
listen 443 quic;
listen [::]:443 quic;
add_header Alt-Svc 'h3=":443"; ma=86400' always;
```

## Integrated Example for an Existing Optimized Nginx

If nginx already has shared settings such as `static_cache`, `detailed`,
`error_502`, `$connection_upgrade`, crawler blocking, HTTPS/QUIC, and common
proxy headers, add only the UAPI-specific pieces below.

Add these in the `http` block with the other upstreams and rate-limit zones:

```nginx
# UAPI upstreams. These match docker-compose.yaml loopback ports.
upstream backend_uapi     { server 127.0.0.1:8080; keepalive 256; zone backend_uapi 64k; }
upstream backend_uapi_web { server 127.0.0.1:3000; keepalive 128; zone backend_uapi_web 64k; }

# UAPI auth endpoint rate limit.
limit_req_zone $binary_remote_addr zone=uapi_auth_limit:10m rate=10r/m;
```

Then add this server block with the other domain server blocks:

```nginx
# uapi.hyhy.fun (UAPI Gateway + Web Console)
server {
    listen 443 ssl;
    listen [::]:443 ssl;
    listen 443 quic;
    listen [::]:443 quic;
    server_name uapi.hyhy.fun;

    access_log /var/log/nginx/uapi.access.log detailed buffer=64k flush=5s;
    error_log /var/log/nginx/uapi.error.log warn;

    ssl_certificate /var/www/.cert/fullchain.pem;
    ssl_certificate_key /var/www/.cert/key.pem;

    error_page 497 = @error;
    error_page 502 504 = @error_502;

    client_max_body_size 256m;
    client_body_buffer_size 32m;

    if ($crawler_block) { return 403; }

    # Security headers
    add_header Accept-Ranges none always;
    add_header Strict-Transport-Security "max-age=15552000; includeSubDomains; preload" always;
    add_header Referrer-Policy "no-referrer" always;
    add_header X-Content-Type-Options "nosniff" always;
    add_header X-Frame-Options "SAMEORIGIN" always;
    add_header X-XSS-Protection "1; mode=block" always;

    # Common proxy headers for UAPI.
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-Host $http_host;
    proxy_set_header X-Real-IP $remote_addr;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_set_header X-Forwarded-Port $server_port;
    proxy_set_header Early-Data $ssl_early_data;

    # UAPI backend health check
    location = /healthz {
        proxy_connect_timeout 5s;
        proxy_read_timeout 10s;
        proxy_send_timeout 10s;
        proxy_next_upstream off;
        proxy_buffering off;
        proxy_cache off;
        proxy_set_header Connection "";
        proxy_pass http://backend_uapi;
    }

    # UAPI auth endpoints (login/register/refresh/setup)
    location ~ ^/api/(admin|user)/(login|register|refresh|setup)(?:/|$) {
        limit_req zone=uapi_auth_limit burst=5 nodelay;
        limit_req_status 429;
        proxy_connect_timeout 30s;
        proxy_read_timeout 60s;
        proxy_send_timeout 60s;
        proxy_next_upstream off;
        proxy_buffering off;
        proxy_cache off;
        proxy_no_cache 1;
        proxy_cache_bypass 1;
        proxy_set_header Connection "";
        add_header Cache-Control "no-store, no-cache, must-revalidate" always;
        proxy_pass http://backend_uapi;
    }

    # UAPI console API (admin/user/public)
    location ~ ^/api(?:/|$) {
        proxy_connect_timeout 30s;
        proxy_read_timeout 300s;
        proxy_send_timeout 300s;
        proxy_next_upstream off;
        proxy_buffering off;
        proxy_cache off;
        proxy_no_cache 1;
        proxy_cache_bypass 1;
        proxy_set_header Connection "";
        add_header Cache-Control "no-store, no-cache, must-revalidate" always;
        proxy_pass http://backend_uapi;
    }

    # UAPI Gateway <-> remote Relay control plane
    location ~ ^/internal/relay(?:/|$) {
        proxy_connect_timeout 30s;
        proxy_read_timeout 300s;
        proxy_send_timeout 300s;
        proxy_next_upstream off;
        proxy_buffering off;
        proxy_cache off;
        proxy_no_cache 1;
        proxy_cache_bypass 1;
        proxy_set_header Connection "";
        add_header Cache-Control "no-store" always;
        proxy_pass http://backend_uapi;
    }

    # UAPI Responses/Reatime endpoint (SSE + optional WebSocket upgrade)
    location ~ ^/v1/(responses|realtime)(?:/|$) {
        proxy_connect_timeout 60s;
        proxy_read_timeout 3600s;
        proxy_send_timeout 300s;
        proxy_next_upstream off;
        proxy_http_version 1.1;
        proxy_socket_keepalive on;
        proxy_buffering off;
        proxy_request_buffering off;
        proxy_cache off;
        proxy_no_cache 1;
        proxy_cache_bypass 1;
        chunked_transfer_encoding on;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection $connection_upgrade;
        add_header Cache-Control "no-store" always;
        add_header X-Accel-Buffering "no" always;
        add_header X-Server-Response-Time $request_time always;
        proxy_pass http://backend_uapi;
    }

    # UAPI model relay hot paths: OpenAI-compatible /v1 and Gemini-compatible /v1beta
    location ~ ^/(v1|v1beta)(?:/|$) {
        proxy_connect_timeout 60s;
        proxy_read_timeout 3600s;
        proxy_send_timeout 300s;
        proxy_next_upstream off;
        proxy_http_version 1.1;
        proxy_socket_keepalive on;
        proxy_buffering off;
        proxy_request_buffering off;
        proxy_cache off;
        proxy_no_cache 1;
        proxy_cache_bypass 1;
        chunked_transfer_encoding on;
        proxy_set_header Connection "";
        add_header Cache-Control "no-store" always;
        add_header X-Accel-Buffering "no" always;
        add_header X-Server-Response-Time $request_time always;
        proxy_pass http://backend_uapi;
    }

    # Next.js build assets (content-hashed)
    location /_next/static/ {
        expires 1y;
        add_header Cache-Control "public, max-age=31536000, immutable" always;
        add_header Strict-Transport-Security "max-age=15552000; includeSubDomains; preload" always;
        add_header X-Content-Type-Options "nosniff" always;
        add_header X-Frame-Options "SAMEORIGIN" always;
        add_header Referrer-Policy "no-referrer" always;
        proxy_cache static_cache;
        proxy_cache_valid 200 365d;
        proxy_pass http://backend_uapi_web;
        access_log off;
    }

    # Frontend static assets
    location ~* \.(ico|png|webp|jpg|jpeg|gif|svg|js|mjs|css|woff2?|ttf|wasm|map)$ {
        expires 30d;
        add_header Cache-Control "public, max-age=2592000, immutable" always;
        add_header Strict-Transport-Security "max-age=15552000; includeSubDomains; preload" always;
        add_header X-Content-Type-Options "nosniff" always;
        add_header X-Frame-Options "SAMEORIGIN" always;
        add_header Referrer-Policy "no-referrer" always;
        proxy_cache static_cache;
        proxy_cache_valid 200 30d;
        proxy_pass http://backend_uapi_web;
        access_log off;
    }

    # UAPI web console
    location / {
        access_log /var/log/nginx/crawler.log crawler if=$is_crawler;
        proxy_connect_timeout 30s;
        proxy_read_timeout 300s;
        proxy_send_timeout 300s;
        proxy_next_upstream off;
        proxy_pass http://backend_uapi_web;
    }

    location @error { return 308 https://$http_host$request_uri; }
    location @error_502 { access_log /var/log/nginx/uapi.error_502.log error_502 buffer=16k flush=1s; return 502; }
    location = /robots.txt { default_type text/plain; return 200 $robots_txt; }
}
```

## Key Headers

| Header | Purpose |
|--------|---------|
| `X-Forwarded-Proto` | UAPI uses this to detect HTTPS and generate correct OAuth callback URLs |
| `X-Real-IP` | For logging real client IPs |
| `Host` | Required for generating correct redirect URLs |

## Rate Limiting

Auth endpoints (`/api/*/login`, `/api/*/register`, `/api/*/refresh`, `/api/*/setup`)
are rate-limited to 10 requests per minute per IP with a burst allowance of 5.
This prevents brute-force attacks against login and registration. Adjust the
`limit_req_zone` rate and burst values to match your deployment's expected
legitimate traffic.

## Request Body Size

Keep nginx `client_max_body_size` aligned with `server.max_body_size_mb` in the
UAPI config. The default example is `256m`, leaving room for base64 expansion
and JSON wrapping so Anthropic/Gemini/Responses-format requests with PDFs or
long document fields reach UAPI instead of being rejected by nginx with an HTML
`413 Request Entity Too Large` response.

The repository-local `config.yaml` is ignored by git and is the file mounted by
the default Docker Compose deployment. Keep its `server.max_body_size_mb` aligned
with `web/nginx.conf` when testing large uploads locally.

## Stream Timeouts

UAPI uses `server.stream_idle_timeout_seconds` to close upstream streams that
stop producing chunks. The default example is 300 seconds. Keep nginx
`proxy_read_timeout` for `/v1`, `/v1beta`, `/ws`, and `/v1/responses` greater
than that value. Do not replace it with a short nginx read timeout: long-running
model streams should stay open as long as chunks continue arriving.

## OAuth Completion Flow

OAuth channels currently use provider-specific manual callback redirect
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

## CORS

The Go backend validates the `Origin` header on admin API requests against
`server.allowed_origins` in the UAPI config. If the list is empty, all origins
are allowed (suitable for same-origin deployments where nginx serves both
frontend and API). For cross-origin deployments, list the allowed origins:

```yaml
server:
  allowed_origins:
    - https://admin.example.com
```

## Security Recommendations

- Enable HTTPS with a valid certificate. Let's Encrypt is a good default.
- Use `proxy_set_header X-Forwarded-Proto $scheme` to ensure correct URL generation.
- Restrict admin access with an IP allowlist or another edge control if required by
  the deployment environment.
- Keep the auth rate limit enabled. Adjust `rate` and `burst` as needed.
- Add `Content-Security-Policy` header if serving the frontend from a different
  origin than the API.

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
