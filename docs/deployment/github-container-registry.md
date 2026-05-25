# GitHub Container Registry Images

The repository publishes two Docker images to GitHub Container Registry:

- `ghcr.io/<github-owner>/uapi`
- `ghcr.io/<github-owner>/uapi-web`

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

If the packages are private, log in first with a GitHub token that has `read:packages`:

```bash
echo "$GITHUB_TOKEN" | docker login ghcr.io -u <github-user> --password-stdin
```

## Deploy With Compose

Copy `config.example.yaml` to `config.yaml`, edit the secrets and database DSN, then run:

```bash
export GHCR_OWNER=<github-owner-in-lowercase>
export UAPI_TAG=latest
docker compose -f docker-compose.ghcr.yaml pull
docker compose -f docker-compose.ghcr.yaml up -d
```

`docker-compose.ghcr.yaml` keeps local runtime files mounted:

- `./config.yaml:/app/config.yaml`
- `./assets:/app/assets`
- `pgdata:/var/lib/postgresql/data`
