# UAPI Documentation

This directory is the canonical documentation index for UAPI. New coding
sessions should start with [current/handoff.md](current/handoff.md), then read
the specific current document that matches the task.

## Structure

- `current/`: active product, frontend, backend, and architecture notes. Use
  these files for implementation decisions.
- `deployment/`: deployment and operations notes.
- `reference/`: stable background references. These can explain upstream tools
  or external behavior, but they are not product requirements by themselves.

## Current Source Of Truth

- [current/handoff.md](current/handoff.md): first-read project state, commands,
  known gaps, and verification checklist.
- [current/frontend.md](current/frontend.md): frontend routes, UI boundaries, and
  backend API alignment.
- [current/platform-design.md](current/platform-design.md): platform design,
  architecture, data models, and relay engine.

## Maintenance Rules

- Keep `current/` aligned with implemented behavior before ending a major work
  session.
- Prefer adding cross-links from this index instead of scattering "start here"
  instructions across many files.
