# UAPI Current Handoff

The current branch uses strict split binaries:

```text
cmd/uapi-gateway
cmd/uapi-relay
```

There is no runtime role selector and no all-in-one runtime.

Gateway serves Web, admin/user API, `/v1/*`, and `/v1beta/*`.

Relay serves only:

```text
GET  /healthz
POST /internal/execute
POST /internal/reload
```

Relay calls Gateway:

```text
GET  /internal/config
POST /internal/usage
POST /internal/account
POST /internal/dumps
```

Relay debug dumps use remote mode in split deployments: Relay keeps dumps in memory and uploads `tar.gz` archives asynchronously to Gateway through `/internal/dumps`; Gateway stores them under its configured `debug_dump.dir`.

Primary docs:

- `docs/current/platform-design.md`
- `docs/current/gateway-relay.md`
- `docs/deployment/nginx.md`
- `server/README.md`
