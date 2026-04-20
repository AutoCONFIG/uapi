# Nginx Reverse Proxy Configuration

CLI Relay is designed to run behind an nginx reverse proxy with HTTPS.
This is required for OAuth browser flows to work correctly when the server
is accessed remotely.

## Basic Configuration

```nginx
server {
    listen 443 ssl http2;
    server_name relay.example.com;

    ssl_certificate     /etc/ssl/certs/relay.example.com.pem;
    ssl_certificate_key /etc/ssl/private/relay.example.com.key;

    # Proxy all requests to CLI Relay
    location / {
        proxy_pass http://127.0.0.1:9876;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;

        # WebSocket support (for future use)
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
    }
}
```

## Key Headers

| Header | Purpose |
|--------|---------|
| `X-Forwarded-Proto` | CLI Relay uses this to detect HTTPS and generate correct OAuth callback URLs |
| `X-Real-IP` | For logging real client IPs |
| `Host` | Required for generating correct redirect URLs |

## OAuth Callback Flow

When using browser-based OAuth login from a remote client:

1. User initiates login via Web UI or API
2. CLI Relay generates an auth URL with callback pointing to `https://relay.example.com/api/v1/oauth/callback`
3. User's browser navigates to the provider's auth page (e.g., OpenAI, Google)
4. After authentication, the provider redirects back to the callback URL
5. Nginx proxies this request to CLI Relay
6. CLI Relay exchanges the authorization code for tokens

The `X-Forwarded-Proto: https` header is critical — without it, CLI Relay
would generate callback URLs with `http://` which OAuth providers would reject.

## Security Recommendations

- Enable HTTPS with a valid certificate (Let's Encrypt recommended)
- Restrict access with HTTP Basic Auth or IP whitelist if desired:
  ```nginx
  # Optional: restrict by IP
  allow 192.168.1.0/24;
  deny all;
  ```
- Use `proxy_set_header X-Forwarded-Proto $scheme` to ensure correct URL generation

## Systemd Service

Create `/etc/systemd/system/cli-relay.service`:

```ini
[Unit]
Description=CLI Relay
After=network.target

[Service]
Type=simple
User=cli-relay
WorkingDirectory=/opt/cli-relay
ExecStart=/opt/cli-relay/bin/cli-relay -config /opt/cli-relay/config.yaml
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
```

Then:

```bash
sudo systemctl daemon-reload
sudo systemctl enable cli-relay
sudo systemctl start cli-relay
```
