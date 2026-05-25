# Gateway / Relay Architecture

This is the current target architecture for UAPI relay scaling. It supersedes older notes that describe `/v1/*` as going directly to the relay engine.

## Baseline

UAPI uses a single Gateway as the control authority and one or more Relay nodes as execution workers.

```text
Frontend / Admin UI
  -> Gateway / Control Plane
      - users, API keys, plan-bound access policies
      - channels and upstream accounts
      - relay nodes and node-to-channel bindings
      - request auth, limits, scheduling, billing
  -> PostgreSQL

API client
  -> /v1/* or /v1beta/* on Gateway
  -> Gateway selects relay_node + channel + account
  -> Relay executes the upstream provider request
  -> Relay reports usage back to Gateway
```

Current scale target:

- Near term: single Gateway + single Relay.
- Next step: single Gateway + 2-3 Relay nodes.
- No CDN, HAProxy, GSLB, multi-Gateway, WebSocket config push, or distributed limiter in the current scope.

## Responsibilities

Gateway is the only configuration authority:

- User API key authentication.
- Plan-bound access policy checks: allowed models, hourly/weekly/monthly request limits, max concurrency.
- Channel and account management.
- Relay node management.
- Node-to-channel bindings and weights. Runtime scheduling expands each bound
  channel to the channel's enabled accounts.
- Request scheduling: choose `relay_node + channel + account`.
- Billing pre-check / pre-consume.
- Usage event ingestion and final settlement.
- Audit and admin APIs.

Relay is an execution node:

- Accept only Gateway-signed internal requests when `gateway.require_internal` is enabled.
- Do not expose management APIs.
- Do not authenticate user API keys.
- Do not own business configuration.
- Do not choose channels/accounts in the target architecture.
- Cache only the runtime config assigned by Gateway in process memory.
- Do not require PostgreSQL or Redis in remote relay mode; transient runtime state is small and does not need durable storage.
- Execute provider conversion/forwarding and stream responses.
- Parse usage and report usage events back to Gateway.

Frontend is the management surface for Gateway, not a separate source of truth.

## Request Flow

Target request flow:

```text
1. Client calls /v1/chat/completions with Bearer sk-...
2. Gateway authenticates the API key.
3. Gateway applies the active plan's access policy limits.
4. Gateway parses the endpoint format and resolves public model aliases to upstream model names.
5. Gateway selects a Relay node, bound channel, and enabled account.
6. Gateway pre-consumes estimated quota and creates a request_id.
7. Gateway forwards the request to Relay with an internal HMAC signature and selected channel/account IDs.
8. Relay verifies the Gateway signature.
9. Relay executes the selected channel/account against the upstream provider.
10. Relay returns the response stream/body to Gateway.
11. Relay reports actual usage with request_id.
12. Gateway settles billing idempotently.
```

Model listing endpoints are handled by Gateway directly:

- `GET /v1/models` with `Authorization: Bearer` returns OpenAI-compatible
  `{object:"list", data:[...]}`.
- `GET /v1/models` with `x-api-key` returns Anthropic-compatible
  `{data:[...]}` for SDKs that use Anthropic's auth header.
- `GET /v1beta/models` returns Gemini-compatible `{models:[...]}`.

The model set is the local database intersection of:

- public model names derived from `channels.models` and `channels.model_aliases`
  on enabled channels that have at least one enabled account;
- the current API key's active subscription plan policy (`token_plans` ->
  `plans.policy_id` -> `access_policies.allowed_models`) when a plan policy is
  configured.

Model-list endpoints do not call upstream providers during downstream client
requests. Admins explicitly refresh a channel's local model catalog through
`POST /api/admin/channels/models/sync?id=<channel_id>`. API-key channels may use
upstream model-list APIs during that admin sync action; OAuth channels use
provider-specific normalization based on local official-client or reference
implementations.

`channels.model_aliases` maps upstream model IDs to public model IDs, one
mapping per line in `upstream=public` form. Downstream clients see only the
public model. Gateway accepts the public name, schedules against the matching
channel/account, and Relay rewrites to the upstream model before forwarding.

