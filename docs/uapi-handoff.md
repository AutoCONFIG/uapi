# UAPI Handoff

This is the first file a new coding session should read. It captures the current
working state so the next agent can continue without extra user briefing.

## Product

- Public name: **UAPI**
- Meaning: Unified API / Your API
- Positioning: Your Unified AI API Gateway
- Old visible names such as `CLI Relay`, `CR`, and `API Console` should not appear
  in the UI.

## Repository State

- Main workspace: `D:\cli-relay`
- Active frontend branch: `codex-frontend-dashboard`
- Remote branch: `origin/codex-frontend-dashboard`
- PR URL: `https://github.com/AutoCONFIG/cli-relay/pull/new/codex-frontend-dashboard`
- There is a pre-existing local `AGENTS.md` modification. Do not stage or revert it
  unless the user explicitly asks.

Recent branch commits:

- `d59d3d3 feat: add UAPI frontend console`
- `8b99933 feat: align frontend with UAPI backend`
- `8293a23 fix: complete admin user management flow`

## Frontend

The frontend is under `web/`.

Stack:

- Next.js App Router
- React
- TypeScript
- Plain CSS design system
- `lucide-react`

Build mode:

- `web/next.config.ts` uses `output: "export"`
- Production preview should serve `web/out` with `npm --prefix web run serve:static`
- Avoid using `next dev` for user review unless live editing is needed. It is slower
  and can get confused if `next build` runs while dev mode is alive.

Main routes:

- Auth: `/`, `/login`, `/register`, `/forgot-password`
- User console: `/overview`, `/keys`, `/usage`, `/plans`, `/settings`
- Admin console: `/admin/dashboard`, `/admin/channels`, `/admin/users`,
  `/admin/tokens`, `/admin/plans`, `/admin/logs`, `/admin/audit-logs`
- `/admin/accounts` is a compatibility page only. Accounts are conceptually folded
  into channels.

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

## Backend Alignment

Implemented backend routes used by the frontend:

- User auth: `POST /api/user/register`, `POST /api/user/login`,
  `POST /api/user/refresh`
- User console: `/api/user/profile`, `/api/user/keys`, `/api/user/usage`,
  `/api/user/usage/logs`, `/api/user/subscription`, `/api/user/plans`
- Admin auth/setup: `/api/admin/login`, `/api/admin/init-status`,
  `/api/admin/setup`
- Admin CRUD: `/api/admin/channels`, `/api/admin/accounts`, `/api/admin/users`,
  `/api/admin/tokens`, `/api/admin/plans`, `/api/admin/logs`,
  `/api/admin/audit-logs`

Backend changes already made on this branch:

- `internal/admin/dto.go`
  - `UpdateUserRequest` supports `new_password`.
- `internal/admin/user_handler.go`
  - Admins can reset a user password by sending `new_password`; the handler hashes it.
  - `new_password` must be at least 8 characters.
- `internal/user/service.go`
  - User password changes validate new password length.
  - User API key deletion now updates `deleted_at` instead of relying on GORM hard delete.

## Known Remaining Gaps

Do not pretend these are done:

- OAuth onboarding from `/admin/channels` is UI-ready but needs backend endpoints for:
  - auth URL creation
  - callback status
  - account binding
- User API key creation currently accepts only `name`; backend fields are still needed
  for IP whitelist, expiry, model restrictions, and scoped permissions.
- Usage endpoints return generic maps, which limits strongly typed charts.

## Commands

Install frontend dependencies:

```powershell
npm --prefix web install
```

Build frontend:

```powershell
npm --prefix web run build
```

Serve static frontend:

```powershell
npm --prefix web run serve:static
```

Run Go tests:

```powershell
& 'C:\Program Files\Go\bin\go.exe' test ./...
```

The machine has Go at `C:\Program Files\Go\bin\go.exe`. The bare `go` command may
not be visible in a fresh shell until PATH refreshes.

## Verification Standard

Before handing back work, run:

```powershell
& 'C:\Program Files\Go\bin\go.exe' test ./...
npm --prefix web run build
npm --prefix web audit --audit-level=high
rg "TODO|FIXME|debugger|cli-relay-web|CLI Relay|API Console|>CR<" web internal docs\uapi-frontend.md -g "!node_modules" -g "!.next" -g "!out"
git diff --check
```

Also verify static routes after `npm --prefix web run serve:static`:

- `/`
- `/login/`
- `/register/`
- `/forgot-password/`
- `/overview/`
- `/keys/`
- `/usage/`
- `/plans/`
- `/settings/`
- `/admin/`
- `/admin/dashboard/`
- `/admin/channels/`
- `/admin/users/`
- `/admin/tokens/`
- `/admin/plans/`
- `/admin/logs/`
- `/admin/audit-logs/`
- `/admin/accounts/`

Known dependency note:

- `npm audit --audit-level=high` passes.
- `npm audit` reports moderate PostCSS/Next advisories. Do not run
  `npm audit fix --force`; it suggests a breaking downgrade to an old Next version.
