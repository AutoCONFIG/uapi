# Nginx Deployment

Use separate domains for Gateway and Relay.

```text
Client -> https://gateway.example.com
Gateway -> https://relay-1.example.com/internal/execute
Relay -> https://gateway.example.com/internal/config
Relay -> https://gateway.example.com/internal/dumps
```

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
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection $connection_upgrade;
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

```nginx
server {
    listen 80;
    server_name relay-1.example.com;
    return 301 https://$host$request_uri;
}

server {
    listen 443 ssl http2;
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

    location / {
        return 404;
    }
}
```

## HTTPS

Use HTTPS for both directions:

```text
Client -> Gateway
Gateway -> Relay
Relay -> Gateway
```

Application-level internal auth is still required; TLS and Nginx allowlists are boundary protection.

## Remote Debug Dumps

Relay debug dumps are uploaded back to Gateway when enabled:

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
