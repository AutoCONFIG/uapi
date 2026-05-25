# UAPI Roadmap And Scope

This document defines what UAPI should absorb from reference projects during the
current early-development period. UAPI has not shipped to production, so obsolete
first-draft behavior should be removed or rewritten instead of preserved behind
compatibility layers.

`docs/api-reference/` is different from product planning documents: it is the
protocol-standard corpus used to keep OpenAI Chat Completions, OpenAI Responses,
Gemini, and Anthropic Messages behavior aligned with upstream API contracts. Do
not delete or treat it as business-feature scope unless a file is a duplicate or
known-bad copy.

## Product Boundary

UAPI should remain a high-performance, high-concurrency AI API gateway with a
necessary control plane. It should not become a heavy all-in-one business system.
Business features are included only when they directly support gateway operation,
security, troubleshooting, or a simple plan/subscription workflow.

The request hot path should stay small:

1. Authenticate API key.
2. Resolve the user's active plan and limits.
3. Resolve public model name to upstream model name.
4. Select Relay node, channel, and account.
5. Forward the request.
6. Persist logs, usage, audit, and cleanup work outside the critical forwarding
   path where possible.

The hot path must not synchronously call upstream model-list APIs, refresh quota
dashboards, aggregate reports, execute cleanup jobs, or depend on Redis for
remote Relay runtime state.

## Stage 1: Gateway Core And Necessary Control Plane

Stage 1 is the current implementation focus.

| Area | Scope | Reference Projects | Decision |
| --- | --- | --- | --- |
| Gateway/Relay split | Gateway owns auth, plan policy, scheduling, billing; Relay only executes signed forwarding | CLIProxyAPI, new-api | implement now |
| Channel/account model | channels define provider/protocol/model scope; accounts hold credentials and endpoints | new-api, CLIProxyAPI | implement now |
| Two credential classes | OAuth/login channels and API-key channels | CLIProxyAPI, cockpit-tools | implement now |
| Node binding | bind nodes to channels; runtime expands channel to enabled accounts | UAPI design | implement now |
| Local model catalog | `/v1/models` and `/v1beta/models` read local DB only | new-api, CLIProxyAPI | implement now |
| Manual model sync | admin button/API syncs upstream models into channel config | new-api, CLIProxyAPI | implement now |
| Model alias/redirect | map upstream model IDs to public model IDs; hide upstream aliases downstream | Antigravity-Manager, new-api | implement now |
| Account health | enabled state, cooldown, quota status, credential validity | new-api, cockpit-tools | implement now |
| Request logs | user, IP, token, node, channel, account, model, tokens, latency, status, compact error | CLIProxyAPI, Antigravity-Manager | implement now |
| Audit logs | meaningful admin/user operations, credential export, model sync, plan assignment | Antigravity-Manager | implement now |
| Plans | one active plan per user, upgrade-oriented assignment, plan validity dates | UAPI product need | implement now |
| Redeem codes | redeem codes map to plans, not arbitrary custom values | UAPI product need | implement now |
| Admin/user auth | low-resource AT/RT for admin and user; short AT, long RT | common web practice | implement now |
| Admin boundary | admin manages business; admin should not generate/use downstream API keys | UAPI product need | implement now |

Stage 1 should actively remove stale first-draft product surfaces: standalone
admin token-management UI, account-as-primary navigation, token-bound access
policies, account-bound node bindings, real-time upstream model discovery on
client `/models`, and any UI copy that describes those old concepts.

## Stage 2: Operational Hardening And Lightweight Business Features

Stage 2 can be planned now and implemented after Stage 1 stabilizes. These
features are useful but should remain lightweight and optional where possible.

| Area | Scope | Reference Projects | Decision |
| --- | --- | --- | --- |
| Quota dashboard | compact quota buckets, reset time, warning/exhausted/invalid states | cockpit-tools | plan for Stage 2 |
| Background quota refresh | scheduled refresh with configurable interval, not request-time refresh | cockpit-tools | plan for Stage 2 |
| Better failure handling | account cooldown by error class, automatic skip, cleanup suggestions | new-api, CLIProxyAPI | plan for Stage 2 |
| Usage retention | configurable request-log and redeem-code retention cleanup | Antigravity-Manager | plan for Stage 2 |
| Lightweight statistics | daily/hourly request, token, success-rate summaries | CLIProxyAPI, Antigravity-Manager | plan for Stage 2 |
| Security hardening | credential export password verification, later two-factor hooks | cockpit-tools, common practice | plan for Stage 2 |
| Provider modularity | stable provider module interface for adding more OAuth/API-key channels | CLIProxyAPI | plan for Stage 2 |
| Admin UX polish | denser channel/node pages, clearer drawers, less redundant copy | cockpit-tools | plan for Stage 2 |
| System settings | retention, refresh intervals, advanced operational toggles | Antigravity-Manager | plan for Stage 2 |

Stage 2 should still protect the request hot path. Prefer background jobs,
precomputed summaries, pagination, and retention limits over synchronous report
building.

## Stage 3: Candidate Pool Only

Stage 3 items may be useful later, but they are not approved for implementation
just because they appear here. They are a candidate pool for future selection.

| Candidate | Value | Risk |
| --- | --- | --- |
| Online payment integration | self-service purchase flow | high business complexity |
| Model price table and cost estimation | operator-facing cost visibility | large config surface and maintenance |
| LiteLLM/provider price sync | reduces manual pricing work | external dependency and mismatch risk |
| Advanced BI dashboards | better business operations | storage/query pressure and UI complexity |
| Multi-tenant organizations | enterprise use cases | large auth/permission expansion |
| Multi-Gateway/distributed limiter | higher availability and scale | major architecture complexity |
| Full session/device management | admin security control | requires server-side session state |
| Mandatory two-factor authentication | stronger admin/user security | UX and recovery complexity |
| Plugin/channel marketplace | faster provider expansion | requires versioning and trust model |
| Provider-specific public routes | debugging and explicit routing | can fragment the clean public API |

Stage 3 remains documentation-only until explicitly selected.

## Reference Project Takeaways

- new-api: useful for channel management, model mapping, quota and log ideas;
  avoid copying heavy business-platform complexity early.
- CLIProxyAPI: strongest reference for provider adaptation, model conversion,
  account pools, provider-specific routing, and request monitoring.
- Antigravity-Manager: useful for Antigravity auth, model remapping, quota/account
  status, and operational stats; avoid desktop-specific account switching ideas.
- cockpit-tools: useful for compact quota display, account state colors, scheduled
  refresh, and credential safety prompts; avoid local-client injection workflows.

## No Legacy Burden

Because UAPI has not had a production release, compatibility with abandoned
first-draft logic is not a goal. When the source of truth changes, update the DB
model, backend route behavior, frontend workflow, and docs together. If a stale
API remains temporarily for internal composition, mark it internal/control-plane
only instead of documenting it as user-facing product surface.
