# Gateway Relay Documentation Rewrite Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Rewrite Gateway/Relay documentation so it fully reflects the strict split runtime without stale all-in-one, runtime mode, `/internal/relay/*`, `/internal/execute`, or OriginalURI concepts.

**Architecture:** This is a documentation-only rewrite plus verification pass. The docs should describe current production behavior: `uapi-gateway` is the public API/control-plane owner, `uapi-relay` is an execution node, data-plane forwarding preserves original `/v1/*` and `/v1beta/*` paths, and control-plane paths are explicit. Keep useful deployment, Nginx, debug dump, WebSocket, and operations information while deleting historical migration wording.

**Tech Stack:** Markdown docs, Go package tests, Docker Compose dev stack, ripgrep-style content checks through Claude Code Grep.

---

## File Structure

Modify these documentation files only unless verification reveals another tracked doc with stale split wording:

- `README.md`: high-level product and split runtime overview. Keep quick start and internal path summary short.
- `docs/current/platform-design.md`: current architecture reference. Keep broad platform responsibilities and protocol conversion facts; remove any runtime-mode or all-in-one framing if present.
- `docs/current/gateway-relay.md`: canonical strict Gateway/Relay split document. This should be the most complete architecture document for data plane, control plane, auth boundaries, debug dumps, WebSocket boundaries, and forbidden legacy concepts.
- `docs/deployment/nginx.md`: production Nginx guidance. Keep concrete Nginx examples, allowlists, streaming settings, and remote debug dump config; remove stale single-runtime/all-in-one wording.

Handle temporary review files:

- `1.md`: inspect with `Read`; if it is only a prompt/review artifact, delete it after confirming it contains no project documentation that must be preserved.
- `2.md`: inspect with `Read`; if it is only a prompt/review artifact, delete it after confirming it contains no project documentation that must be preserved.

Do not modify Go implementation files unless a verification step proves the docs cannot be made consistent with current behavior.

---

### Task 1: Inventory stale documentation wording

**Files:**
- Read: `README.md`
- Read: `docs/current/platform-design.md`
- Read: `docs/current/gateway-relay.md`
- Read: `docs/deployment/nginx.md`
- Read: `1.md`
- Read: `2.md`

- [ ] **Step 1: Search docs for legacy split terms**

Use Claude Code Grep, not shell grep, over Markdown/YAML docs with this pattern:

```text
server\.mode|all-in-one|runtime mode|mode selector|/internal/relay|internal/relay|/internal/execute|OriginalURI|X-UAPI-Original-URI|HeaderOriginalURI
```

Scope:

```text
README.md, docs/**/*.md, *.md, docker-compose*.yaml
```

Expected current useful findings:

```text
README.md may only contain internal/relayserver or internal/relay as package paths.
docs/current/platform-design.md may only contain internal/relayserver or internal/relay as package paths.
docs/current/gateway-relay.md should not recommend legacy paths.
docs/deployment/nginx.md currently contains one stale all-in-one/local dump phrase that must be rewritten.
1.md and 2.md are prompt/review artifacts and contain many forbidden terms.
```

- [ ] **Step 2: Read each target doc section before editing**

Read:

```text
README.md
README.md lines 1-120 are enough unless Grep finds more.
docs/current/platform-design.md lines 1-170 are enough unless Grep finds more.
docs/current/gateway-relay.md lines 1-150 are enough unless Grep finds more.
docs/deployment/nginx.md lines 1-260 are enough unless Grep finds more.
```

Expected: enough context to rewrite without losing useful deployment and operations information.

- [ ] **Step 3: Classify each legacy hit**

Classify every hit into one of these categories:

```text
A. Real stale doc wording to rewrite.
B. Package path false positive such as internal/relay/provider.
C. Explicit forbidden-history warning that is acceptable only if worded as "do not use".
D. Temporary review artifact to delete.
```

Expected classification based on current known state:

```text
README.md: package path false positives only.
docs/current/platform-design.md: package path false positives only.
docs/current/gateway-relay.md: likely clean.
docs/deployment/nginx.md: one real stale phrase: "local dump mode is reserved for all-in-one or local development diagnostics".
1.md: temporary review artifact.
2.md: temporary review artifact.
```

---

