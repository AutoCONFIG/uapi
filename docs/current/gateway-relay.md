# Gateway / Relay Architecture

This is the current target architecture for UAPI relay scaling. It supersedes older notes that describe `/v1/*` as going directly to the relay engine.

## Baseline

UAPI uses a single Gateway as the control authority and one or more Relay nodes as execution workers.

```text
Frontend / Admin UI
  -> Gateway / Control Plane
      - users, API keys, access policies
      - channels and upstream accounts
      - relay nodes and account-to-node bindings
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
- Access policy checks: allowed models, hourly/weekly/monthly request limits, max concurrency.
- Channel and account management.
- Relay node management.
- Account-to-node bindings and weights.
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
3. Gateway applies access policy limits.
4. Gateway parses the model and endpoint format.
5. Gateway selects a channel, account, and Relay node using enabled account-node bindings.
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

The model set is the intersection of:

- models configured on enabled channels that have at least one enabled account;
- models discovered from enabled regular API-key channels by calling native
  upstream model-list endpoints and falling back to OpenAI-compatible
  `/v1/models`;
- the current API key's `tokens.models`, or its bound Access Policy
  `allowed_models` when a policy is present.

This is intentional for Code channels because CodeX/Gemini Code/Claude Code do
not expose a stable public model-list endpoint equivalent to regular provider
APIs. Their available model set is represented by UAPI channel configuration,
which is initially pre-filled from the local official client source presets.
Regular OpenAI/Gemini/Anthropic API-key channels are discovered from upstream
with a short cache.

## Config Sync

Config sync should stay simple for the current scale:

- Relay pulls assigned runtime config from Gateway on a fixed interval, initially 5 seconds.
- Config has a version so Relay can skip unchanged config.
- Request hot paths only read local memory.
- Relay keeps channel/account pools, cooldown state, and assigned credentials in memory.
- Redis is not used for Relay runtime state at the current scale; adding it would add network latency and operational cost without improving the hot path.
- Short config delay is acceptable.
- If Gateway disables an account/node, Gateway immediately stops scheduling new requests to it even before Relay pulls the next config.

Long polling, WebSocket, gRPC stream, and mTLS are not current requirements.

## Security

- Local/small deployments may use HTTP on trusted internal networks.
- Remote Relay nodes should use HTTPS when crossing public networks.
- Internal HMAC signatures are still required for production Relay nodes.
- `gateway.require_internal: true` should be used on remote Relay nodes to reject direct user calls.
- Account credentials remain owned by Gateway. First implementation may share the existing encryption key for Relay runtime decryption; node-specific encryption can be added later.

## Runtime Modes

The binary supports three modes via `server.mode`:

- `all`: Gateway, admin/user APIs, and local in-process Relay in one process. This is the default for small single-machine deployments.
- `gateway`: Gateway/control plane only. It owns PostgreSQL and schedules remote Relay nodes.
- `relay`: execution-only node. It does not connect to PostgreSQL; it pulls assigned runtime config from Gateway and accepts only Gateway-signed requests.

Recommended Docker layout:

- Default single machine: `docker-compose.yaml` runs PostgreSQL, Gateway/control API, local in-process Relay, and the static frontend. Only the web container publishes port `80`; PostgreSQL, Gateway `8080`, and Relay internals stay inside Docker.
- Same-machine split test: `docker-compose.gateway.yaml` plus `docker-compose.relay.yaml` share the internal `uapi-net` network. Relay publishes no host port; Gateway reaches it as `http://relay:8081`.
- Remote Relay machine: `docker-compose.relay.remote.yaml` runs only the Relay binary with `server.mode: relay` and publishes `8081` for Gateway-to-Relay traffic.

## Access Policy Scope

Access Policy first version includes only:

- Allowed models.
- Hourly request limit.
- Weekly request limit.
- Monthly request limit.
- Max concurrency.

It intentionally does not limit:

- Streaming.
- Endpoint type (`chat`, `responses`, `messages`, `gemini`).

Policies are bound to API keys. If a token has no policy, legacy `tokens.models` behavior remains compatible.

## Current Implementation Status

Implemented now:

- Gateway skeleton for `/v1/*` and `/v1beta/*`.
- Relay node model and admin CRUD.
- Admin relay node management page.
- Account-to-node bindings and weights.
- Gateway-side user API key authentication and pre-consume for remote-node mode.
- Gateway selection of `relay_node + channel + account`.
- Gateway-to-Relay internal HMAC signatures.
- Relay internal signature verification and optional `gateway.require_internal`.
- Relay runtime config pull into in-memory channel/account pools.
- Usage event reporting to Gateway and idempotent settlement by `request_id`.
- Relay-only runtime mode without PostgreSQL/Redis.
- Local fallback to in-process Relay when there are no active remote Relay nodes.

Still to implement:

- Node-specific credential encryption.
- Durable retry queue for usage events if a remote Relay loses connectivity to Gateway during settlement.
