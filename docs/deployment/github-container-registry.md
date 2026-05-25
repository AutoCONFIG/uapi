# GitHub Container Registry Images

The repository publishes two Docker images to GitHub Container Registry:

- `ghcr.io/<github-owner>/uapi`
- `ghcr.io/<github-owner>/uapi-web`

For this repository, the image names are:

- `ghcr.io/autoconfig/uapi`
- `ghcr.io/autoconfig/uapi-web`

The workflow is `.github/workflows/docker-publish.yml`.

## Publish

Push to `main` or `master`, create a tag such as `v0.1.0`, or run the workflow manually in GitHub Actions.

The workflow publishes multi-arch images for `linux/amd64` and `linux/arm64`.

Tags include:

- `latest` for the default branch
- branch name, such as `main`
- git tags, such as `v0.1.0`
- semver tags, such as `0.1.0` and `0.1`
- commit tags, such as `sha-<short-sha>`

## Pull

For public internet pulls, make both packages public in GitHub:

`Repository -> Packages -> Package -> Package settings -> Change visibility -> Public`

Then pull directly:

```bash
docker pull ghcr.io/<github-owner>/uapi:latest
docker pull ghcr.io/<github-owner>/uapi-web:latest
```

For `AutoCONFIG/UAPI`:

```bash
docker pull ghcr.io/autoconfig/uapi:latest
docker pull ghcr.io/autoconfig/uapi-web:latest
```

If the packages are private, log in first with a GitHub token that has `read:packages`:

```bash
echo "$GITHUB_TOKEN" | docker login ghcr.io -u <github-user> --password-stdin
```

## Deploy With Compose

Copy `config.example.yaml` to `config.yaml`, edit the secrets and database DSN, then run:

```bash
docker compose pull
docker compose up -d
```

`docker-compose.yaml` uses GHCR images and exposes only loopback host ports for the frontend and Gateway. It does not publish PostgreSQL or a standalone Relay service.

- `./config.yaml:/app/config.yaml`
- `./assets:/app/assets`
- `pgdata:/var/lib/postgresql/data`

Default ports:

- Frontend static server: `127.0.0.1:3000:3000`
- Gateway/API for the host reverse proxy: `127.0.0.1:8080:8080`

For a remote Relay service, use `docker-compose.relay.yaml` separately and fill `gateway.control_url`, `gateway.relay_node_id`, `gateway.internal_secret`, and `security.encryption_key` in `config.relay.yaml`.

Use `docker-compose.dev.yaml` for local development. It still builds locally and keeps the nginx reverse proxy for convenient frontend/API testing.
