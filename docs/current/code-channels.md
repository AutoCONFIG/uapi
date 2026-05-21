# Code Channel Source Alignment

This document is the active source of truth for CodeX, Gemini Code, and Claude
Code channel behavior. These channels must be implemented from the local
official client source trees first; public API docs are used only to normalize
the standard non-Code API formats.

## Channel Model

- `channels.type` is the provider family: `openai`, `gemini`, or `anthropic`.
- `channels.api_format` selects the transport variant:
  - `codex`: CodeX login and OpenAI Responses-style model requests.
  - `gemini_code`: Gemini Code login and Code Assist API requests.
  - `claude_code`: Claude Code login and Anthropic Messages requests with
    Claude Code OAuth headers.
  - `responses`: standard OpenAI Responses API.
  - `standard`: OpenAI Chat Completions, Gemini generateContent, or Anthropic
    Messages depending on `channels.type`.
- `channels.channel_group` stores UI grouping. Missing or empty group values are
  normalized to `default`; the UI displays this as `默认渠道`.
- OAuth/API credentials are stored as `accounts` under a channel. OAuth accounts
  keep encrypted refresh tokens and structured `metadata` with plan/account
  details synced from the provider flow when available.
- OAuth credentials refresh on use, matching the upstream client lifecycle.
  CodeX follows Codex's on-use proactive rule: refresh when the access token is
  expired or when the last refresh is older than 8 days. Claude Code and Gemini
  Code do not have a general long-idle keep-alive in their normal local client
  paths, so UAPI adds a service-side safety net for those two only. The safety
  net is expiry-driven rather than poll-driven: on startup and after OAuth
  account changes, UAPI schedules one timer per enabled Claude/Gemini OAuth
  account from the provider-supplied `token_expiry`. The timer fires in a stable
  per-account jitter window between 60 and 5 minutes before expiry. It performs
  provider refresh plus metadata/quota sync only; it does not simulate a model
  request.
- Admin channel creation pre-fills Code channel model allow-lists from the local
  upstream model source files. Operators can still edit the list per channel.

## OpenAI / CodeX

Local source of truth:

- `upstream/codex/codex-rs/login/src/server.rs`
- `upstream/codex/codex-rs/login/src/auth/default_client.rs`
- `upstream/codex/codex-rs/login/src/auth/manager.rs`
- `upstream/codex/codex-rs/login/src/token_data.rs`

Implemented alignment:

- Browser auth URL uses the Codex client id, PKCE S256, local callback
  `http://localhost:1455/auth/callback`, `id_token_add_organizations=true`,
  `codex_cli_simplified_flow=true`, and `originator=codex_cli_rs`.
- Device auth uses the Codex device endpoints and sends the shared Codex
  `originator` and `User-Agent` headers.
- Authorization-code exchange and API-key exchange use form-urlencoded payloads.
- Refresh uses JSON `{client_id, grant_type, refresh_token}` and Codex headers.
- Refresh is checked when credentials are used. UAPI mirrors Codex's stale
  criteria from `AuthManager::is_stale_for_proactive_refresh`: expired access
  token, or a last refresh older than 8 days.
- ID token claims are parsed into account metadata using the same useful claim
  set as Codex: email, ChatGPT plan type, user id, account id, and FedRAMP flag.
- Account metadata sync attempts the Codex backend usage endpoint
  `GET https://chatgpt.com/backend-api/api/codex/usage` with Codex backend
  auth headers, matching the local backend-client usage path. When available,
  the response is stored as `metadata.codex_usage`.
- CodeX upstream requests are routed through the OpenAI Responses converter.
- CodeX channel model presets are sourced from
  `upstream/codex/codex-rs/models-manager/models.json` and currently include
  `gpt-5.5`, `gpt-5.4`, `gpt-5.4-mini`, `gpt-5.3-codex`, `gpt-5.2`, and
  `gpt-image-2`.

Standard OpenAI API reference:

- OpenAI Responses API: `https://platform.openai.com/docs/api-reference/responses`
- OpenAI Chat Completions API: `https://platform.openai.com/docs/api-reference/chat`
- OpenAI Images API: `https://platform.openai.com/docs/api-reference/images`
- OpenAI Models API: `https://platform.openai.com/docs/api-reference/models`

## Gemini / Gemini Code

Local source of truth:

- `upstream/gemini-cli/packages/core/src/code_assist/oauth2.ts`
- `upstream/gemini-cli/packages/core/src/code_assist/oauth-credential-storage.ts`
- `upstream/gemini-cli/packages/core/src/code_assist/server.ts`
- `upstream/gemini-cli/packages/core/src/code_assist/setup.ts`
- `upstream/gemini-cli/packages/core/src/code_assist/converter.ts`
- `upstream/gemini-cli/packages/core/src/config/models.ts`

Implemented alignment:

- OAuth uses Gemini CLI's installed-app client id, client secret, Google OAuth
  token URL, and scopes: Cloud Platform, userinfo.email, userinfo.profile.
