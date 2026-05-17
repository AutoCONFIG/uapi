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
- Admin console: `/admin/dashboard`, `/admin/channels`, `/admin/users`,
  `/admin/tokens`, `/admin/plans`, `/admin/logs`, `/admin/audit-logs`

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
- Admin CRUD: `/api/admin/channels`, `/api/admin/accounts`, `/api/admin/users`,
  `/api/admin/tokens`, `/api/admin/plans`, `/api/admin/logs`,
  `/api/admin/audit-logs`
- Admin channel OAuth: `POST /api/admin/channels/oauth/auth-url`,
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

The `/admin/channels` modal can create a channel and start OAuth onboarding for
OpenAI/Codex or Gemini. The backend returns a provider authorization URL, the UI
polls callback status by `state`, and a completed session is bound as an
`oauth_token` account inside the channel.

The `/keys` page creates user API keys with optional `ip_whitelist`, `expires_at`,
`models`, and `permissions`. Permissions map to relay entry points: `chat`,
`responses`, `messages`, and `gemini`.

The `/usage` page consumes typed `UsageSummary` and `UsageLogs` responses from
`/api/user/usage` and `/api/user/usage/logs`, while preserving static preview
fallback rows when the API server is unavailable.

## Known Backend Gaps

No frontend placeholder is currently waiting on the original known backend gap list.
