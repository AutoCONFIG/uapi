# Gateway / Relay

UAPI has only split Gateway and Relay runtimes on this branch.

## Roles

Gateway:

- Serves embedded Web UI.
- Serves admin/user API.
- Accepts user `/v1/*` and `/v1beta/*` requests.
- Authenticates API keys, applies access policy, precharges usage, and owns billing.
- Owns channel, account, Relay node, and node-channel binding management.
- Chooses Relay node, channel, and account for each request.
- Calls Relay through the original user API path, for example `POST /v1/responses`.
- Receives Relay config, usage, and account update callbacks.
- Stores PostgreSQL state and central debug dumps.

Relay:

- Serves signed Gateway execution requests on `/v1/*` and `/v1beta/*`.
- Does not connect to PostgreSQL.
- Pulls assigned runtime config from Gateway.
- Executes upstream requests and streams responses back to Gateway.
- Owns upstream authentication, request conversion, passthrough, response conversion, streaming normalization, and upstream error classification.
- May perform execution-time account/channel failover, then reports the final result back to Gateway.
- Reports usage and OAuth account updates back to Gateway.

## Request Flow

```text
Client  -> Gateway: /v1/* or /v1beta/*
Gateway -> Relay:   same /v1/* or /v1beta/* path
Relay   -> Upstream provider
Relay   -> Gateway: response stream
Relay   -> Gateway: POST /internal/usage
Relay   -> Gateway: POST /internal/dumps (optional async debug dump upload)
```

Gateway signs execution requests with HMAC. Relay verifies the signature on the original business path, then uses that path directly for request type and protocol detection.

## Communication Model

Data plane:

```text
Client -> Gateway: /v1/* or /v1beta/*
Gateway -> Relay: same /v1/* or /v1beta/* path, HMAC signed
Relay -> Upstream: provider protocol
```

Control plane:

```text
Relay -> Gateway: /internal/config, /internal/usage, /internal/account, /internal/dumps
Gateway -> Relay: /internal/reload
```

Public network links should use HTTPS. Nginx should restrict Relay business paths and internal paths to Gateway IPs. Nginx can advertise HTTP/3 and HTTP/2 so external clients try H3 first and fall back automatically. Gateway -> Relay is initiated by the Gateway application, so application-level H3-first/H2-fallback requires a future dedicated Relay client transport.

## HTTP/SSE and WebSocket

Normal model APIs use the original HTTP path all the way through Gateway and Relay:

```text
Client  -> Gateway: POST /v1/responses
Gateway -> Relay:   POST /v1/responses
Relay   -> Upstream: HTTP/SSE or provider HTTP protocol
```

WebSocket is not the default Gateway -> Relay transport. In the current split runtime, normal Gateway forwarding is HTTP/SSE. Provider-native WebSocket/realtime support should be treated as a separate endpoint capability, and it is only appropriate when the downstream client also initiates a WebSocket upgrade:

```text
Client  -> Gateway: GET /v1/... with Upgrade: websocket
Gateway -> Relay:   same WebSocket path after auth, policy, billing, routing, and HMAC signing
Relay   -> Upstream: provider WebSocket protocol when that provider endpoint requires it
```

Relay-side WebSocket code must not be exposed directly to users in split deployments; Gateway remains the only public API entry. If an upstream supports WebSocket as an optional alternative to HTTP/SSE, ordinary `/v1/responses` and `/v1/chat/completions` should keep using HTTP/SSE. HTTP-to-WebSocket bridging is avoided unless a provider has no HTTP-compatible path, because it weakens status/header semantics, complicates cancellation and dump correlation, and does not provide a general performance win for single request/response calls.

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
POST /v1/*
POST /v1beta/*
POST /internal/reload
```

The split runtime does not use an internal execution wrapper. Do not route model execution through an internal control path; Gateway forwards the original business request path and Relay verifies that same path in the HMAC signature. Control paths are only for config, usage, account updates, dump upload, and reload notifications.

`/internal/reload` is best-effort. If a reload notification fails, Relay still refreshes by periodic config pull.

## Debug Dumps

Relay supports remote debug dumps for split deployments:

- Relay uses `debug_dump.mode: "remote"` and does not write dump files to local disk.
- Request traces are collected in memory, packed as `tar.gz`, and queued after terminal relay events.
- Internal Gateway/Relay exchanges are also captured when debug dump is enabled:
  `Gateway -> Relay` execution requests are stored on Gateway with their original API path, while
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
