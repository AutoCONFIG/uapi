# Nginx Deployment

Use separate domains for Gateway and Relay.

```text
Client  -> https://gateway.example.com
Gateway -> https://relay-1.example.com/v1/... or /v1beta/...
Relay   -> https://gateway.example.com/internal/config
Relay   -> https://gateway.example.com/internal/dumps
```

Recommended boundary:

- Public client-to-Nginx links use HTTPS. Enable HTTP/3 and HTTP/2 on Nginx if available; clients can try H3 through Alt-Svc and automatically fall back to H2/H1.
- Nginx -> local app can stay HTTP/1.1.
- Gateway -> Relay data-plane requests are HMAC signed by the application.
- Relay -> Gateway control-plane requests use `X-UAPI-Internal-Secret`.
- Nginx allowlists should restrict Relay paths to Gateway IPs and Gateway `/internal/*` to Relay IPs.
- WebSocket upgrade headers should only be passed on explicitly supported provider-native realtime endpoints; normal `/v1/*` data-plane calls remain HTTP/SSE.

Current implementation note: Nginx can expose HTTP/3/HTTP/2 at the edge, but Gateway -> Relay is initiated by the Gateway application HTTP client. True application-level H3-first/H2-fallback for Gateway -> Relay requires a dedicated Relay client transport. Until then, keep Relay Nginx H3/H2 enabled for compatible clients and rely on the existing HTTPS + HMAC + allowlist security boundary. WebSocket is not a replacement transport for normal Gateway -> Relay forwarding, and split WS forwarding needs explicit Gateway support before being enabled.

## Gateway

`uapi-gateway` listens on `127.0.0.1:1240` in the compose examples and serves both Web and API.

```nginx
map $http_upgrade $connection_upgrade {
    default upgrade;
    '' close;
}

server {
    listen 80;
    server_name gateway.example.com;
    return 301 https://$host$request_uri;
}

server {
    listen 443 ssl http2;
    # If your Nginx build supports QUIC/HTTP/3:
    # listen 443 quic reuseport;
    # add_header Alt-Svc 'h3=":443"; ma=86400' always;
    server_name gateway.example.com;

    ssl_certificate /etc/letsencrypt/live/gateway.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/gateway.example.com/privkey.pem;

    client_max_body_size 256m;
    proxy_read_timeout 360s;
    proxy_send_timeout 360s;
    proxy_connect_timeout 30s;

    location /internal/ {
        allow <RELAY_SERVER_PUBLIC_IP>;
        allow <RELAY_SERVER_2_PUBLIC_IP>;
        deny all;

        proxy_pass http://127.0.0.1:1240;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }

    location = /v1/responses {
        proxy_pass http://127.0.0.1:1240;
        proxy_http_version 1.1;
        proxy_buffering off;
        proxy_request_buffering off;
        proxy_cache off;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection $connection_upgrade;
    }

    location /v1/ {
        proxy_pass http://127.0.0.1:1240;
        proxy_http_version 1.1;
        proxy_buffering off;
        proxy_request_buffering off;
        proxy_cache off;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }

    location /v1beta/ {
        proxy_pass http://127.0.0.1:1240;
        proxy_http_version 1.1;
        proxy_buffering off;
        proxy_request_buffering off;
        proxy_cache off;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }

    location / {
        proxy_pass http://127.0.0.1:1240;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}
```

## Relay

`uapi-relay` listens on `127.0.0.1:8081` in the compose examples. Relay should only be reachable by Gateway.
If you copy the Relay server block into a separate Nginx config and enable a provider-native WebSocket/realtime endpoint, define the `map $http_upgrade $connection_upgrade` block in the `http` context as shown in the Gateway example and scope those headers to the exact realtime path.

