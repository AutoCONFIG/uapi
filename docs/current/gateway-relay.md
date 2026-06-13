# Gateway / Relay

UAPI has only split Gateway and Relay runtimes on this branch.

## Roles

Gateway:

- Serves embedded Web UI.
- Serves admin/user API.
- Accepts user `/v1/*` and `/v1beta/*` requests.
- Authenticates API keys, applies policy, precharges usage, chooses Relay node/channel/account.
- Calls Relay through `POST /internal/execute`.
- Receives Relay config, usage, and account update callbacks.

Relay:

- Does not expose user `/v1` APIs.
- Does not connect to PostgreSQL.
- Pulls assigned runtime config from Gateway.
- Executes upstream requests and streams responses back to Gateway.
- Reports usage and OAuth account updates back to Gateway.

## Request Flow

```text
Client  -> Gateway: /v1/* or /v1beta/*
Gateway -> Relay:   POST /internal/execute
Relay   -> Upstream provider
Relay   -> Gateway: response stream
Relay   -> Gateway: POST /internal/usage
Relay   -> Gateway: POST /internal/dumps (optional async debug dump upload)
```

Gateway signs execution requests with HMAC. The original user URI is stored in `X-UAPI-Original-URI` and covered by the signature. Relay verifies the signature on `/internal/execute`, restores the original URI internally, then uses the existing Relay execution engine.

## Internal Paths

Relay -> Gateway:

```text
GET  /internal/config
POST /internal/usage
POST /internal/account
POST /internal/dumps
```

Gateway -> Relay:

```text
POST /internal/execute
POST /internal/reload
```

`/internal/reload` is best-effort. If a reload notification fails, Relay still refreshes by periodic config pull.

## Debug Dumps

Relay supports remote debug dumps for split deployments:

- Relay uses `debug_dump.mode: "remote"` and does not write dump files to local disk.
- Request traces are collected in memory, packed as `tar.gz`, and queued after terminal relay events.
- Internal Gateway/Relay exchanges are also captured when debug dump is enabled:
  `Gateway -> Relay /internal/execute` is stored on Gateway, while
  `Relay -> Gateway /internal/config`, `/internal/usage`, and `/internal/account`
  are uploaded back to Gateway.
- Upload is best-effort and asynchronous through `POST /internal/dumps`; a full queue or upload failure drops the dump without blocking the user request.
- Gateway must set `debug_dump.accept_remote: true` and `debug_dump.dir` to store received archives.
- Dump bodies always use redaction and truncation. User chat content is only kept as short snippets for protocol debugging.
- Existing relay dump files are preserved. The classified layout changes parent directories only; files such as `summary.json`, `request.original.json`, `request.converted.json`, `routing.json`, `events.jsonl`, `stream.events.jsonl`, `stream.upstream.sse`, `stream.normalized.sse`, `stream.downstream.sse`, `response.upstream.json`, `response.downstream.json`, `oauth.json`, `quota.json`, and ChatGPT reverse `http.jsonl` remain available.

Gateway stores a daily `index.jsonl` next to classified dump directories:

```text
debug-dumps/
  2026-06-14/
    index.jsonl
    gateway/
      api/
      downstream/
      to-relay/
      internal/
    relay/
      <relay-node-id>/
        to-upstream/
        to-gateway/
        remote/
        archives/
    oauth/
      <provider>/<operation>/
    quota/
      <provider>/
    chatgptreverse/
```

Use the index first, then open only the matching dump directory:

```bash
jq 'select(.status >= 500)' debug-dumps/2026-06-14/index.jsonl
jq 'select(.category == "to-upstream")' debug-dumps/2026-06-14/index.jsonl
jq 'select(.gateway_request_id == "gw_xxx")' debug-dumps/2026-06-14/index.jsonl
```

Use `debug_dump.enabled: false` by default and enable it only while investigating production issues.
