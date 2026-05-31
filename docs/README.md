# UAPI Documentation

This directory is the canonical documentation index for UAPI. New coding
sessions should start with [current/handoff.md](current/handoff.md), then read
the specific current document that matches the task.

## Structure

- `current/`: active product, frontend, backend, and architecture notes. Use
  these files for implementation decisions.
- `api-reference/`: official upstream API reference documents organized by the
  four protocol surfaces implemented by UAPI. This corpus is kept to validate
  protocol behavior and avoid non-standard relay interfaces; it is not business
  roadmap scope.
- `deployment/`: deployment and operations notes.

## Current Source Of Truth

- [current/handoff.md](current/handoff.md): first-read project state, commands,
  known gaps, and verification checklist.
- [current/frontend.md](current/frontend.md): frontend routes, UI boundaries, and
  backend API alignment.
- [current/platform-design.md](current/platform-design.md): platform design,
  architecture, data models, and relay engine.
- [current/roadmap.md](current/roadmap.md): staged product scope, upstream
  project takeaways, and explicit no-legacy-burden rule for the pre-release
  phase.
- [current/gateway-relay.md](current/gateway-relay.md): current Gateway/Relay
  control-plane architecture and implementation status.
- [current/oauth-channels.md](current/oauth-channels.md): OAuth-backed Codex, Gemini Code,
  Claude Code, Antigravity, and standard provider API source alignment.
- [api-reference/README.md](api-reference/README.md): official API reference
  corpus for OpenAI Chat Completions API, OpenAI Responses API, Gemini API, and
  Anthropic Messages API.

## Maintenance Rules

- Keep `current/` aligned with implemented behavior before ending a major work
  session.
- Before first production release, remove or rewrite obsolete behavior instead
  of preserving stale layers that create maintenance burden.
- When code and older prose disagree, treat code plus `current/handoff.md` as the
  immediate source of truth and update or delete the stale prose in the same
  change.
- Prefer adding cross-links from this index instead of scattering "start here"
  instructions across many files.
