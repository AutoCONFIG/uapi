# UAPI Frontend Notes

For a concise current-state handoff, read `docs/current/handoff.md` first.

UAPI is the public-facing name for the frontend product. It is short for
Unified API / Your API and is presented as a unified AI API gateway.

## Current Frontend Shape

The frontend lives entirely under `web/` and is intentionally separated from the Go
backend. It is a Next.js 15 static export that can be served from `web/out`.

Primary surfaces:

- Public auth: `/`, `/login`, `/register`, `/forgot-password`
- User console: `/overview`, `/keys`, `/usage`, `/plans`, `/settings`
- Admin console: `/admin/dashboard`, `/admin/relay-nodes`, `/admin/channels`,
  `/admin/users`, `/admin/tokens`, `/admin/plans`, `/admin/logs`,
  `/admin/audit-logs`

The user console does not expose admin navigation. The admin console does not expose
user self-service navigation. Admins who want to use the API should create a normal
user account.

## Backend API Alignment

The frontend calls the implemented backend routes:

- User auth: `POST /api/user/register`, `POST /api/user/login`,
  `POST /api/user/refresh`
- User console: `/api/user/profile`, `/api/user/keys`, `/api/user/usage`,
  `/api/user/usage/logs`, `/api/user/subscription`, `/api/user/plans`,
  `/api/user/password`, `/api/user/email`
- Admin auth/setup: `/api/admin/login`, `/api/admin/init-status`,
  `/api/admin/setup`
- Admin CRUD: `/api/admin/access-policies` for plan-composed policy resources,
  `/api/admin/relay-nodes`, `/api/admin/channels`, `/api/admin/accounts`,
  `/api/admin/users`, `/api/admin/tokens`, `/api/admin/plans`,
  `/api/admin/logs`, `/api/admin/audit-logs`
- Admin channel OAuth: `POST /api/admin/channels/oauth/auth-url`,
  `POST /api/admin/channels/oauth/complete`,
  `GET /api/admin/channels/oauth/status`, and
  `POST /api/admin/channels/oauth/bind`. Provider callbacks return to
  `GET /api/admin/channels/oauth/callback`.

The unified login form tries user login first, then admin login. In static preview mode,
it keeps local fallback accounts so the UI remains navigable without the Go API server:

- Admin: `admin@example.com` / `admin123`
- User: `user@example.com` / `user123456`

## Admin Channel Model

The UI treats channels as the single top-level object for upstream access. Accounts,
API keys, and OAuth credentials are represented as credentials within a channel rather
than as a separate primary navigation item. The old `/admin/accounts` route remains as
a compatibility page only.

The `/admin/channels` page groups channels by `channel_group`. The left rail
shows groups and the right side shows channel tiles; clicking a tile opens a
drawer for channel edits and credentials. The stored `default` group is displayed
as `默认渠道`.

The channel drawer and create drawer support three provider families:
OpenAI, Gemini, and Anthropic. Each family has normal API-key configuration; OpenAI
also exposes the `standard` Chat Completions and `responses` API format switch. Code login
buttons create Codex, Gemini Code, or Claude Code OAuth channels. The backend
returns a provider authorization URL, the UI polls callback status by `state`
where possible, and a completed session is bound as an `oauth_token` account
inside the channel. OAuth account metadata is shown in the credential list when
available. Code channel presets pre-fill model allow-lists from the local
upstream client source trees, and the credential list displays provider quota or
credit metadata when the backend has synced it.

The `/keys` page creates user API keys with optional `ip_whitelist`, `expires_at`,
`models`, and `permissions`. Permissions map to relay entry points: `chat`,
`responses`, `messages`, `gemini`, and `images`.

The `/usage` page consumes typed `UsageSummary` and `UsageLogs` responses from
`/api/user/usage` and `/api/user/usage/logs`, while preserving static preview
fallback rows when the API server is unavailable.


## Gateway Management Surface

The frontend manages Gateway/Control Plane state. It does not manage Relay nodes
directly as independent authorities. Relay nodes are execution workers configured
through Gateway. The `/admin/relay-nodes` page is the current management surface
for node address, region, egress IP, weight, max concurrency, and status. Future
Gateway work should add access policies and account-to-node bindings here rather
than adding separate Relay administration.

## Known Backend Gaps

No frontend placeholder is currently waiting on the original known backend gap list.
