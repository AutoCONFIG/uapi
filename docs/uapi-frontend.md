# UAPI Frontend Notes

UAPI is the public-facing name for the frontend product. It is short for
Unified API / Your API and is presented as a unified AI API gateway.

## Current Frontend Shape

The frontend lives entirely under `web/` and is intentionally separated from the Go
backend. It is a Next.js static export that can be served from `web/out`.

Primary surfaces:

- Public auth: `/`, `/login`, `/register`, `/forgot-password`
- User console: `/overview`, `/keys`, `/usage`, `/plans`, `/settings`
- Admin console: `/admin/dashboard`, `/admin/channels`, `/admin/users`,
  `/admin/tokens`, `/admin/plans`, `/admin/logs`, `/admin/audit-logs`

The user console does not expose admin navigation. The admin console does not expose
user self-service navigation. Admins who want to use the API should create a normal
user account.

## Backend API Alignment

The frontend currently follows the implemented backend routes:

- User auth: `POST /api/user/register`, `POST /api/user/login`, `POST /api/user/refresh`
- User console: `/api/user/profile`, `/api/user/keys`, `/api/user/usage`,
  `/api/user/usage/logs`, `/api/user/subscription`, `/api/user/plans`
- Admin auth/setup: `/api/admin/login`, `/api/admin/init-status`, `/api/admin/setup`
- Admin CRUD: `/api/admin/channels`, `/api/admin/accounts`, `/api/admin/users`,
  `/api/admin/tokens`, `/api/admin/plans`, `/api/admin/logs`, `/api/admin/audit-logs`

The unified login form tries user login first, then admin login. In static preview mode,
it keeps local fallback accounts so the UI remains navigable without the Go API server:

- Admin: `admin@example.com` / `admin123`
- User: `user@example.com` / `user123456`

## Admin Channel Model

The UI treats channels as the single top-level object for upstream access. Accounts,
API keys, and OAuth credentials are represented as credentials within a channel rather
than as a separate primary navigation item. The old `/admin/accounts` route remains as
a compatibility page only.

## Known Backend Gaps

These are intentional frontend placeholders until backend endpoints exist:

- Admin random user password reset. The backend currently supports status/balance
  update and delete, but not password reset.
- OAuth onboarding from the channel page. The backend needs endpoints for auth URL
  creation, callback status, and account binding.
- User API key scopes. User key creation currently accepts only `name`; IP whitelist,
  expiry, model restrictions, and scoped permissions need backend fields.
- Typed usage payloads. Usage endpoints currently return generic maps, which limits
  chart safety.

## Local Commands

```powershell
npm --prefix web install
npm --prefix web run build
npm --prefix web run serve:static
```