```nginx
server {
    listen 80;
    server_name relay-1.example.com;
    return 301 https://$host$request_uri;
}

server {
    listen 443 ssl http2;
    # If your Nginx build supports QUIC/HTTP/3:
    # listen 443 quic reuseport;
    # add_header Alt-Svc 'h3=":443"; ma=86400' always;
    server_name relay-1.example.com;

    ssl_certificate /etc/letsencrypt/live/relay-1.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/relay-1.example.com/privkey.pem;

    client_max_body_size 256m;
    proxy_read_timeout 360s;
    proxy_send_timeout 360s;
    proxy_connect_timeout 30s;

    location = /healthz {
        proxy_pass http://127.0.0.1:8081;
    }

    location /internal/ {
        allow <GATEWAY_SERVER_PUBLIC_IP>;
        deny all;

        proxy_pass http://127.0.0.1:8081;
        proxy_http_version 1.1;
        proxy_buffering off;
        proxy_request_buffering off;
        proxy_cache off;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }

    location = /v1/responses {
        allow <GATEWAY_SERVER_PUBLIC_IP>;
        deny all;

        proxy_pass http://127.0.0.1:8081;
        proxy_http_version 1.1;
        proxy_buffering off;
        proxy_request_buffering off;
        proxy_cache off;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection $connection_upgrade;
    }

    location /v1/ {
        allow <GATEWAY_SERVER_PUBLIC_IP>;
        deny all;

        proxy_pass http://127.0.0.1:8081;
        proxy_http_version 1.1;
        proxy_buffering off;
        proxy_request_buffering off;
        proxy_cache off;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }

    location /v1beta/ {
        allow <GATEWAY_SERVER_PUBLIC_IP>;
        deny all;

        proxy_pass http://127.0.0.1:8081;
        proxy_http_version 1.1;
        proxy_buffering off;
        proxy_request_buffering off;
        proxy_cache off;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }

    location / {
        return 404;
    }
}
```

## Transport

Use HTTPS for both directions:

```text
Client -> Gateway
Gateway -> Relay
Relay -> Gateway
```

Application-level internal auth is still required; TLS and Nginx allowlists are boundary protection. WebSocket is not used for normal `/v1/*` data-plane requests because HTTP/SSE preserves status codes, headers, streaming, and dump visibility.

WebSocket handling is provider-specific and should remain disabled for split deployments until Gateway-mediated WS forwarding is implemented:

- Downstream clients must explicitly use a WebSocket/realtime path and send `Upgrade: websocket`.
- Gateway still performs API-key auth, policy checks, billing setup, Relay selection, and HMAC signing before proxying to Relay.
- Relay only connects to an upstream WebSocket when the selected provider endpoint requires or benefits from that protocol.
- Relay WS entrypoints must not be exposed directly to public clients.
- If the provider offers equivalent HTTP/SSE for a normal request, use HTTP/SSE.

## Remote Debug Dumps

Relay debug dumps are uploaded back to Gateway when enabled. Production `uapi-relay` deployments should either use `mode: "remote"` or keep debug dumps disabled; Relay local dump mode is only for isolated developer diagnostics where the Relay filesystem is intentionally inspected directly.

```yaml
# Gateway
debug_dump:
  enabled: false
  mode: "local"
  dir: "/app/debug-dumps"
  accept_remote: true

# Relay
debug_dump:
  enabled: false
  mode: "remote"
  queue_max_items: 1000
  batch_max_bytes_mb: 8
  upload_timeout: "10s"
```

The Gateway compose examples mount `./debug-dumps:/app/debug-dumps`. Relay examples do not mount a dump directory, so remote mode does not leave local dump files on Relay servers. Gateway stores uploaded Relay archives under `debug-dumps/<date>/relay/<node>/archives/` and extracts a readable copy under `debug-dumps/<date>/relay/<node>/remote/`.

When enabled, dumps include internal Gateway/Relay request-response metadata. Gateway also writes `debug-dumps/<date>/index.jsonl` so dumps can be filtered by status, path, category, Gateway request ID, Relay request ID, node, channel, account, or model before opening individual files. Bodies are redacted and truncated for protocol debugging; full user chat content is not stored in internal exchange dumps.
