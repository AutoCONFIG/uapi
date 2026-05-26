# UAPI Handoff

This is the first file a new coding session should read. It captures the current
working state so the next agent can continue without extra user briefing.

## Product

- Public name: **UAPI**
- Meaning: Unified API / Your API
- Positioning: Your Unified AI API Gateway

## Repository State

- Go module: `github.com/AutoCONFIG/uapi`
- Binary entry point: `cmd/uapi/main.go` (startup log: `"uapi ready"`)
- Active frontend branch: `codex-frontend-dashboard`
- Remote branch: `origin/codex-frontend-dashboard`
- There is a pre-existing local `AGENTS.md` modification. Do not stage or revert it
  unless the user explicitly asks.

## Documentation Layout

- `docs/README.md` is the documentation index.
- `docs/current/` is the source of truth for active implementation work.
- `docs/current/gateway-relay.md` is the current source of truth for Gateway/Relay control-plane architecture.
- `docs/current/roadmap.md` is the staged scope and no-legacy-burden source of
  truth. Stage 1 and Stage 2 are planned product direction; Stage 3 is a
  candidate pool only and must not be implemented until explicitly selected.
- `docs/current/oauth-channels.md` is the current source of truth for OAuth-backed Codex, Gemini Code, Claude Code, Antigravity, and standard provider API alignment.
- Runtime logging is documented in `docs/current/platform-design.md`; backend
  logs are structured JSON from `internal/logger`, controlled by
  `logging.level` in config. Current local development config uses `debug`;
  production should normally use `info` or `warn`. The logger has a global
  redaction fallback for common credential fields and token-shaped strings, but
  handlers should still avoid logging full request bodies or credentials.
- OAuth channel model presets and provider quota/usage metadata display are
  documented in `docs/current/oauth-channels.md`; the old
  `docs/reference/cli-auth-reference.md` is only a pointer to avoid stale auth
  guidance.
- `docs/api-reference/` is retained as protocol-standard reference material for
  OpenAI Chat Completions, OpenAI Responses, Gemini, and Anthropic Messages. It
  prevents non-standard interface drift and is not business-roadmap clutter.
- `docs/deployment/` contains deployment and operations notes.
- `docs/reference/` contains background reference material only.

## Current Channel State

- Admin channels are listed directly as top-level items; `channel_group` is no
  longer a user-facing grouping concept in the Web UI.
- The channel page uses a narrow channel rail, dense account cards, and
  drawer-based channel/account editing.
- Channel provider family remains `channels.type`: `openai`, `gemini`,
  `anthropic`.
- Protocol/client variant is `channels.api_format`: `standard`, `responses`,
  `codex`, `gemini_code`, or `claude_code`.
- Upstream endpoints are account-level configuration. API-key accounts can set
  a custom endpoint through Base URL plus route prefix; blank route prefix uses
  the standard provider path, and OAuth/Code accounts receive the provider
  default endpoint
  automatically when bound.
- OAuth accounts store encrypted refresh tokens plus JSON `accounts.metadata`
  for provider account/project/plan fields.

- Relay node bindings are channel-level. The historical `/api/admin/node-channels`
  path is kept, but the binding payload is `relay_node_id + channel_id`; runtime
  config expands each bound channel to all enabled accounts in that channel.
- OAuth lifecycle follows upstream client behavior on use. UAPI adds expiry-driven
  idle maintenance only for Claude Code and Gemini Code, because their normal
  local client paths do not have a standalone long-idle keep-alive loop. Each
  enabled Claude Code/Gemini Code OAuth account gets a timer based on provider
  `token_expiry`, with stable jitter between 60 and 5 minutes before expiry;
  Codex keeps the official client's expired-token/8-day refresh rule.
- OAuth channel behavior must be checked against the local upstream official
  client sources listed in `docs/current/oauth-channels.md` before changing auth,
  refresh, metadata, or request-shaping logic.
- Protocol conversion rule: same-protocol HTTP bodies and SSE streams are
  preserved raw where possible (raw preservation). Cross-protocol requests/responses
  use internal structures with graceful degradation: equivalent fields are mapped,
  fields without target-protocol equivalents are logged with warning and skipped
  (unless they would invalidate core prompt/tool flow), and only malformed input or
  missing required fields cause explicit conversion errors.