- Browser/manual callback uses `http://127.0.0.1:1456/oauth2callback`.
- Admin OAuth completion also accepts Gemini CLI token JSON with
  `access_token`, `refresh_token`, `scope`, `token_type`, `id_token`, and
  `expiry_date`. `expiry_date` is interpreted as Gemini CLI's millisecond Unix
  timestamp. The imported token is bound as a normal `oauth_token` account and
  then runs the same Code Assist metadata/setup sync as browser OAuth.
- OAuth refresh uses Google's token endpoint with the stored client secret.
- Gemini CLI relies on Google OAuth's `getAccessToken()` path to refresh when
  needed; it does not run a standalone long-idle keep-alive loop for Code Assist.
  UAPI's idle maintenance covers this server-side deployment gap for Gemini Code.
- Gemini Code requests do not call the public Gemini API directly. They call
  Code Assist:
  - `POST https://cloudcode-pa.googleapis.com/v1internal:generateContent`
  - `POST https://cloudcode-pa.googleapis.com/v1internal:streamGenerateContent?alt=sse`
- Request bodies follow Gemini CLI's Code Assist wrapper:
  `{model, project?, user_prompt_id, request, enabled_credit_types?}`.
- Code Assist request conversion must run through the initialized Gemini
  adaptor, not the global stateless converter, because `project` and optional
  credit fields are derived from the selected OAuth account metadata.
- Account metadata sync follows Gemini CLI's `setupUser` path. It calls
  `loadCodeAssist` with Gemini CLI metadata `{ideType: IDE_UNSPECIFIED,
  platform: PLATFORM_UNSPECIFIED, pluginType: GEMINI, duetProject}` and stores
  returned project/tier details.
- If `loadCodeAssist` returns `VALIDATION_REQUIRED`, UAPI stores
  `metadata.setup_status=validation_required` plus the provider `validationUrl`
  for the admin UI. After the browser validation completes, the next metadata
  sync retries `loadCodeAssist` and continues setup.
- If the account has not been onboarded, UAPI calls `onboardUser` with the
  default allowed tier, waits for the long-running operation like Gemini CLI,
  reloads Code Assist metadata, and stores the resulting project/tier details.
- When a project id is available, metadata sync calls `retrieveUserQuota` and
  stores the returned buckets as `metadata.user_quota`.
  Paid-tier `availableCredits` are preserved under `metadata.paid_tier`.
- Streaming responses unwrap Code Assist `response` chunks before converting to
  UAPI's internal/Gemini/OpenAI formats.
- Gemini Code model presets are sourced from
  `upstream/gemini-cli/packages/core/src/config/models.ts` and include the CLI
  aliases plus stable and preview Gemini model ids.

Standard Gemini API reference:

- Gemini generateContent API: `https://ai.google.dev/api/generate-content`
- Gemini Models API: `https://ai.google.dev/api/models`

## Anthropic / Claude Code

Local source of truth:

- `upstream/claude-code/src/constants/oauth.ts`
- `upstream/claude-code/src/services/oauth/client.ts`
- `upstream/claude-code/src/services/oauth/getOauthProfile.ts`
- `upstream/claude-code/src/services/api/usage.ts`
- `upstream/claude-code/src/services/api/client.ts`
- `upstream/claude-code/src/utils/http.ts`
- `upstream/claude-code/src/utils/model/configs.ts`

Implemented alignment:

- Auth URL uses Claude Code's Claude.ai authorize URL, client id, manual
  redirect `https://platform.claude.com/oauth/code/callback`, PKCE S256, state,
  and all Claude Code OAuth scopes.
- Token exchange and refresh use JSON payloads against
  `https://platform.claude.com/v1/oauth/token`.
- Refresh scopes use Claude Code's Claude.ai scope set without
  `org:create_api_key`.
- Claude Code checks refresh on API-client creation and treats tokens expiring
  within 5 minutes as expired. It does not run a standalone long-idle keep-alive
  loop for the normal local client path. UAPI's idle maintenance covers this
  server-side deployment gap for Claude Code.
- OAuth account metadata sync fetches profile, roles, and first-token-date data
  from the Claude Code OAuth endpoints and stores subscription/rate-limit/billing
  fields on the account.
- OAuth account metadata sync also calls Claude Code's utilization endpoint
  `GET /api/oauth/usage` with Claude Code auth/user-agent headers and stores
  the response as `metadata.usage`.
- OAuth model requests send `Authorization: Bearer`, `anthropic-beta:
  oauth-2025-04-20`, `x-app: cli`, Claude Code-style `User-Agent`, and
  `anthropic-version: 2023-06-01`.
- Claude Code model presets are sourced from
  `upstream/claude-code/src/utils/model/configs.ts` and include canonical
  first-party model ids.

Standard Anthropic API reference:

- Anthropic Messages API: `https://platform.claude.com/docs/en/api/messages`
- Anthropic Models API: `https://docs.anthropic.com/en/api/models-list`
- Anthropic has no standard standalone image-generation endpoint in the public
  API. UAPI supports Claude vision input through Messages conversion, but it
  does not invent `/v1/images` behavior for Anthropic channels.

## Verification Commands

Run these before claiming the channel layer is ready:

```bash
gofmt -w internal/relay internal/admin internal/db
go test ./...
go vet ./...
npm --prefix web run build
docker compose up -d --build
docker compose ps
```