## Config Sync

Config sync should stay simple for the current scale:

- Relay pulls assigned runtime config from Gateway on `gateway.config_pull_interval`,
  initially 5 seconds. Pull failures use exponential backoff up to 60 seconds;
  successful pulls reset the interval.
- Config has a version so Relay can skip unchanged config.
- Request hot paths only read local memory.
- Relay keeps channel/account pools, cooldown state, and assigned credentials in memory.
- Redis is not used for Relay runtime state at the current scale; adding it would add network latency and operational cost without improving the hot path.
- Short config delay is acceptable.
- If Gateway disables an account/node, Gateway immediately stops scheduling new requests to it even before Relay pulls the next config.
- Runtime config versioning includes disabled or soft-deleted bindings,
  accounts, and channels assigned to the node. That makes removals visible to
  Relay on the next pull instead of leaving stale in-memory routes active.

Long polling, gRPC stream, and mTLS are not current requirements. WebSocket is
currently limited to all-in-one `/v1/responses` turn handling; split
Gateway/Relay deployments continue to use HTTP/SSE relay paths for Responses
until WS relay across Gateway nodes is exposed.

Remote Relay OAuth refreshes are pushed back to Gateway through the internal
account-update endpoint after the local in-memory account is refreshed. Gateway
only accepts fresher OAuth updates for the same account/channel and keeps
credentials encrypted at rest. The push is currently best-effort: Relay keeps
the fresher credential in memory and logs a warning if Gateway is unreachable,
but durable retry for account-update is a deferred hardening item.

## Security

- Local/small deployments may use HTTP on trusted internal networks.
- Remote Relay nodes should use HTTPS when crossing public networks.
- Internal HMAC signatures are still required for production Relay nodes.
- `gateway.require_internal: true` should be used on remote Relay nodes to reject direct user calls.
- Account credentials remain owned by Gateway. First implementation may share the existing encryption key for Relay runtime decryption; node-specific encryption can be added later.

## Runtime Modes

The binary supports three modes via `server.mode`:

- `all`: Gateway, admin/user APIs, and local in-process Relay in one process. This is the default for small single-machine deployments. Gateway and the in-process Relay share the same `ConcurrencyLimiter`, so policy/token concurrency is counted once across Gateway admission and local Relay execution.
- `gateway`: Gateway/control plane only. It owns PostgreSQL and schedules remote Relay nodes.
- `relay`: execution-only node. It does not connect to PostgreSQL; it pulls assigned runtime config from Gateway and accepts only Gateway-signed requests.

Recommended Docker layout:

- Production: `docker-compose.yaml` pulls GHCR images and exposes native ports without an in-container nginx reverse proxy. The web service publishes `3000`, Gateway/API is available on host loopback `8080` for the host reverse proxy, and Relay publishes `8081`.
- Local development: `docker-compose.dev.yaml` builds locally and keeps the nginx reverse proxy for convenient frontend/API testing.
- Remote Relay node: `docker-compose.relay.yaml` runs only the Relay binary with `server.mode: relay` and publishes `8081` for Gateway-to-Relay traffic.

Before starting a Relay node, copy `config.relay.example.yaml` to
`config.relay.yaml` and fill
`gateway.control_url`, `gateway.relay_node_id`, `gateway.internal_secret`, and
the shared encryption key. `config.relay.yaml` is intentionally ignored by git.

## Access Policy Scope

Access Policy first version includes only:

- Allowed models.
- Hourly request limit.
- Weekly request limit.
- Monthly request limit.
- Max concurrency (per plan policy when the active subscription plan has a policy; otherwise per-token).

It intentionally does not limit:

- Streaming.
- Endpoint type (`chat`, `responses`, `messages`, `gemini`).

Policies are bound to plans, not API keys. A normal user should have one API key by default; admin users manage business
resources and should not create or use downstream API keys. The runtime source
of truth is the
active token subscription (`token_plans`) and the subscribed plan's
`plans.policy_id`. API keys keep only their own security fields such as
`tokens.models`, `tokens.permissions`, expiry, and IP whitelist; they do not
store or override policy IDs.