- Downstream model-list endpoints are local database reads. They use configured
  channel models plus channel model aliases and the user's active plan policy.
  They must not call upstream providers on client requests. Admins use
  `POST /api/admin/channels/models/sync?id=<channel_id>` when they want to sync
  a channel's local model catalog.

## Frontend

The frontend is under `web/`.

Stack:

- Next.js 15 App Router (static export)
- React + TypeScript
- Plain CSS design system
- `lucide-react`

Build mode:

- `web/next.config.ts` uses `output: "export"`
- Production preview: `npm --prefix web run serve:static`
- For development: `npm --prefix web run dev` (live hot-reload)
- For review: build first (`npm --prefix web run build`), then
  `npm --prefix web run serve:static` to serve the static export.

Main routes:

- Auth: `/`, `/login`, `/register`, `/forgot-password`
- User console: `/overview`, `/keys`, `/usage`, `/plans`, `/settings`
- Admin console: `/admin/dashboard`, `/admin/relay-nodes`, `/admin/channels`,
  `/admin/users`, `/admin/plans`, `/admin/logs`, `/admin/audit-logs`,
  `/admin/settings`
- `/admin/accounts` is a legacy-link explanation page only. Accounts are
  conceptually folded into channels and should not regain primary navigation.

Login behavior:

- The login form is intentionally minimal: title, email, password, login button,
  forgot password, register.
- It tries `/api/user/login` first.
- If user login fails, it tries `/api/admin/login` using the email prefix as the
  admin username.
- Static preview fallback accounts:
  - Admin: `admin@example.com` / `admin123`
  - User: `user@example.com` / `user123456`

Navigation rules:

- User console must not show admin navigation.
- Admin console must not show user self-service navigation.
- Admins who want to use the API should create a normal user account.

## Backend Architecture

Stack: Go + fasthttp + GORM/PostgreSQL + JWT (HS256) + AES-256-GCM

Directory structure (implemented):

```
internal/
├── server/
│   ├── server.go              # Server init, lifecycle, route registration
│   └── router.go              # Prefix-match router with :param extraction
├── auth/
│   ├── jwt.go                 # JWT generate/verify (dual: admin + user)
│   └── middleware.go           # JWT auth middleware
├── gateway/                   # Gateway request auth, node scheduling, reverse proxy
├── internalauth/              # Gateway/Relay HMAC signing
├── relay/                     # Core relay execution engine
│   ├── handler.go             # Dispatch/execution logic
│   ├── handler_test.go        # Handler tests
│   ├── account_refresh.go    # OAuth token auto-refresh
│   ├── pool.go                # Weighted round-robin pool
│   ├── affinity.go            # Channel affinity cache
│   ├── billing.go             # PreConsume/Settle/Refund
│   ├── concurrency.go         # Shared per-key concurrency limiter
│   ├── streaming.go           # SSE stream forwarding
│   ├── sse_reader.go          # SSE reader
│   ├── stream_converter.go    # Stream-to-non-stream conversion
│   └── provider/              # Upstream adaptors
│       ├── types.go           # Adaptor interface + internal format
│       ├── credentials.go     # Credential extraction
│       ├── convert.go         # Format conversion registry
│       ├── openai/            # OpenAI Chat Completions API/OpenAI Responses API adaptor
│       │   ├── adaptor.go
│       │   ├── auth.go
│       │   ├── responses.go
│       │   ├── response_convert.go
│       │   └── to_internal.go
│       ├── anthropic/         # Anthropic Messages API adaptor
│       │   ├── adaptor.go
│       │   ├── crypto.go
│       │   ├── streaming.go
│       │   ├── response_convert.go
│       │   ├── to_internal.go
│       │   └── from_internal.go
│       └── gemini/            # Gemini adaptor
│           ├── adaptor.go
│           ├── auth.go
│           ├── streaming.go
│           ├── response_convert.go
│           ├── to_internal.go
│           └── from_internal.go
├── user/                      # User system
│   ├── handler.go
│   ├── service.go
│   └── dto.go
├── admin/                     # Admin backend
│   ├── handler.go             # Route dispatch (login, setup, dashboard)
│   ├── channel_handler.go
│   ├── account_handler.go
│   ├── token_handler.go
│   ├── plan_handler.go
│   ├── user_handler.go
│   ├── log_handler.go
│   ├── dto.go
│   ├── audit.go
│   └── scheduler.go
├── db/                        # Data models
│   ├── db.go                  # InitDB + AutoMigrate
│   ├── user.go
│   ├── channel.go
│   ├── account.go
│   ├── token.go
│   ├── plan.go
│   ├── log.go
│   ├── audit_log.go
│   └── redeem_code.go
├── crypto/                    # AES-256-GCM encryption
└── config/
    └── config.go
```