### Task 2: Rewrite `docs/deployment/nginx.md` stale remote debug dump wording

**Files:**
- Modify: `docs/deployment/nginx.md:235-258`

- [ ] **Step 1: Replace stale all-in-one debug dump sentence**

Replace this sentence:

```markdown
Relay debug dumps are uploaded back to Gateway when enabled. Split `uapi-relay` deployments must either use `mode: "remote"` or keep debug dumps disabled; local dump mode is reserved for all-in-one or local development diagnostics.
```

With this sentence:

```markdown
Relay debug dumps are uploaded back to Gateway when enabled. Production `uapi-relay` deployments should either use `mode: "remote"` or keep debug dumps disabled; Relay local dump mode is only for isolated developer diagnostics where the Relay filesystem is intentionally inspected directly.
```

Rationale: keeps useful local-diagnostics information without reviving all-in-one terminology.

- [ ] **Step 2: Verify the Remote Debug Dumps section still preserves useful information**

After editing, the section must still include:

```markdown
# Gateway
debug_dump:
  enabled: false
  mode: "local"
  dir: "/app/debug-dumps"
  accept_remote: true

# Relay
debug_dump:
  enabled: false
  mode: "remote"
  queue_max_items: 1000
  batch_max_bytes_mb: 8
  upload_timeout: "10s"
```

Expected: remote dump config remains unchanged except for the stale sentence.

---

### Task 3: Strengthen canonical split docs without adding redundancy

**Files:**
- Modify if needed: `docs/current/gateway-relay.md:1-150`
- Modify if needed: `docs/current/platform-design.md:35-50`
- Modify if needed: `README.md:5-118`

- [ ] **Step 1: Ensure `docs/current/gateway-relay.md` explicitly names forbidden legacy concepts as non-current**

If the file does not already include a short non-current boundary section, add this near the internal paths section after the Gateway -> Relay path list:

```markdown
The split runtime does not use an internal execution wrapper. Do not route model execution through an internal control path; Gateway forwards the original business request path and Relay verifies that same path in the HMAC signature. Control paths are only for config, usage, account updates, dump upload, and reload notifications.
```

Do not include literal legacy path strings unless necessary for a user-facing warning. If literal forbidden strings are included, phrase them only as deprecated/non-current, never as examples to configure.

Expected: readers understand that control paths are not data-plane execution paths without reintroducing old route names as recommended config.

- [ ] **Step 2: Ensure `docs/current/platform-design.md` does not describe runtime mode selection**

The config section should list current split-relevant settings only:

```markdown
- `gateway.internal_secret`: Gateway/Relay 内部认证 secret。
- `gateway.config_pull_interval`: Relay 拉取运行时配置间隔，默认 5 秒。
```

If any text describes runtime mode selection or `server.mode`, delete it. Do not replace it with historical discussion.

Expected: platform design remains a current-state reference.

- [ ] **Step 3: Ensure `README.md` remains concise and not redundant**

Keep README limited to:

```markdown
- strict split summary
- features
- quick start
- build
- project layout
- internal path summary
- links to detailed docs
```

If README duplicates detailed Nginx or debug dump content, remove the duplicate and link to:

```markdown
docs/current/gateway-relay.md
docs/deployment/nginx.md
```

Expected: README is not a second architecture spec.

---

### Task 4: Remove temporary review prompt artifacts

**Files:**
- Delete: `1.md` if confirmed as review prompt artifact
- Delete: `2.md` if confirmed as review prompt artifact

- [ ] **Step 1: Read `1.md` and confirm it is a prompt/review artifact**

Read `1.md`. It should contain review instructions and forbidden keyword checklist, not project documentation.

Expected: artifact confirmed.

- [ ] **Step 2: Read `2.md` and confirm it is a prompt/review artifact**

Read `2.md`. It should contain review instructions and forbidden keyword checklist, not project documentation.

Expected: artifact confirmed.

- [ ] **Step 3: Delete only confirmed artifacts**

Use a non-destructive file removal only after confirmation. Because deletion is destructive, ask for user confirmation if the artifact content is ambiguous. If both are clearly temporary review prompts, delete them.

Expected after deletion:

```text
1.md no longer exists.
2.md no longer exists.
```

---

### Task 5: Verify docs keyword hygiene

