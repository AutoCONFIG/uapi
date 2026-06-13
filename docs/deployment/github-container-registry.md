# GitHub Container Registry

CI publishes two images:

```text
ghcr.io/<owner>/<repo>/uapi-gateway
ghcr.io/<owner>/<repo>/uapi-relay
```

`uapi-gateway` contains the Gateway API and embedded Web UI. `uapi-relay` contains only the Relay runtime.

Login for private packages:

```bash
echo <github_token> | docker login ghcr.io -u <github_user> --password-stdin
```

Start Gateway:

```bash
docker compose pull
docker compose up -d
```

Start Relay:

```bash
docker compose -f docker-compose.relay.yaml pull
docker compose -f docker-compose.relay.yaml up -d
```