## Implemented API Routes

All routes below are registered in `internal/server/server.go`.

### User API (short access JWT + long refresh JWT)

```
POST   /api/user/register
POST   /api/user/login
POST   /api/user/refresh
GET    /api/user/profile
POST   /api/user/password
POST   /api/user/email
GET    /api/user/keys
POST   /api/user/keys
DELETE /api/user/keys/:keyID
GET    /api/user/usage
GET    /api/user/usage/logs
GET    /api/user/subscription  # 当前套餐、总额度、小时/周/月窗口剩余
POST   /api/user/redeem
```

### Admin API (short access JWT + long refresh JWT)

```
POST   /api/admin/login
POST   /api/admin/refresh
GET    /api/admin/init-status
POST   /api/admin/setup
GET    /api/admin/dashboard
CRUD   /api/admin/access-policies   # policy resource used by plan management
CRUD   /api/admin/relay-nodes
CRUD   /api/admin/node-channels   # node-channel bindings; request/response uses channel_id
CRUD   /api/admin/channels
POST   /api/admin/channels/oauth/auth-url
GET    /api/admin/channels/oauth/callback
POST   /api/admin/channels/oauth/complete
GET    /api/admin/channels/oauth/status
POST   /api/admin/channels/oauth/bind
CRUD   /api/admin/accounts   # credential export is POST-only and requires admin password
CRUD   /api/admin/tokens   # internal/admin API only; no first-stage admin UI
CRUD   /api/admin/plans
CRUD   /api/admin/redeem-codes
GET/PUT /api/admin/settings
GET    /api/admin/users
PUT    /api/admin/users
DELETE /api/admin/users
GET    /api/admin/logs
GET    /api/admin/audit-logs
```

### Gateway / Relay API (Gateway auth + scheduling, Relay execution)

```
ANY    /v1/chat/completions    # OpenAI Chat Completions API
ANY    /v1/responses           # OpenAI Responses API HTTP/SSE; WS 仅 all-in-one 模式暴露
GET    /v1/models              # 当前 API Key 可用模型列表
ANY    /v1/images/*            # OpenAI Images API
ANY    /v1/messages            # Anthropic Messages API
ANY    /v1beta/*               # Gemini generateContent API
GET    /v1beta/models          # Gemini 格式模型列表
```


## Gateway / Relay Direction

The current target is single Gateway as the control authority and one or more Relay
execution nodes. Frontend/admin users manage Gateway only. Gateway owns users,
API keys, plan-bound access policies, channels, accounts, Relay nodes,
channel-level node bindings, scheduling, and billing. Relay nodes should become execution-only workers that
accept Gateway-signed requests, execute the selected channel/account, and report
usage back to Gateway. Remote Relay nodes do not require PostgreSQL or Redis; they
pull assigned runtime config into process memory and keep the request hot path
database-free. See `docs/current/gateway-relay.md` before changing relay or
gateway behavior.

## Backend Changes on This Branch

- `internal/admin/dto.go`: `UpdateUserRequest` supports `new_password`.
- `internal/admin/oauth_handler.go`: Admin channel OAuth onboarding supports auth
  URL creation, provider callback exchange, session status, and binding a completed
  OAuth session into an `oauth_token` account.
- `internal/db/account.go`: OAuth accounts can store an encrypted `client_secret`
  for providers that require it during refresh.
- `internal/db/token.go`: User API keys support `ip_whitelist`, `expires_at`,
  `models`, and `permissions`. API keys do not bind policies or packages
  directly; Gateway resolves access limits from the key owner's active user
  subscription (`token_plans.user_id` -> `plans.policy_id`) before scheduling
  relay execution.
- `internal/user/service.go`: `CreateKeyRequest` accepts advanced key fields and
  returns them from key listing/creation responses.
- `internal/user/dto.go` and `internal/user/service.go`: Usage endpoints return
  typed summary/log payloads instead of generic maps.
- `internal/admin/user_handler.go`: Admins can reset user password via `new_password`
  (min 8 chars, bcrypt hashed).
