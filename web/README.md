# UAPI Web

Frontend for UAPI, the unified AI API gateway. This app is intentionally
kept in `web/` and should not require backend code changes for UI iteration.

See `docs/current/frontend.md` for current product and backend alignment notes.

## Stack

- Next.js App Router
- React
- TypeScript
- Plain CSS design system for the first implementation pass
- `lucide-react` icons

## API Boundary

The client currently follows the backend routes implemented in `internal/server/server.go`:

- User auth: `/api/user/register`, `/api/user/login`, `/api/user/refresh`
- User console: `/api/user/profile`, `/api/user/keys`, `/api/user/usage`, `/api/user/subscription`, `/api/user/redeem`
- Admin: `/api/admin/*`
- Relay traffic: `/v1/*`

## Backend Requests To Track

The original backend placeholders for channel OAuth onboarding, advanced user API
key fields, and typed usage responses have been implemented. See
`docs/current/handoff.md` for the current verification checklist.