**Files:**
- Check: `README.md`
- Check: `docs/**/*.md`
- Check: `docker-compose*.yaml`

- [ ] **Step 1: Re-run focused documentation keyword search**

Use Claude Code Grep with pattern:

```text
server\.mode|all-in-one|runtime mode|mode selector|/internal/relay|internal/execute|/internal/execute|OriginalURI|X-UAPI-Original-URI|HeaderOriginalURI
```

Scope:

```text
README.md, docs/**/*.md, *.md, docker-compose*.yaml
```

Expected acceptable results:

```text
No all-in-one, runtime mode, mode selector, /internal/relay URL, /internal/execute, OriginalURI, X-UAPI-Original-URI, or HeaderOriginalURI recommendations.
Possible package path hits for internal/relay are acceptable only if they are source tree paths, not URL paths.
```

- [ ] **Step 2: Check code keyword results for false positives only**

Use Claude Code Grep over Go files with pattern:

```text
/internal/relay|internal/execute|/internal/execute|OriginalURI|X-UAPI-Original-URI|HeaderOriginalURI|/internal/dumps|debugdump
```

Expected:

```text
/internal/dumps and debugdump hits exist and are desired.
internal/relay hits are package import paths or directory names.
No /internal/execute or OriginalURI mechanism remains.
```

---

### Task 6: Run test and runtime verification

**Files:**
- No source files modified by this task.

- [ ] **Step 1: Run specified Go package tests**

Run:

```bash
go test ./internal/gateway ./internal/relay ./internal/relayserver ./internal/internalauth ./internal/debugdump ./internal/relay/provider/convert
```

Expected:

```text
ok github.com/AutoCONFIG/uapi/internal/gateway
ok github.com/AutoCONFIG/uapi/internal/relay
?  github.com/AutoCONFIG/uapi/internal/relayserver [no test files]
ok github.com/AutoCONFIG/uapi/internal/internalauth
?  github.com/AutoCONFIG/uapi/internal/debugdump [no test files]
ok github.com/AutoCONFIG/uapi/internal/relay/provider/convert
```

- [ ] **Step 2: Build and start dev Gateway/Relay**

Run:

```bash
docker compose -f docker-compose.dev.yaml up -d --build gateway relay
```

Expected:

```text
Image uapi-gateway:dev Built
Image uapi-relay:dev Built
Container uapi-gateway-dev Started
Container uapi-gateway-dev Healthy
Container uapi-relay-dev Started
```

- [ ] **Step 3: Check Gateway health through Nginx**

Run:

```bash
curl -sS -i http://127.0.0.1/healthz
```

Expected:

```http
HTTP/1.1 200 OK

{"status":"ok"}
```

---

### Task 7: Final review and report

**Files:**
- Check: all modified docs
- Check: git status

- [ ] **Step 1: Review final diff**

Run:

```bash
git diff -- README.md docs/current/platform-design.md docs/current/gateway-relay.md docs/deployment/nginx.md
```

Expected:

```text
Diff only removes stale split wording, clarifies strict split boundaries, and preserves current operations information.
```

- [ ] **Step 2: Check working tree status**

Run:

```bash
git status --short
```

Expected:

```text
Modified docs are intentional.
1.md and 2.md are deleted if confirmed artifacts.
Existing unrelated changes are not modified.
```

- [ ] **Step 3: Produce final report**

Report in Chinese with these sections:

```markdown
## 结论
## 修改内容
## 保留的信息
## 删除或替换的旧概念
## 残留关键词检查
## 测试和运行验证
## 风险和建议
```

Expected conclusion:

```text
文档已与严格 Gateway/Relay split 架构收口；代码测试和 dev runtime 验证通过。
```

---

## Self-Review

Spec coverage:

- Full documentation rewrite scope is covered by Tasks 1-3.
- Temporary artifact cleanup is covered by Task 4.
- Keyword hygiene is covered by Task 5.
- Go and Docker verification is covered by Task 6.
- Final reporting is covered by Task 7.

Placeholder scan:

- No TBD/TODO placeholders.
- No vague “add appropriate docs” steps; every step names exact files and wording.

Scope check:

- This plan is documentation-only plus verification. It does not refactor implementation code.
- The only deletion is limited to confirmed temporary prompt artifacts `1.md` and `2.md`.