## Format Conversion

Relay follows a Bifrost-style adapter pipeline and preserves raw bodies only
when the upstream and downstream are the same standard protocol:

- Detect the downstream client format from the request path: OpenAI Chat Completions API, OpenAI Responses API, Anthropic Messages API, or Gemini generateContent API.
- For cross-protocol requests, convert into UAPI's internal request structure,
  then serialize to the selected upstream channel format.
- For same-protocol requests/responses, keep the standard body/stream intact
  where that is safer than rebuilding a narrower internal schema.
- Cross-protocol conversion follows a coarse, Bifrost-style degradation model:
  fields that have an equivalent target-protocol representation are mapped;
  fields that cannot be represented and do not affect the core prompt/tool
  flow are skipped with a warning and retained only for same-protocol raw
  preservation/ExtraParams passthrough. Only malformed input or semantics that
  would make the target request invalid are rejected with an explicit conversion
  error.
- For streaming, keep conversion incremental: normalize upstream SSE chunks to OpenAI Chat Completions-style chunks and immediately format those chunks as the downstream stream protocol.
- Same-protocol streaming preserves the upstream SSE event body. OpenAI Chat Completions API
  streams only add a final `[DONE]` marker when the upstream omitted it; Gemini,
  Anthropic, and Responses streams keep native event fields and usage intact.
- SSE normalization preserves `event:` names for converters and removes only the
  single optional space after `data:`. Do not trim the remaining payload: leading
  or trailing spaces may be valid JSON string content in multi-line SSE data.
- The `/v1/responses` WS HTTP-SSE bridge feeds a synthetic `[DONE]` to the
  OpenAI Chat Completions API-to-OpenAI Responses API converter at EOF when the upstream sent a terminal
  `finish_reason` but omitted `[DONE]`, matching the HTTP streaming path.

This avoids sending OpenAI Chat Completions API SSE to OpenAI Responses API, Gemini API, or Anthropic Messages API clients and avoids buffering full streams just to translate protocol envelopes.

## Current Implementation Status

Implemented now:

- Gateway skeleton for `/v1/*` and `/v1beta/*`.
- Relay node model and admin CRUD.
- Admin relay node management page.
- Node-to-channel bindings and weights. Runtime scheduling expands each bound
  channel to the channel's enabled accounts.
- Gateway-side user API key authentication and pre-consume for remote-node mode.
- Gateway selection of `relay_node + channel + account`.
- Gateway-to-Relay internal HMAC signatures.
- Relay internal signature verification and optional `gateway.require_internal`.
- Relay runtime config pull into in-memory channel/account pools.
- Usage event reporting to Gateway and idempotent settlement by `request_id`;
  remote pre-consume also records `token_plan_id` so final settlement/refund
  targets the same token-plan row that was pre-charged.
- Relay-only runtime mode without PostgreSQL/Redis.
- Local fallback to in-process Relay when there are no active remote Relay nodes.
- In all-in-one deployments, `/v1/responses` WebSocket turns hold a per-session
  turn lock until the upstream native WS or HTTP-SSE bridge finishes, and
  upstream WS pool capacity is released when the turn is discarded. WS request
  message size defaults to `ws.max_message_size_mb: 256`, aligned with the
  HTTP/nginx body limit. Split Gateway/Relay deployments currently use HTTP/SSE
  relay paths for `/v1/responses`; WS relay across Gateway nodes is not yet
  exposed.
- Streaming billing settles only after a valid terminal event. Successful
  terminal events include normal completion and standard partial-completion
  terminals such as OpenAI Responses `response.incomplete` with valid
  `incomplete_details`. Upstream failure terminals, conversion error chunks,
  scanner errors, client disconnects, or streams ending without a terminal event
  are recorded as failures and refund the pre-consumed estimate.

Still to implement:

- Node-specific credential encryption.
- Durable retry queue for usage events if a remote Relay loses connectivity to Gateway during settlement.
