# UAPI Handoff

This is the first file a new coding session should read. It captures the current
working state so the next agent can continue without extra user briefing.

## Product

- Public name: **UAPI**
- Meaning: Unified API / Your API
- Positioning: Your Unified AI API Gateway

## Repository State

- Main workspace: `D:\cli-relay`
- Go module: `github.com/AutoCONFIG/cli-relay`
- Binary entry point: `cmd/relay/main.go` (startup log: `"cli-relay ready"`)
- Active frontend branch: `codex-frontend-dashboard`
- Remote branch: `origin/codex-frontend-dashboard`
- There is a pre-existing local `AGENTS.md` modification. Do not stage or revert it
  unless the user explicitly asks.

## Documentation Layout

- `docs/README.md` is the documentation index.
- `docs/current/` is the source of truth for active implementation work.
- `docs/deployment/` contains deployment and operations notes.
- `docs/reference/` contains background reference material only.

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
├── relay/                     # Core relay engine
│   ├── handler.go             # Dispatch logic
│   ├── handler_test.go        # Handler tests
│   ├── account_refresh.go    # OAuth token auto-refresh
│   ├── pool.go                # Weighted round-robin pool
│   ├── affinity.go            # Channel affinity cache
│   ├── billing.go             # PreConsume/Settle/Refund
│   ├── concurrency.go         # Per-token concurrency limit
│   ├── streaming.go           # SSE stream forwarding
│   ├── sse_reader.go          # SSE reader
│   ├── stream_converter.go    # Stream-to-non-stream conversion
│   └── provider/              # Upstream adaptors
│       ├── types.go           # Adaptor interface + internal format
│       ├── credentials.go     # Credential extraction
│       ├── convert.go         # Format conversion registry
│       ├── openai/            # OpenAI Chat/Responses adaptor
│       │   ├── adaptor.go
│       │   ├── auth.go
│       │   ├── responses.go
│       │   ├── response_convert.go
│       │   └── to_internal.go
│       ├── anthropic/         # Anthropic Messages adaptor
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

### User API (user JWT auth)

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
GET    /api/user/subscription
POST   /api/user/subscription/:planID
POST   /api/user/redeem
GET    /api/user/plans
```

### Admin API (admin JWT auth)

```
POST   /api/admin/login
GET    /api/admin/init-status
POST   /api/admin/setup
GET    /api/admin/dashboard
CRUD   /api/admin/channels
CRUD   /api/admin/accounts
CRUD   /api/admin/tokens
CRUD   /api/admin/plans
GET    /api/admin/users
PUT    /api/admin/users
DELETE /api/admin/users
GET    /api/admin/logs
GET    /api/admin/audit-logs
```

### Relay API (API Key auth, performance-critical path)

```
ANY    /v1/chat/completions    # OpenAI Chat Completions
ANY    /v1/responses           # OpenAI Responses API
ANY    /v1/messages            # Anthropic Messages
ANY    /v1beta/*               # Gemini generateContent
```

## Backend Changes on This Branch

- `internal/admin/dto.go`: `UpdateUserRequest` supports `new_password`.
- `internal/admin/user_handler.go`: Admins can reset user password via `new_password`
  (min 8 chars, bcrypt hashed).
- `internal/user/service.go`: Password change validates length; API key deletion
  uses `deleted_at` soft-delete instead of GORM hard delete.

## Frontend Changes on This Branch

- `web/app/settings/page.tsx`: Password and email settings are wired to
  `POST /api/user/password` and `POST /api/user/email` with validation and
  success/error states.
- `web/lib/api.ts`: User settings API helpers are available as
  `userApi.updatePassword` and `userApi.updateEmail`.

## Known Remaining Gaps

Do not pretend these are done:

- **OAuth onboarding**: UI-ready on `/admin/channels`, but backend endpoints are
  missing for auth URL creation, callback status, and account binding.
- **User API key advanced fields**: Creation accepts only `name`. IP whitelist,
  expiry, model restrictions, and scoped permissions need backend fields.
- **Usage endpoint types**: Usage endpoints return generic maps, which limits
  strongly typed charts.

## Commands

```powershell
# Frontend
npm --prefix web install
npm --prefix web run build
npm --prefix web run serve:static

# Backend
& 'C:\Program Files\Go\bin\go.exe' test ./...
& 'C:\Program Files\Go\bin\go.exe' build ./...

# Binary entry point
& 'C:\Program Files\Go\bin\go.exe' run ./cmd/relay/
```

The machine has Go at `C:\Program Files\Go\bin\go.exe`. The bare `go` command may
not be visible in a fresh shell until PATH refreshes.

## Verification Standard

Before handing back work, run:

```powershell
& 'C:\Program Files\Go\bin\go.exe' test ./...
npm --prefix web run build
npm --prefix web audit --audit-level=high
rg "TODO|FIXME|debugger|>CR<" web internal -g "!node_modules" -g "!.next" -g "!out"
git diff --check
```

Also verify static routes after `npm --prefix web run serve:static`:

- `/`, `/login/`, `/register/`, `/forgot-password/`
- `/overview/`, `/keys/`, `/usage/`, `/plans/`, `/settings/`
- `/admin/`, `/admin/dashboard/`, `/admin/channels/`, `/admin/users/`,
  `/admin/tokens/`, `/admin/plans/`, `/admin/logs/`, `/admin/audit-logs/`,
  `/admin/accounts/`

Known dependency note:

- `npm audit --audit-level=high` passes.
- `npm audit` reports moderate PostCSS/Next advisories. Do not run
  `npm audit fix --force`; it suggests a breaking downgrade to an old Next version.
