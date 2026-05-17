# UAPI Web

Frontend for UAPI, the unified AI API gateway. This app is intentionally
kept in `web/` and should not require backend code changes for UI iteration.

See `docs/uapi-frontend.md` for current product and backend alignment notes.

## Stack

- Next.js App Router
- React
- TypeScript
- Plain CSS design system for the first implementation pass
- `lucide-react` icons

## API Boundary

The client currently follows the backend routes implemented in `internal/server/server.go`:

- User auth: `/api/user/register`, `/api/user/login`, `/api/user/refresh`
- User console: `/api/user/profile`, `/api/user/keys`, `/api/user/usage`, `/api/user/plans`
- Admin: `/api/admin/*`
- Relay traffic: `/v1/*`

The superpowers spec uses `/api/v1/*` for user routes, while the implemented backend uses
`/api/user/*`. The frontend follows the implemented backend until product/API naming is
confirmed.

## Backend Requests To Track

- Admin user management supports status and delete on the backend. Random password reset
  needs a backend endpoint before it can persist.
- API Key creation only accepts `name`; IP whitelist, expiry, model limits, and scoped keys
  need backend fields before the UI can persist them.
- OAuth account onboarding UI is present on the channel page. It needs admin endpoints for
  auth URL creation, callback status, and account binding before it can perform real auth.
- Usage endpoints return generic maps today; typed response contracts would make charts and
  filters safer.