- `internal/relay/account_refresh.go`: OpenAI OAuth refresh can re-run the Codex
  token-exchange flow when an `id_token` is returned.
- `internal/logger/logger.go`: Global leveled JSON logger for app, gateway,
  relay, WebSocket, billing, credential refresh, and scheduler diagnostics.
- `internal/relay/handler.go`: Upstream failures now persist a parsed
  `error_message` into request logs; Gemini Code 4xx/5xx logs include upstream
  model, project, enabled credit types, and a compact response body in stdout.
- `internal/user/service.go`: Password change validates length; API key deletion
  uses `deleted_at` soft-delete instead of GORM hard delete.

## Frontend Changes on This Branch

- `web/app/settings/page.tsx`: Password and email settings are wired to
  `POST /api/user/password` and `POST /api/user/email` with validation and
  success/error states.
- `web/lib/api.ts`: User settings API helpers are available as
  `userApi.updatePassword` and `userApi.updateEmail`.
- `web/components/admin-channel-console.tsx`: The channel modal calls the OAuth
  backend endpoints to open provider authorization, poll callback status, and bind
  completed sessions into channel credentials.
- `web/app/keys/page.tsx`: User key creation includes IP whitelist, expiry, model
  restriction, and scoped endpoint permissions.
- `web/app/usage/page.tsx`: Usage charts and logs consume typed
  `UsageSummary`/`UsageLogs` API responses with static preview fallback.
- `web/lib/api.ts`: Admin channel OAuth helpers are available as
  `adminApi.startChannelOAuth`, `adminApi.channelOAuthStatus`, and
  `adminApi.bindChannelOAuth`.
- `web/app/admin/plans/page.tsx`: Plan management composes plan CRUD with
  access-policy CRUD so admins configure count quota for `count_based` plans,
  token quota for `token_based` plans, model limits, fixed-window limits, and
  max concurrency from the plan page.
- `web/app/admin/relay-nodes/page.tsx`: Relay node management is available for
  node address, region, egress IP, weight, max concurrency, status, and
  channel-level node bindings.

## Known Remaining Gaps

Gateway/Relay architecture is now implemented for the current production target:
single Gateway/control plane plus one or more execution-only Relay nodes. The
implemented path includes channel-level node bindings, Gateway scheduling of
`relay_node + channel + account`, Relay config pull into memory, HMAC-only Relay
execution, and usage-event settlement by `request_id`.

Remaining hardening items are intentionally deferred: node-specific credential
encryption, a durable retry queue for usage events when a remote Relay cannot
reach Gateway after a request completes, and durable retry for remote Relay
OAuth account-update pushes if Gateway is temporarily unreachable.

## Commands

```bash
# Frontend
npm --prefix web install
npm --prefix web run build
npm --prefix web run serve:static

# Backend
go test ./...
go build ./...

# Binary entry point
go run ./cmd/uapi/
```

## Verification Standard

Before handing back work, run:

```bash
go test ./...
npm --prefix web run build
npm --prefix web audit --audit-level=high
rg "TODO|FIXME|debugger|>CR<" web internal -g "!node_modules" -g "!.next" -g "!out"
git diff --check
```

Current relay notes:

- Default HTTP body size, WS message size, nginx `client_max_body_size`, and the
  checked-in example configs are aligned at 256 MB. The local `config.yaml` is
  git-ignored but mounted by default Docker Compose, so keep it aligned too.
- SSE conversion must preserve `event:` names and significant `data:` payload
  whitespace. Only the single optional space after `data:` may be removed.
- The Responses WS HTTP-SSE bridge synthesizes a final `[DONE]` for Chat Completions
  upstreams that ended after `finish_reason` without sending `[DONE]`.

Also verify static routes after `npm --prefix web run serve:static`:

- `/`, `/login/`, `/register/`, `/forgot-password/`
- `/overview/`, `/keys/`, `/usage/`, `/plans/`, `/settings/`
- `/admin/`, `/admin/dashboard/`, `/admin/channels/`, `/admin/users/`,
  `/admin/plans/`, `/admin/logs/`, `/admin/audit-logs/`,
  `/admin/settings/`, `/admin/accounts/`

Known dependency note:

- `npm audit --audit-level=high` passes.
- `npm audit` reports moderate PostCSS/Next advisories. Do not run
  `npm audit fix --force`; it suggests a breaking downgrade to an old Next version.
