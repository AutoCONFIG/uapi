# UAPI API Conversion Layer Implementation Specification

> **Architecture Limitations (Known Issues)**
> - Gateway route selection is based on model support, weight, and health — it does NOT consider `clientFormat` to match protocol-family preference. This is **by design**: routing strategy is independent of protocol conversion; the upstream protocol format is determined by channel/account configuration, not by client request format.
> - UAPI does NOT implement Bifrost's raw passthrough gate for Claude Code. Claude Code requests go through standard IR conversion.
> - Error responses use generic shapes, not per-protocol error conversion. This is intentional for simplicity.
> - See Section 2.6 for full list of Bifrost-supported features that UAPI does not currently implement.

Scope: this document is the implementation-level reference for rebuilding and auditing UAPI's API conversion layer against `upstream/bifrost` design ideas and UAPI's current code. It covers the four public protocol families:

- OpenAI Chat Completions
- OpenAI Responses
- Gemini / Google GenAI
- Anthropic Messages

It also covers the OAuth/native CLI channel variants currently implemented by UAPI:

- Codex (`openai` channel with `api_format=codex`)
- Claude Code (`anthropic` channel with `api_format=claude_code`)
- Gemini Code Assist / Gemini CLI (`gemini` channel with `api_format=gemini_code`)
- Antigravity (`antigravity` channel)

Authoritative local sources:

- Existing Bifrost review: `BIFROST_PROTOCOL_CONVERSION_LAYER_REVIEW.md`
- Bifrost HTTP integrations: `upstream/bifrost/transports/bifrost-http/integrations/{router.go,openai.go,anthropic.go,genai.go}`
- Bifrost provider adapters: `upstream/bifrost/core/providers/{openai,anthropic,gemini}`
- UAPI gateway and relay: `internal/server/server.go`, `internal/gateway/gateway.go`, `internal/relay/handler.go`, `internal/relay/request_type.go`
- UAPI conversion IR: `internal/relay/provider/ir/*.go`
- UAPI request/response converters: `internal/relay/provider/convert/*.go`
- UAPI stream converters: `internal/relay/provider/stream/*.go`
- UAPI provider adapters and OAuth: `internal/relay/provider/{openai,anthropic,gemini,antigravity}/*.go`
- OAuth/native reference projects: `upstream/codex`, `upstream/gemini-cli`, `upstream/claude-code-source-*`, `upstream/CLIProxyAPI`, `upstream/Antigravity-Manager`, `upstream/cockpit-tools`

## 1. Audit Result For `BIFROST_PROTOCOL_CONVERSION_LAYER_REVIEW.md`

The existing document is directionally correct but not sufficient for repair work. It correctly records Bifrost's high-level design:

- Bifrost HTTP routes parse protocol-specific requests into internal request structs.
- **Bifrost has two internal request types** (NOT one unified IR):
  - `BifrostChatRequest` for OpenAI Chat Completions: see `upstream/bifrost/transports/bifrost-http/integrations/openai.go:578-584`
  - `BifrostResponsesRequest` for OpenAI Responses, Anthropic Messages, and Gemini generateContent: see `openai.go:727-731`, `anthropic.go:92-99`, `genai.go:124-156`
- This is architecturally different from UAPI's single IR layer (see section 4). UAPI normalizes ALL protocols to a unified IR, while Bifrost keeps Chat and Responses families separate internally.
- Same-provider raw request/response passthrough is an optimization, not byte-for-byte transparent proxying.
- Anthropic Claude Code passthrough is gated and still sanitized.
- Gemini route classification is path/body/metadata dependent.
- OpenAI Responses, Anthropic Messages, and Gemini streams do not use OpenAI Chat `[DONE]` semantics in Bifrost.

The document is not 100% complete for UAPI design because it lacks these implementation-level items:

- It does not describe UAPI's actual gateway/relay split, internal signing, local-first mode, selected channel/account forwarding, or precharged billing behavior.
- It does not describe UAPI's current route surface. UAPI only sends `/v1/*` and `/v1beta/*` through the relay path; it does not expose Bifrost's `/openai/...`, `/anthropic/...`, or `/genai/...` integration prefixes.
- It does not document UAPI's current `provider.Format` matrix and native CLI formats: `codex`, `claude_code`, `gemini_code`, `gemini_cli`, `antigravity`.
- It does not define UAPI's IR fields, item kinds, native raw preservation, loss records, and current cross-protocol extra-field dropping rules.
- It does not describe UAPI's same-protocol behavior: parse/validate plus JSON cleanup, then raw body forwarding. Same protocol does not run through IR emission except Gemini same-format has a special raw model-in-path exception.
- It does not describe current UAPI feature gaps relative to Bifrost, especially async, count tokens, file APIs, batch APIs, cached content APIs, containers, full large-payload hooks, and several Bifrost provider capability gates.
- It does not describe UAPI's OAuth request headers and native envelopes for Codex, Claude Code, Gemini Code Assist, and Antigravity.
- It does not describe UAPI's stream conversion contract and current `[DONE]` behavior. UAPI currently appends `[DONE]` only when the downstream client protocol is OpenAI Chat.
- It does not include per-field mappings for requests, responses, usage, cache tokens, reasoning/thinking, tool calls, structured output, files, images, Gemini `thoughtSignature`, Anthropic `redacted_thinking`, or OpenAI Responses opaque items.

Conclusion: keep `BIFROST_PROTOCOL_CONVERSION_LAYER_REVIEW.md` as a Bifrost audit summary, but use this document as the UAPI implementation spec for the next repair phase.

### 1.1 Strict Bifrost Review Verification Matrix

This section is the source-backed acceptance record for `BIFROST_PROTOCOL_CONVERSION_LAYER_REVIEW.md`. The review is useful, but it is not sufficient as a 100% implementation checklist because several Bifrost route families and provider-boundary details are only summarized. Every row below is grounded in current local source.

| Area | `BIFROST_PROTOCOL_CONVERSION_LAYER_REVIEW.md` status | Source fact | Verification result | UAPI spec action |
|---|---|---|---|---|
| Generic route abstraction | Correctly states route config is the central abstraction. | `upstream/bifrost/transports/bifrost-http/integrations/router.go:92-156` defines batch, file, container, cached-content, async, error, and stream converter slots. | Accurate but review-level. | Keep summary; add Bifrost route-capability matrix below. |
| OpenAI Chat endpoints | Correctly lists Chat main endpoints and Azure deployment dynamic routing. | `upstream/bifrost/transports/bifrost-http/integrations/openai.go:302-319` dispatches Azure deployment suffixes; `openai.go:563-569` registers `/v1/chat/completions` and `/chat/completions`. | Accurate. | UAPI exposes only `/v1/chat/completions`, not Bifrost `/openai/...` aliases. |
| OpenAI Responses endpoints | Correctly lists Responses and input-token routes. | `upstream/bifrost/transports/bifrost-http/integrations/openai.go:712-719` registers `/v1/responses`, `/responses`, `/openai/responses`; `openai.go:794-801` registers `/v1/responses/input_tokens`, `/responses/input_tokens`, `/openai/responses/input_tokens`. | Accurate. | UAPI only treats `/v1/responses` as conversion family; input-token route is unsupported. |
| OpenAI files/containers | Review mentions as boundary but not implementation-level. | `upstream/bifrost/transports/bifrost-http/integrations/openai.go:1693-1913` registers OpenAI file routes; `openai.go:2300-2709` registers containers and container files. | Incomplete for the original task. | UAPI spec must record these as Bifrost-supported, UAPI-unsupported conversion-core routes. |
| Anthropic Messages endpoints | Correctly lists `/v1/messages`, wildcard path, and count-tokens. | `upstream/bifrost/transports/bifrost-http/integrations/anthropic.go:74-183` registers messages and wildcard routes; `anthropic.go:415-448` registers `/v1/messages/count_tokens`; `anthropic.go:450-560` starts batch routes; `anthropic.go:249-275` registers model-list routes. | Accurate for the main Messages route, but incomplete for count-tokens/files/batches in the original review. | UAPI exposes `/v1/messages` as main conversion endpoint; count-tokens/files/batches are not first-class converted routes. |
| Anthropic Claude Code raw gate | Correctly states passthrough is gated and sanitized. | `upstream/bifrost/transports/bifrost-http/integrations/anthropic.go:311-373` gates on `IsClaudeCodeRequest` and Claude model; `upstream/bifrost/core/providers/anthropic/requestbuilder.go:82-104`, `174-196`, `305-312` still rewrites/sanitizes raw body. | Accurate. | UAPI does not implement this Bifrost raw gate; UAPI treats `claude_code` as Anthropic-native family. |
| Gemini generateContent dynamic route | Correctly states Gemini classification is path/body/metadata dependent. | `upstream/bifrost/transports/bifrost-http/integrations/genai.go:102-288` uses one POST `/v1beta/models/{model:*}` route and `extractAndSetModelAndRequestType`; `genai.go:1199-1234`, `1355-1385` classify stream/countTokens/batch. | Accurate. | UAPI treats all `/v1beta/*` as Gemini generate family and does not implement Bifrost's dynamic action matrix. |
| Gemini files/batches/cache | Review mentions but does not fully enumerate. | `upstream/bifrost/transports/bifrost-http/integrations/genai.go:351-547` files; `genai.go:578-677` batches; `genai.go:931-1137` cachedContents. | Incomplete for original task. | UAPI spec must call out all as unsupported or separate future work. |
| SSE `[DONE]` semantics | Correctly states not every stream gets `[DONE]`. | Bifrost stream routing suppresses `[DONE]` for OpenAI Responses and Anthropic Messages by setting `shouldSendDoneMarker=false` when `ResponsesStreamResponseConverter` is used, and skips it for GenAI/Bedrock at terminal write time; see `upstream/bifrost/transports/bifrost-http/integrations/router.go:2594-2596`, `2814-2820`. | Accurate. | Keep UAPI-specific `[DONE]` rule separate from Bifrost behavior. |
| Raw/extra/provider passthrough | Correctly says raw is not transparent, but lacks full field matrix. | Bifrost safe header list at `upstream/bifrost/transports/bifrost-http/integrations/anthropic.go:186-246`; raw-body mutation at `requestbuilder.go:82-104`, `174-196`, `305-312`. | Partial. | Raw/loss/error matrices are now in sections 15-16. |

### 1.2 Bifrost Route-Capability Matrix

| Protocol family | Bifrost endpoints and methods | Request parser/body shape | Internal target | Stream/error/async support | UAPI current delta |
|---|---|---|---|---|---|
| OpenAI Chat Completions | `POST /openai/v1/chat/completions`, `POST /openai/chat/completions`, and Azure-style `POST /openai/openai/deployments/{deploymentPath:*}` via `openai.go:302-319`, `563-569`. | `OpenAIChatRequest` from JSON or large-payload metadata; model/stream hydrated by `openai.go:60-150`. | `BifrostChatRequest`; Chat provider path. | Chat stream converter configured at `openai.go:642-694`; no async chat converter in route config. | UAPI accepts `/v1/chat/completions`; same-format validates then forwards raw-ish JSON; cross-format goes through UAPI IR. |
| OpenAI Responses | `POST /openai/v1/responses`, `/openai/responses`, `/openai/openai/responses`; count tokens `POST /openai/v1/responses/input_tokens` etc. from `openai.go:712-801`. | `OpenAIResponsesRequest`, supports `input`, `instructions`, `tools`, `text.format`, `reasoning`, metadata. | `BifrostResponsesRequest`; count tokens reuses Responses request into `CountTokensRequest`. | Responses response converter at `openai.go:735-743`, async converter at `openai.go:743-764`, stream converter at `openai.go:764-794`. | UAPI accepts `/v1/responses`; does not implement `/v1/responses/input_tokens` conversion route. |
| Anthropic Messages | `POST /anthropic/v1/messages`, wildcard `POST /anthropic/v1/messages/{path:*}` per `anthropic.go:74-183`; count tokens `POST /anthropic/v1/messages/count_tokens` per `anthropic.go:415-448`; batches start at `anthropic.go:450-560`. | `AnthropicMessageRequest` with system/messages/tools/thinking/output_config/cache/tool_result/document blocks. Count tokens reuses `AnthropicMessageRequest` into `CountTokensRequest`. | Main Messages maps to `BifrostResponsesRequest`; count tokens maps to `CountTokensRequest`; batches map to `BatchRequest`. | Response converter at `anthropic.go:102-126`; stream converter and stream error converter at `anthropic.go:130-170`; async converter present at `anthropic.go:111-126`; count-token converter at `anthropic.go:439-440`. | UAPI accepts `/v1/messages`; no Bifrost-style wildcard raw path/count-tokens/batch implementation. |
| Gemini / Google GenAI | One dynamic POST route `/genai/v1beta/models/{model:*}` at `genai.go:102-288`, plus files `genai.go:351-547`, batches `genai.go:578-677`, cached contents `genai.go:931-1137`, model metadata `genai.go:290-320`. | `GeminiGenerationRequest` plus specialized request structs selected by path suffix and context flags. | Main generateContent maps to `BifrostResponsesRequest`; countTokens maps to `CountTokensRequest`; embedding/speech/transcription/image/video/batch go to dedicated Bifrost request types. | Responses stream converter at `genai.go:264-286`; countTokens/image/video/batch converters in same route; error converter `gemini.ToGeminiError`. | UAPI currently treats `/v1beta/*` as Gemini generate; files/batches/cachedContents/countTokens are not first-class conversion routes. |

### 1.3 Bifrost Field-Coverage Checklist

| Field group | Bifrost source fact | Review/spec conclusion | UAPI parity status |
|---|---|---|---|
| messages/content parts | Bifrost converts protocol-specific message structs before provider emission; Chat and Responses request/content-block structs are defined in `upstream/bifrost/core/schemas/chatcompletions.go:14`, `1100-1112` and `upstream/bifrost/core/schemas/responses.go:37`, `894-913`; Anthropic and Gemini request adapters map into Responses in `upstream/bifrost/core/providers/anthropic/responses.go:2134-2206` and `upstream/bifrost/core/providers/gemini/responses.go:16-151`. | Review is directionally correct but lacks a complete per-block table. | UAPI IR has text/image/file/document/audio/video/tool/reasoning/opaque item kinds in `internal/relay/provider/ir/content.go:19-45`. |
| tools/function calling | Bifrost OpenAI and Anthropic adapters sanitize provider-specific tool fields; Anthropic tool types and beta-gated fields exist in `upstream/bifrost/core/providers/anthropic/types.go:1175-1280`. | Review captures examples but not full allowlist/denylist. | UAPI supports function-like tools broadly, with known gaps versus Bifrost sanitizer in section 13. |
| structured output/json schema | Bifrost Anthropic adapter selects GA `output_config.format` or a synthetic structured-output tool path depending provider/thinking/file-citation constraints; see `upstream/bifrost/core/providers/anthropic/responses.go:2180-2184`, `2402-2405`, `2771-2798` and the Chat path at `upstream/bifrost/core/providers/anthropic/chat.go:412-428`, `795-835`. | Partial. | UAPI maps JSON schema across formats but Antigravity has special schema sanitation in `internal/relay/provider/antigravity/adaptor.go:150-156`. |
| multimodal/file/PDF/image/audio | Bifrost has dedicated file/container/media routes and content blocks; OpenAI file/container routes are in `openai.go:1693-1913`, `2300-2709`; Gemini files in `genai.go:351-547`. | Review does not fully enumerate route-level behavior. | UAPI conversion core handles file/image/document-ish content parts but not full file APIs/batch/container routes. |
| reasoning/thinking/cache | Bifrost Anthropic reasoning and cache behavior is provider-capability gated; see provider feature flags in `upstream/bifrost/core/providers/anthropic/types.go:119-131`, capability sanitation in `upstream/bifrost/core/providers/anthropic/utils.go:214-296`, beta-header selection in `utils.go:946-1013`, and reasoning/thinking conversion in `upstream/bifrost/core/providers/anthropic/responses.go:2203-2206`, `4289-4322`. | Partial. | UAPI handles major cross-protocol reasoning paths but not full Bifrost provider capability gates. |
| raw/extra/provider-specific | Bifrost supports explicit extra params and safe header passthrough; see `router.go:84-90` and Anthropic safe headers `anthropic.go:186-246`. | Partial. | UAPI records native fields/losses but drops most cross-protocol extras via `internal/relay/provider/convert/registry.go:161-181`. |

## 2. UAPI Runtime Architecture

### 2.1 Server route boundary

UAPI's HTTP server has two path classes:

- Relay/gateway model APIs:
  - Any path starting `/v1/`
  - Any path starting `/v1beta/`
- Admin/user/internal APIs:
  - `/api/...`
  - `/internal/...`
  - `/healthz`

The server does not register separate Bifrost-style integration prefixes. There is no public `/openai/v1/...`, `/anthropic/v1/...`, or `/genai/v1beta/...` path in the main relay path.

For `/v1/*` and `/v1beta/*`:

- In `server.mode=relay`, the request goes directly to `Relayer.HandleRelay`.
- In `server.mode=gateway`, the request goes to `Gateway.Handle`.
- In `server.mode=all`, the gateway is local-first by default and directly calls the local relayer.
- WebSocket upgrade for `/v1/responses` goes to `WSHandler` only in all mode when websocket handler exists.

Max request body size is enforced before routing by `server.max_body_size_mb`.

### 2.2 Gateway responsibilities

Gateway is not a protocol converter. It is the routing and accounting front end.

For model requests it:

- Extracts `model` from JSON body or from known request path forms.
- Handles `/v1/models` and `/v1beta/models` locally.
- Authenticates downstream bearer token.
- Checks token enabled/deleted/expiry, IP whitelist, model permission, and path permission.
- Applies access-policy overrides for allowed models and max concurrency.
- Pre-consumes billing if billing is enabled.
- Picks a route candidate from `(relay_node, channel, account)` using weighted least-load scoring.
- Signs the forwarded request with internal gateway claims:
  - gateway id
  - token id
  - user id
  - requested model
  - estimated tokens
  - precharged flag
  - token plan id
  - client ip
  - request id
  - selected channel id
  - selected account id
- Removes downstream `Authorization` before internal forwarding.
- Proxies response headers/body back to the client.
- Marks relay nodes temporarily unhealthy after passive failures.

Gateway model list endpoints:

- `GET /v1/models` returns OpenAI-style `{object:"list", data:[...]}` unless auth uses Anthropic-style `x-api-key` without `Authorization`, in which case it emits Anthropic model list shape.
- `GET /v1beta/models` returns Gemini model list shape.

### 2.3 Relay responsibilities

Relay owns conversion and upstream transport.

For each request it:

- Detects downstream request type from path.
- Maps request type to downstream client format.
- Authenticates direct downstream bearer token unless gateway internal signature is valid.
- Enforces model permissions and path permissions.
- Resolves channel/account, either from gateway-selected claims or local channel/account routing.
- Resolves upstream model name with model aliases.
- Derives upstream protocol format from channel type and `api_format`.
- Injects or rewrites `model` for JSON request bodies when needed.
- Optionally injects `stream:true` when channel forces stream or Codex requires stream.
- Builds upstream URL via provider adaptor.
- Converts request body only if downstream client format differs from upstream format.
- Sends buffered, streaming, force-stream, or media request.
- Converts response body and streaming chunks back to downstream protocol when needed.
- Parses usage and cache tokens for billing.
- Records usage events with requested model, routed model, channel/account, client/upstream formats, stream flag, status, latency, token counts, and client IP.

### 2.4 Request type to client format

UAPI detects request type only from path prefixes:

| Path prefix | Request type | Client format |
|---|---|---|
| `/v1/chat/completions` | chat completion | `openai_chat` |
| `/v1/responses` | responses | `openai_responses` |
| `/v1/messages` | messages | `anthropic` |
| `/v1beta/` | Gemini generate | `gemini` |
| `/v1/responses/*` except `/v1/responses/` | unsupported | rejected before conversion |
| `/v1/messages/*` except `/v1/messages/` | unsupported | rejected before conversion |
| `/v1/files`, `/v1/containers`, `/v1/batches` | unsupported | rejected before conversion |
| Gemini non-generate actions under `/v1beta/models/*` (`:countTokens`, `:embedContent`, `:batchEmbedContents`, `:predict`, `:predictLongRunning`, `:batchGenerateContent`) | unsupported | rejected before conversion |
| `/v1beta/files`, `/v1beta/cachedContents`, `/v1beta/batches` | unsupported | rejected before conversion |
| `/v1/images/generations` | image generation | OpenAI Chat media passthrough path |
| `/v1/images/edits` | image edit | OpenAI Chat media passthrough path |
| `/v1/images/variations` | image variation | OpenAI Chat media passthrough path |
| `/v1/audio/speech` | speech | OpenAI Chat media passthrough path |
| `/v1/audio/transcriptions` | transcription | OpenAI Chat media passthrough path |
| `/v1/audio/translations` | translation | OpenAI Chat media passthrough path |
| `/v1/embeddings` | embedding | OpenAI Chat media passthrough path |
| `/v1/moderations` | moderation | OpenAI Chat media passthrough path |
| `/v1/realtime/` | realtime | OpenAI Chat media passthrough path |
| `/v1/videos`, `/v1/video/` | video | OpenAI Chat media passthrough path |
| other | unsupported | rejected before conversion |

Only the first four rows are part of the four protocol conversion families. Media routes are separate passthrough/special handlers.

### 2.5 Channel type to upstream format

| Channel type | `api_format` | Upstream format |
|---|---|---|
| `openai` | `responses` | `openai_responses` |
| `openai` | `codex` | `codex` |
| `openai` | any other / empty | `openai_chat` |
| `anthropic` | `claude_code` | `claude_code` |
| `anthropic` | any other / empty | `anthropic` |
| `gemini` | `gemini_code` | `gemini_code` |
| `gemini` | any other / empty | `gemini` |
| `antigravity` | any | `antigravity` |

Important current-source fact: UAPI's gateway route picker does **not** match upstream format to downstream client protocol family. This is **by design**:
- Routing strategy (weight, priority, account health) should remain independent of protocol conversion
- The upstream protocol format is determined by the channel/account configuration
- Protocol conversion (same-protocol vs cross-protocol) is handled by Relay, not Gateway

Gateway only extracts the requested model, filters candidates by public model support, skips unhealthy or saturated nodes, and picks the lowest `(node.Current+1)/(nodeWeight*accountWeight)` score. See `internal/gateway/gateway.go:254-267` for model extraction, `internal/gateway/gateway.go:294-299` for route selection call, `internal/gateway/gateway.go:643-678` for scoring, and `internal/gateway/gateway.go:705-779` for the loaded route columns. The loaded gateway route does not include channel type and does not compute downstream request protocol.

**What is `clientFormat`?** `clientFormat` is UAPI's internal identifier for the downstream client's protocol format, derived from the request path:
- `openai_chat` = `/v1/chat/completions` (OpenAI Chat Completions)
- `openai_responses` = `/v1/responses` (OpenAI Responses)
- `anthropic` = `/v1/messages` (Anthropic Messages)
- `gemini` = `/v1beta/*` (Gemini / Google GenAI)

`clientFormat` is the **critical signal** that determines whether to use same-protocol passthrough (fast) or cross-protocol conversion (slower IR path):
- When `clientFormat == upstreamFormat`: same-protocol passthrough (no conversion)
- When `clientFormat != upstreamFormat`: cross-protocol conversion via IR

This is a core standard component of UAPI's conversion layer, working alongside path parsing.

Therefore, native protocol preference is a required repair target, not a current guarantee. Current behavior is:

1. Gateway selects `(relay_node, channel, account)` by model support, node/account weight, node current load, node health, and account availability.
2. Gateway signs the selected channel/account into internal claims.
3. Relay must honor the gateway-selected channel/account if claims contain them.
4. Relay derives `upstreamFormat` from the selected channel type and `api_format`.
5. If `clientFormat == upstreamFormat`, Relay same-protocol-normalizes and forwards raw-ish JSON.
6. If `clientFormat != upstreamFormat`, Relay converts through IR/adaptor.

Local relay mode loads enabled channels ordered by descending priority, checks affinity first, then model support, then picks an account from the channel's smooth weighted round-robin pool. See `internal/relay/handler.go:1454-1495`, `internal/relay/handler.go:1500-1546`, `internal/relay/pool.go:50-69`, and `internal/db/channel.go:12-15`.

**Protocol-family preference is NOT part of UAPI design**:

- Gateway route selection is based on model support, node/account weight, node health — it does NOT consider `clientFormat`. This is **intentional** because:
  1. Routing strategy (weight, priority, account selection) should remain independent of protocol conversion
  2. The upstream format is determined by the channel/account configuration, not by the client request format
  3. Relay handles protocol conversion separately — same-protocol vs cross-protocol decision is made after routing

The current behavior is:

1. Gateway selects `(relay_node, channel, account)` by model support, node/account weight, node current load, node health, and account availability.
2. Gateway signs the selected channel/account into internal claims.
3. Relay must honor the gateway-selected channel/account if claims contain them.
4. Relay derives `upstreamFormat` from the selected channel type and `api_format`.
5. If `clientFormat == upstreamFormat`, Relay same-protocol-normalizes and forwards raw-ish JSON.
6. If `clientFormat != upstreamFormat`, Relay converts through IR/adaptor.

**Source code verification**:
- Scoring logic in `internal/gateway/gateway.go:673-676`: `score := float64(node.Current+1) / float64(effectiveWeight)` — no clientFormat factor
- Route loading in `gateway.go:720-731`: loads node_weight, account_weight, channel_api_format, channel_models — no clientFormat

### 2.6 Known Feature Gaps (Bifrost-supported, UAPI-unsupported)

This section documents features that Bifrost implements but UAPI does NOT currently support as first-class conversion routes. These are NOT bugs — they are known gaps that may be addressed in future iterations.

| Feature | Bifrost Endpoint(s) | UAPI Current Status | Notes |
|---|---|---|---|
| Count tokens (OpenAI) | `/v1/responses/input_tokens` | **Explicitly unsupported** | Detected as `unsupported` and rejected before conversion |
| Count tokens (Anthropic) | `/v1/messages/count_tokens` | **Explicitly unsupported** | Detected as `unsupported` and rejected before conversion |
| Async create/retrieve | Various `x-bf-async` routes | **Not implemented** | UAPI has no async conversion path |
| Batch API (OpenAI) | `/v1/batches` | **Explicitly unsupported** | Detected as `unsupported`; no batch converter registered |
| Batch API (Anthropic) | `/v1/messages/batches` | **Explicitly unsupported** | Detected as `unsupported`; no batch converter registered |
| Files API (OpenAI) | `/v1/files/*`, `/containers/*` | **Explicitly unsupported** | Detected as `unsupported`; no file/container route registration |
| Files API (Gemini) | `/v1beta/files/*` | **Explicitly unsupported** | Detected as `unsupported`; no Gemini file route |
| Cached content (Gemini) | `/v1beta/cachedContents/*` | **Explicitly unsupported** | Detected as `unsupported`; no cached content CRUD |
| Video generation | Gemini `:predictLongRunning` | **Explicitly unsupported** | Detected as `unsupported`; no video long-running operation |
| Embeddings | Gemini `:embedContent` | **Explicitly unsupported** as conversion route | OpenAI `/v1/embeddings` remains media passthrough only |
| Image generation (OpenAI) | `/v1/images/generations` | **Implemented only outside conversion core** | OpenAI media passthrough path; Antigravity image requests are converted by a dedicated media handler |
| Realtime API | `/v1/realtime` | **Implemented only outside conversion core** | OpenAI media passthrough path; not part of IR conversion |
| WebSocket Responses | `/v1/responses` (WS) | **Partially implemented outside conversion core** | Separate WS handler exists in all mode and can bridge Responses WS to HTTP/SSE conversion; not part of normal HTTP conversion route detection |
| Azure deployment routing | `/openai/openai/deployments/{path:*}` | **Not implemented** at public layer | UAPI does not expose `/openai/...` integration prefixes |
| Wildcard raw path (Anthropic) | `/anthropic/v1/messages/{path:*}` | **Not implemented** | No raw passthrough path |
| Model list endpoints | Various `/models` | **Partially implemented** | Gateway returns model lists but not full Bifrost behavior |

**Implementation status meaning**:
- **Explicitly unsupported**: Relay request type detection returns `unsupported`; `HandleRelay` returns `400 {"error":"unsupported route"}` before conversion or upstream URL construction.
- **Not implemented**: No first-class conversion implementation exists.
- **Partially implemented**: Some functionality exists but is incomplete compared to Bifrost
- **Implemented only outside conversion core**: The endpoint is handled by a separate media or WebSocket handler, not the normal HTTP IR conversion core
- **Media passthrough path**: The endpoint is handled by a separate media handler, not the conversion core

**Test coverage implication**: None of the above gaps have dedicated conversion tests in the current test suite. When fixing conversion issues, these gaps should not be treated as bugs — they are future work items.

## 3. Same-Protocol, Cross-Protocol, And Raw Behavior

### 3.1 Same-protocol forwarding

When `clientFormat == upstreamFormat`:

- UAPI runs `NormalizeRequestSameProtocol`.
- It removes JSON `undefined` placeholders.
- It invokes the protocol parser to validate body shape.
- It returns the original body after cleanup, not a re-emitted IR body.
- It preserves client raw field ordering/unknown top-level fields as much as cleanup permits.

Exception:

- Gemini same-format uses `rawGeminiSameFormat`; Relay does not inject model into body because Gemini standard model is carried in the URL path, not as a top-level body field.

Same-protocol forwarding is therefore raw-ish, but not completely transparent:

- The request URL is still constructed by adaptor.
- Headers are rewritten by adaptor.
- Model may be injected or rewritten except raw Gemini same-format.
- Stream may be injected depending on `force_stream`, client format, and path.
- JSON cleanup may modify invalid placeholders.

### 3.2 Cross-protocol conversion

When `clientFormat != upstreamFormat`:

1. Parse downstream body to IR with `convert.ToIR`.
2. Set target protocol and record loss metadata with `PrepareRequestForTarget`.
3. Drop native extras for cross-protocol unless the formats are in the same native family.
4. Emit target body using adaptor-specific `FromIR`.

The adaptor is important:

- OpenAI adaptor selects Chat, Responses, or Codex emitter from channel `api_format`.
- Anthropic adaptor selects Anthropic or Claude Code emitter.
- Gemini adaptor selects standard Gemini or Code Assist envelope emitter.
- Antigravity adaptor always emits its v1internal envelope and performs Antigravity-specific model routing and schema sanitization.

### 3.3 Native fields and loss records

IR stores native information in:

- `Request.Native.RawBody`
- `Request.Native.Fields`
- `Request.Native.Unknown`
- per-instruction/per-turn/per-item `Native.Raw`
- per-tool `Native`
- `Losses` at request, item, choice, and response levels

For cross-protocol conversion:

- top-level native fields are recorded as warning losses and not emitted.
- `Generation.Extra` is recorded as warning losses and not emitted.
- item-specific losses are recorded for unsupported content/tool output shapes.
- Same native family preserves more:
  - Anthropic and Claude Code are treated as same native request family.
  - OpenAI Responses and Codex are treated as Responses family for several response/request native raw preserves.

Loss records include:

- source protocol
- target protocol
- JSON path
- field name
- kind
- reason
- severity
- hash of preserved raw value
- native raw value

## 4. Internal IR Data Model

### 4.1 Request IR

`ir.Request` contains:

- `SourceProtocol`, `TargetProtocol`
- `Model`
- `RoutedProvider`, `RoutedChannel`, `RoutedModel` reserved fields
- `Stream`
- `Instructions`
- `Turns`
- `Tools`
- `ToolChoice`
- `Generation`
- `Safety`
- `Cache`
- `Usage`
- `Metadata`
- `Native`
- `Losses`

### 4.2 Generation config

`GenerationConfig` stores common generation fields:

- `MaxTokens`
- `MaxTokensField`
- `Temperature`
- `TopP`
- `TopK`
- `Stop`
- `N`
- `CandidateCount`
- `Seed`
- `LogProbs`
- `TopLogProbs`
- `LogitBias`
- `FrequencyPenalty`
- `PresencePenalty`
- `ResponseFormat`
- `Reasoning`
- `Thinking`
- `ServiceTier`
- `Store`
- `User`
- `ParallelToolCalls`
- `Extra`

Important mapping boundary:

- `LogitBias` is only supported by OpenAI Chat emitter. Cross-protocol targets record a loss.
- `Generation.Extra` is dropped cross-protocol and recorded as loss.
- `Reasoning` and `Thinking` are separate raw slots because OpenAI uses reasoning objects while Gemini/Anthropic often use thinking-specific shapes.

### 4.3 Content item kinds

IR item kinds defined for conversion/audit (24 kinds, defined in `internal/relay/provider/ir/content.go:19-42`):

- **Text** (`ItemText = "text"`) - plain text content
- **Image** (`ItemImage = "image"`) - image content with URL or base64 data
- **File** (`ItemFile = "file"`) - file content with URL, data, or file ID
- **Document** (`ItemDocument = "document"`) - document/paper content (PDF, etc.)
- **Audio** (`ItemAudio = "audio"`) - audio content
- **Video** (`ItemVideo = "video"`) - video content
- **Tool use** (`ItemToolUse = "tool_use"`) - tool invocation request
- **Tool result** (`ItemToolResult = "tool_result"`) - tool execution result
- **Function call** (`ItemFunctionCall = "function_call"`) - OpenAI-style function call
- **Function call output** (`ItemFunctionCallOutput = "function_call_output"`) - function call result
- **Reasoning** (`ItemReasoning = "reasoning"`) - reasoning/thinking content
- **Thinking** (`ItemThinking = "thinking"`) - alternative thinking representation
- **Redacted thinking** (`ItemRedactedThinking = "redacted_thinking"`) - encrypted/omitted thinking
- **Encrypted reasoning** (`ItemEncryptedReasoning = "encrypted_reasoning"`) - encrypted reasoning content
- **Refusal** (`ItemRefusal = "refusal"`) - refusal to respond
- **Citation** (`ItemCitation = "citation"`) - citation/reference content
- **Web search result** (`ItemWebSearchResult = "web_search_result"`) - web search result content
- **Executable code** (`ItemExecutableCode = "executable_code"`) - executable code block
- **Code execution result** (`ItemCodeExecutionResult = "code_execution_result"`) - code execution output
- **Rendered content** (`ItemRenderedContent = "rendered_content"`) - reserved rendered/output content kind; no dedicated payload struct or guaranteed emitter path yet
- **Search content** (`ItemSearchContent = "search_content"`) - reserved search content kind; web search result payloads currently use `ItemWebSearchResult`
- **Cache marker** (`ItemCacheMarker = "cache_marker"`) - cache hint/prompt caching marker
- **Safety block** (`ItemSafetyBlock = "safety_block"`) - safety/filtered content block
- **Opaque** (`ItemOpaque = "opaque"`) - preserves unsupported native objects (not guaranteed to emit)

> **Note**: Not all item kinds are guaranteed to be preserved when converting between protocols. Reserved kinds may exist before a dedicated parser/emitter path. `Opaque` items specifically preserve unsupported native objects for potential passthrough but may be dropped in cross-protocol conversion.

## 5. OpenAI Chat Completions

### 5.1 Downstream endpoint

UAPI accepts:

- `POST /v1/chat/completions`

The request type is `chat_completion` and downstream format is `openai_chat`.

UAPI does not implement Bifrost's OpenAI integration aliases:

- no `/openai/v1/chat/completions`
- no `/openai/chat/completions`
- no Azure deployment-compatible `/openai/deployments/...` route at the public server layer

### 5.2 Request parse to IR

Parser: `parseOpenAIChatRequestDirectIR`.

Top-level fields:

- `model` -> `Request.Model`
- `stream` -> `Request.Stream`
- unknown top-level fields -> `Request.Metadata`, `Native.Fields`, `Native.Unknown`
- raw body -> `Native.RawBody`

Messages:

- `system` and `developer` messages become `Instruction`.
- Other roles become `Turn`.
- role normalization maps:
  - OpenAI `assistant` remains assistant
  - OpenAI `tool` remains tool
  - unknown/empty becomes user when emitting

Message content:

- string content becomes one text content item.
- content array parts are preserved as `schema.ContentPart`.
- `image_url` becomes image item.
- `file` becomes file item.
- `input_file` is normalized to `file` on OpenAI Chat emission.
- `input_image` is normalized to `image_url` on OpenAI Chat emission.
- `input_text` and `output_text` normalize to `text`.

Assistant tool calls:

- `tool_calls[].id` -> IR call id.
- `tool_calls[].function.name` -> tool name.
- `tool_calls[].function.arguments` remains string; it is not forced to valid JSON at parse time.
- Missing function names or ids are rejected when emitting OpenAI Chat.

Tool result messages:

- `role:"tool"` becomes a tool result item.
- `tool_call_id` is required when emitting OpenAI Chat.
- `content` is preserved as raw message content when possible; otherwise text is emitted.

Reasoning:

- `reasoning_content` and `reasoning_details` from assistant message extra fields are parsed into reasoning items.
- On OpenAI Chat emission, reasoning items become `reasoning_content` and/or `reasoning_details` in message extra fields.

Generation fields:

- `max_tokens` -> `Generation.MaxTokens`, `MaxTokensField="max_tokens"`
- `max_completion_tokens` overrides `max_tokens`, `MaxTokensField="max_completion_tokens"`
- `temperature`, `top_p`, `frequency_penalty`, `presence_penalty`, `n`, `seed`, `logprobs`, `top_logprobs`
- `stop` accepts string or string array and becomes `[]string`
- `response_format`
- `logit_bias`
- `parallel_tool_calls`
- `service_tier`
- `store`
- `user`
- `stream_options` stored in `Generation.Extra["stream_options"]`
- `reasoning_effort` is converted to raw reasoning object `{"effort":...}`

Tools:

- OpenAI tools are parsed through generic `irTool`.
- For native OpenAI Chat source emitted back to OpenAI Chat, native tool extra fields are preserved.
- For cross-protocol source emitted to OpenAI Chat, tools are projected to function tools only.

Tool choice:

- Raw `tool_choice` is preserved in IR.
- Native OpenAI Chat source emits raw `tool_choice`.
- Cross-protocol source projects function tool choice into OpenAI-compatible form.

### 5.3 Request emission to OpenAI Chat

Emitter: `emitOpenAIChatRequestDirectIR`.

Emits:

- `model`
- `stream`
- `messages`
- generation fields listed above
- `tools`
- `tool_choice`
- metadata/native fields

OpenAI Chat target-specific behavior:

- A single text part with no extra fields emits as string content.
- Multiple or non-text parts emit as content array.
- PDF `file` parts with data, URL, or `.pdf` filename are emitted as `image_url` for current OpenAI Chat compatibility.
- Other `file` parts emit only `type`, `file_data`, `file_id`, and `filename`; `file_type` and non-PDF URLs are dropped by current UAPI OpenAI Chat file emission.
- Missing tool call name/id is a hard conversion error.
- Missing tool result call id is a hard conversion error.

### 5.4 Response parse and emission

Parser: `parseOpenAIChatResponseDirectIR`.

Response fields:

- `id` -> response id.
- `model` -> response model.
- unknown response fields -> metadata/native fields.
- `usage.prompt_tokens`, `completion_tokens`, `total_tokens` mapped to IR usage.
- prompt cache read tokens from `prompt_tokens_details.cached_tokens` or `prompt_cache_hit_tokens`.
- cache write tokens from `prompt_tokens_details.cache_creation_input_tokens` or `cached_write_tokens`.

Choice fields:

- `choices[].message.role` -> choice role.
- `finish_reason` maps:
  - `stop` -> internal `end_turn`
  - `length` -> `max_tokens`
  - `tool_calls` / `function_call` -> `tool_use`
  - `content_filter` -> `content_filter`
- message content becomes content items.
- `tool_calls` become tool-use items.
- `refusal` becomes refusal item.
- `reasoning_content` and `reasoning_details` become reasoning items.

Emitter: `emitOpenAIChatResponseDirectIR`.

Emits:

- `object:"chat.completion"`
- `created:0` currently, not upstream creation time unless preserved in metadata.
- choices with `message`, `finish_reason`
- usage
- native metadata

OpenAI Chat finish mapping:

- internal `end_turn` -> `stop`
- `max_tokens` -> `length`
- `tool_use` -> `tool_calls`

## 6. OpenAI Responses

### 6.1 Downstream endpoint

UAPI accepts:

- `POST /v1/responses`

UAPI treats only `/v1/responses` and `/v1/responses/` as Responses for HTTP routing. Responses subpaths such as `/v1/responses/input_tokens` are explicitly rejected as unsupported conversion routes. WebSocket bridging uses `/v1/responses` internally when opening the upstream request.

UAPI does not implement Bifrost's OpenAI Responses aliases:

- no `/responses`
- no `/openai/responses`
- no `/v1/responses/input_tokens` count-tokens conversion path as a first-class route in current relay detection
- no Azure deployment route

### 6.2 Request parse to IR

Parser: `parseOpenAIResponsesRequestDirectIR`.

Top-level:

- `model` -> `Request.Model`
- `stream` -> `Request.Stream`
- `instructions` string -> system instruction with one text item
- unknown extras -> metadata/native fields
- selected raw fields copied to metadata:
  - `truncation`
  - `stream_options`
  - `metadata`
  - `user`
  - `previous_response_id`
  - `include`
  - `text`
  - `max_tool_calls`
  - `conversation`
  - `prompt_cache_key`
  - `safety_identifier`

Input:

- string input becomes one user turn with one text item.
- array input is parsed item by item.

Known input item types:

- `message`
- `reasoning`
- `function_call`
- `function_call_output`

Unknown input item types:

- recorded as a loss
- preserved as opaque IR item with native raw

Message input item:

- role maps through Responses role normalization.
- string content or content parts become IR content items.

Reasoning input item:

- role forced to assistant.
- reasoning parts are extracted from item extras.

Function call:

- role forced to assistant.
- `namespace` extra and `name` are combined by `qualifyResponsesNamespaceToolName`.
- `call_id`, `name`, `arguments` become tool-use item.

Function call output:

- role forced to tool.
- `call_id`, `output`, `output_raw` become tool-result item.

Generation:

- `max_output_tokens`
- `temperature`
- `top_p`
- `parallel_tool_calls`, only if present or true
- `service_tier`
- `store`, only if present or true
- raw `reasoning`

Tools:

- `tools` raw JSON is decoded as `[]schema.Tool`.
- All decodable tools are converted to IR tools.
- Current UAPI does not implement Bifrost's full OpenAI Responses tool allowlist/sanitizer; it emits many non-function tools in a generic map when tool type is present.

Tool choice:

- raw `tool_choice` is preserved.

### 6.3 Request emission to OpenAI Responses

Emitter: `emitOpenAIResponsesRequestDirectIR`.

Emits:

- `model`
- `stream`
- `instructions`
- `input` array
- `max_output_tokens`
- `temperature`
- `top_p`
- `parallel_tool_calls`
- `service_tier`
- `store`
- `reasoning`
- `tools`
- `tool_choice`
- metadata/native fields

Native Responses-family preservation:

- If source protocol is OpenAI Responses or Codex and a turn has native raw, the raw item is emitted unchanged.

Content output:

- role-aware content mapping:
  - text -> `input_text` for non-assistant, `output_text` for assistant
  - `image_url` -> `input_image`
  - `file` -> `input_file`
- A single non-assistant text part with no extra fields may emit as a string.
- `cache_control` is skipped on Responses content parts.
- For `input_file`, `file_type` and `mime_type` are not emitted; filename is emitted or defaulted from mime:
  - PDF -> `input.pdf`
  - text -> `input.txt`
  - CSV -> `input.csv`
  - JSON -> `input.json`
  - Markdown -> `input.md`
  - otherwise `input.bin`

Reasoning:

- Emits `type:"reasoning"`.
- Uses `id` from turn/item if available, otherwise generated `rs_<unixnano>`.
- Emits `summary`, optional `encrypted_content`, and status.

Function call:

- Requires name and call id.
- Emits `type:"function_call"`, `call_id`, `name`, `arguments`.

Function call output:

- Requires call id.
- Emits `type:"function_call_output"`.
- `output` is raw decoded JSON when available, otherwise string.

Tools:

- Function tools emit:
  - `type:"function"`
  - `name`
  - `description`
  - `parameters`
  - tool extras and function extras
- Non-function tools emit generic:
  - `type`
  - optional `name`
  - optional `description`
  - optional `parameters`
  - extras

### 6.4 Response parse and emission

Parser: `parseOpenAIResponsesResponseDirectIR`.

Top-level copied metadata:

- `object`
- `created_at`
- `status`
- `metadata`
- `error`
- `incomplete_details`
- `parallel_tool_calls`
- `temperature`
- `top_p`
- `tool_choice`

Usage:

- `input_tokens`, `output_tokens`, `total_tokens`
- cache read from `input_tokens_details.cached_tokens` or `prompt_cache_hit_tokens`

Output items:

- `message` -> choice with content items and finish from item status.
- `function_call` -> choice with tool-use item and finish `tool_call`.
- `reasoning` -> pending reasoning items that are attached to the next message/function-call choice, or flushed as standalone assistant choice at end.
- unknown output item -> loss plus opaque item.

Emitter: `emitOpenAIResponsesResponseDirectIR`.

Emits:

- `id`
- `object:"response"`
- `created_at:0`
- `model`
- `output`
- `usage`
- metadata/native fields
- default status from finish when missing

Output item emission:

- Responses-family native raw choice is emitted raw.
- Responses-family raw opaque or `image_generation_call` item is emitted raw.
- Text/image/file/etc content accumulates into `message` output items.
- Data URI image content may emit as `image_generation_call` with base64 result and output format.
- Reasoning emits `type:"reasoning"`, `id`, `status`, optional `content`, `summary`, and `encrypted_content`.
- Function calls and outputs are emitted as described above.

## 7. Anthropic Messages

### 7.1 Downstream endpoint

UAPI accepts:

- `POST /v1/messages`
- `POST /v1/messages/`

UAPI does not treat arbitrary `/v1/messages/*` subpaths as Anthropic Messages conversion routes. Subpaths such as `/v1/messages/count_tokens` and `/v1/messages/batches` are explicitly rejected as unsupported.

UAPI does not expose Bifrost's `/anthropic/v1/messages` integration prefix.

Current UAPI does not implement separate first-class conversion handling for:

- `/v1/messages/count_tokens`
- Anthropic batches
- Anthropic files
- Anthropic complete legacy API

### 7.2 Request parse to IR

Parser: `parseAnthropicRequestDirectIR`.

Top-level:

- `model` -> `Request.Model`
- `stream` -> `Request.Stream`
- unknown extras -> metadata/native fields
- `metadata` raw is placed under metadata key `"metadata"`
- raw body -> native raw body

System:

- string system becomes one system instruction with one text item.
- array system content blocks become instruction items.
- text blocks are joined into `Instruction.Text` with blank-line separator.

Messages:

- Each message becomes a turn.
- Anthropic role normalization maps unknown/empty to user when emitting.

Content blocks:

- `text` -> text item, preserving block extras.
- `image`:
  - missing source -> opaque item.
  - `source.type=url` -> image URL.
  - otherwise base64 source -> data URI image.
- `document`:
  - `source.type=base64` -> file data as data URI when media type exists.
  - `source.type=text` -> file data text, default `text/plain`.
  - `source.type=url` -> file URL.
  - `source.type=file` -> file id.
  - `title` extra -> filename.
- `tool_use` -> tool-use item with id, name, and input serialized as argument string.
- `tool_result` -> tool-result item:
  - `tool_use_id` -> call id
  - `content` string/blocks/raw -> output text
  - raw content preserved
  - `is_error` preserved
- `thinking` -> reasoning item:
  - `thinking` text
  - `signature` preserved in reasoning extra
  - other extras preserved
- `redacted_thinking`:
  - if `data` exists, becomes encrypted reasoning item with encrypted content.
- unknown block -> content item using block type/text/extras.

Generation:

- `max_tokens`
- `temperature`
- `top_p`
- `top_k`
- `stop_sequences`
- raw `thinking`

Tools:

- raw `tools` decoded as `[]schema.Tool`.
- converted to IR tools.

Tool choice:

- raw `tool_choice` preserved.

### 7.3 Request emission to Anthropic

Emitter: `emitAnthropicRequestDirectIR`.

Emits:

- `model`
- `max_tokens`, default `4096` when missing
- `stream`
- metadata/native fields
- `system`
- `messages`
- `temperature`
- `top_p`
- `top_k`
- `stop_sequences`
- `thinking`
- `tools`
- `tool_choice`

System emission:

- Native Anthropic/Claude Code source with one raw system value emits raw system.
- Otherwise emits content blocks.
- A single text block emits string system.

Messages:

- role `tool` or `function` is emitted as `user` because Anthropic uses tool_result blocks inside user messages.
- Native Anthropic/Claude Code raw blocks are preserved when available.
- Otherwise items are converted block by block.

Anthropic content blocks:

- Text -> `{type:"text", text}` plus extras.
- Image URL/data URI -> Anthropic `image` with base64 source.
- File/input_file -> Anthropic `document`.
- File URL strips `file://` before Anthropic URL source.
- File ID emits `source.type:"file"`.
- File data emits base64 source, parsing data URI when present.
- Missing file data/url/id drops the block.

Tool use:

- Requires name and id.
- Emits `{type:"tool_use", id, name, input}`.
- Arguments string is decoded as JSON if possible; invalid JSON falls back via argument helper.

Tool result:

- Requires `tool_use_id`.
- Emits `{type:"tool_result", tool_use_id, content}`.
- `is_error` emits when true.
- Current UAPI emits tool-result content as string, not rich Anthropic block arrays.

Tool choice:

- OpenAI string `auto` -> `{type:"auto"}`
- `required` -> `{type:"any"}`
- `none` -> `{type:"none"}`
- OpenAI function choice -> `{type:"tool", name}`
- Native Anthropic `auto`, `any`, `none`, `tool` are preserved.
- `parallel_tool_calls` maps to `disable_parallel_tool_use = !parallel_tool_calls` unless choice is `none`.

Thinking:

- `Generation.Thinking` is preferred.
- If missing, raw OpenAI-style `Generation.Reasoning` can be projected.
- Gemini thinking can also be projected.
- If tool choice forces tool use (`any` or `tool`), thinking is not emitted.
- If forced tool choice is present, `output_config.effort` is removed from native extra `output_config`.

Tools:

- Native Anthropic/Claude Code source preserves native non-function tools.
- Cross-protocol source is projected to function tools plus special web_search support.
- `web_search` with `external_web_access:false` is dropped.
- `web_search` emits `type:"web_search_20250305"`, default name `web_search`, optional `max_uses`, `user_location`, and `allowed_domains` from filters.

### 7.4 Response parse and emission

Parser: `parseAnthropicResponseDirectIR`.

Top-level:

- `id`, `model`
- `type`, `stop_sequence` metadata
- usage mapped to IR

Usage:

- `input_tokens`, `output_tokens`
- `cache_creation_input_tokens`
- or sum of `cache_creation.ephemeral_5m_input_tokens + ephemeral_1h_input_tokens`
- `cache_read_input_tokens`

Choice:

- single assistant choice.
- content blocks parsed with the same block parser as requests.
- finish reason maps:
  - `end_turn`
  - `max_tokens`
  - `tool_use`
  - `stop_sequence`
  - unknown preserved.

Emitter: `emitAnthropicResponseDirectIR`.

Emits:

- `id`
- `type:"message"`
- `role:"assistant"`
- `model`
- metadata/native fields
- `content`
- `stop_reason`
- `stop_sequence`
- `usage`

Usage emission:

- nil usage emits `{input_tokens:0, output_tokens:0}`.
- cache creation/read emitted when positive.
- native usage extras preserved if present.

## 8. Gemini / Google GenAI

### 8.1 Downstream endpoint

UAPI accepts Gemini generation paths under:

- `/v1beta/`

Standard generation URL shape expected by adaptor:

- `/v1beta/models/{model}:generateContent`
- `/v1beta/models/{model}:streamGenerateContent`

Relay extracts model from path through `httputil.ModelFromRequestPath`. It also sets `stream=true` if path contains `:streamGenerateContent`.

UAPI explicitly rejects `/v1beta/` paths that are outside the generation conversion core:

- `/upload/v1beta/files`
- `/v1beta/files`
- `/v1beta/cachedContents`
- `/v1beta/batches`
- paths containing `:countTokens`
- paths containing `:embedContent`
- paths containing `:batchEmbedContents`
- paths containing `:predict`
- paths containing `:predictLongRunning`
- paths containing `:batchGenerateContent`

Current UAPI does not implement Bifrost's full GenAI dynamic action matrix as first-class conversion targets:

- `:countTokens`
- `:embedContent`
- `:batchEmbedContents`
- Imagen `:predict`
- speech/transcription via Gemini request modalities
- video `:predictLongRunning`
- batch generate
- operations retrieve
- cached content CRUD
- files API

These may pass only through media/special paths if separately implemented, not through the four-family conversion core.

### 8.2 Request parse to IR

Parser: `parseGeminiRequestDirectIR`.

Top-level:

- `model` is read from extra field `"model"` if present. In normal downstream Gemini, model is path-derived and injected by Relay/adaptor, not body-derived.
- unknown extras -> metadata/native fields.
- raw body -> native raw body.

System instruction:

- `systemInstruction.parts[]` becomes system instruction items.
- Text parts are joined into instruction text.
- Non-text system parts record loss because they have no protocol-neutral instruction representation.

Contents:

- each Gemini content becomes a turn.
- role mapping:
  - Gemini `model` or `assistant` -> IR role `model`
  - Gemini `user` or `tool` -> IR role `user`
  - otherwise `unknown`

Gemini parts:

- `text` with `thought:true` -> reasoning item; `thoughtSignature` preserved.
- `text` without thought -> text item.
- `inlineData`:
  - image mime -> image data URI.
  - other mime -> file data URI.
- `fileData`:
  - image mime -> image URL with `file://` prefix.
  - other mime -> file URL with `file://` prefix.
- `functionCall` -> tool-use item with name and args raw JSON string.
- `functionResponse` -> tool-result item:
  - `name` used as tool call id/name surrogate.
  - `response` raw JSON preserved.
  - losses recorded for response/id/willContinue/scheduling/parts/vendor extras because they do not map cleanly across protocols.
- `thoughtSignature` alone -> encrypted/opaque reasoning item.
- `executableCode` -> executable code item.
- `codeExecutionResult` -> code execution result item.
- unknown part -> opaque item and loss.

Generation config:

- `maxOutputTokens`
- `temperature`
- `topP`
- `topK`
- `stopSequences`
- `candidateCount`
- raw `thinkingConfig`
- `responseMimeType`/`responseSchema` -> OpenAI-style `response_format`:
  - `application/json` without schema -> `{"type":"json_object"}`
  - `application/json` with schema -> `{"type":"json_schema","json_schema":{"schema":...}}`
- generation config extras -> `Generation.Extra`

Safety/cache:

- `safetySettings` -> `Safety.Settings`.
- `cachedContent` -> `Cache.CachedContent`; it is a Gemini generation request field. It is emitted back to Gemini same-protocol/Gemini-target requests and has no first-class equivalent in non-Gemini target protocols.

Tools:

- Gemini `tools[].functionDeclarations` parsed into function tools.
- Generic `[]schema.Tool` fallback parser also exists.

Tool config:

- `toolConfig.functionCallingConfig.mode` normalized.
- `allowedFunctionNames` preserved in raw tool choice as `function_names`.

### 8.3 Request emission to Gemini

Emitter: `emitGeminiRequestDirectIR`.

Emits:

- `systemInstruction`
- `contents`
- `generationConfig`
- `safetySettings`
- `tools`
- `toolConfig`
- metadata/native fields

Model is not emitted into standard Gemini body; adaptor puts model in URL.

System:

- instruction items become Gemini parts.
- instruction text fallback emits `{text:...}`.

Contents:

- role maps:
  - internal assistant/model -> Gemini `model`
  - tool/function/user -> Gemini `user`
  - unknown -> `user`
- Native Gemini/Gemini CLI/Gemini Code/Antigravity raw parts are preserved when source protocol is Gemini-envelope family.

Parts:

- reasoning/thinking -> `text` with `thought:true` and/or `thoughtSignature`.
- tool use -> `functionCall`.
- tool result -> `functionResponse`.
- text -> `{text}`
- image data URI -> `inlineData`
- image `file://` URL -> `fileData`
- file URL -> `fileData`
- file data -> `inlineData`
- executable code -> `executableCode`
- code execution result -> `codeExecutionResult`

Function response naming:

- UAPI builds a map from previous tool call ids to names.
- Tool result response name uses matching tool call name if available; otherwise uses the tool call id.
- Missing function response name is a hard conversion error.

Generation config:

- `maxOutputTokens` is capped to `65536`.
- temperature/topP/topK/stopSequences/candidateCount emitted.
- thinking config:
  - raw Gemini `thinkingConfig` is normalized.
  - snake_case aliases become camelCase:
    - `thinking_budget` -> `thinkingBudget`
    - `thinking_level` -> `thinkingLevel`
    - `include_thoughts` -> `includeThoughts`
  - if `thinkingBudget` exists, `thinkingLevel` is deleted.
  - OpenAI/Anthropic reasoning may be projected into Gemini thinking.
- response format:
  - OpenAI `json_object` -> `responseMimeType:"application/json"`
  - OpenAI `json_schema` -> `responseMimeType:"application/json"` and `responseSchema`
- Generation extras are emitted into `generationConfig` if they do not overwrite already set keys.

Tools:

- Only function-like tools are emitted to Gemini standard.
- Emits `tools:[{functionDeclarations:[...]}]`.
- Function declaration uses:
  - `name`
  - optional `description`
  - `parametersJsonSchema`

Tool choice:

- string/object choices are converted to `functionCallingConfig`.
- OpenAI `none` -> `NONE`
- OpenAI `auto` -> `AUTO`
- OpenAI `required` / `any` -> `ANY`
- function choice adds `allowedFunctionNames` and upgrades mode from AUTO to ANY for explicit function choice.

Metadata:

- Gemini envelope keys are skipped when source is Gemini CLI/Gemini Code/Antigravity:
  - `project`
  - `user_prompt_id`
  - `enabled_credit_types`
  - `userAgent`
  - `requestType`
  - `requestId`
  - `sessionId`
  - `session_id`

### 8.4 Response parse and emission

Parser: `parseGeminiResponseDirectIR`.

It accepts both:

- raw Gemini response
- wrapper `{response: ...}`

Top-level:

- `modelVersion` -> response model.
- extras -> metadata/native fields.
- `usageMetadata` -> IR usage.

Prompt feedback:

- `promptFeedback.blockReason` or safety ratings create a safety choice.
- Safety metadata is preserved in native finish and safety block item.
- Loss records mark safety metadata as target-protocol lossy.

Candidates:

- each candidate becomes a choice.
- `index` preserved.
- `finishReason` maps:
  - `STOP` -> `end_turn`
  - `MAX_TOKENS` -> `max_tokens`
  - `SAFETY` -> `safety`
  - `RECITATION` -> `recitation`
  - `OTHER` -> `other`
- `finishMessage` and `safetyRatings` stored in finish native metadata.
- content parts parsed like request parts.
- safety finish/ratings append a safety block item and loss.

Usage:

- `promptTokenCount` -> input/prompt tokens.
- `candidatesTokenCount` -> output/completion tokens.
- `totalTokenCount`, or sum fallback.
- `cachedContentTokenCount` -> cache read tokens.
- `thoughtsTokenCount` -> output token details `reasoning_tokens`.

Emitter: `emitGeminiResponseDirectIR`.

Emits:

- `candidates`
- `usageMetadata`
- `modelVersion`
- native fields

Choice emission:

- `index`
- `finishReason`
- content parts converted from IR items.
- role uses internal-to-Gemini role mapping.

Usage emission:

- `promptTokenCount`
- `candidatesTokenCount`
- `totalTokenCount`
- optional `cachedContentTokenCount`
- optional `thoughtsTokenCount` from output token details `reasoning_tokens`

## 9. Native OAuth / CLI Channel Variants

### 9.0 OAuth Evidence Ledger

The rows below separate four evidence classes:

- **UAPI source fact**: implemented in this repository.
- **Official/client source fact**: observed in official or client source trees under `upstream`.
- **Competitor fact**: observed in competitor/proxy implementations or their repository docs.
- **Inference / still needs verification**: plausible behavior that is not proven by UAPI or official client source.

| Channel | UAPI source fact | Official/client source fact | Competitor fact | Inference / still needs verification |
|---|---|---|---|---|
| Codex | UAPI maps `openai` + `api_format=codex` to `FormatCodexResponses` in `internal/relay/handler.go:435-445`; Codex requests use ChatGPT backend when OpenAI platform base is configured in `internal/relay/provider/openai/adaptor.go:32-43`; headers `originator`, `User-Agent`, optional `ChatGPT-Account-ID`, and `X-OpenAI-Fedramp` are set in `internal/relay/provider/openai/adaptor.go:56-70`; OAuth constants are in `internal/relay/provider/openai/auth.go:20-34`, auth URL params in `auth.go:104-119`. | Codex source uses `openid profile email offline_access api.connectors.read api.connectors.invoke` and `originator` in `upstream/codex/codex-rs/login/src/server.rs:497-508`; official client has ChatGPT backend Codex Responses URLs in `upstream/codex/codex-rs/codex-client/src/chatgpt_cloudflare_cookies.rs:130-204`; Responses clients use `/responses` in `upstream/codex/codex-rs/codex-api/tests/clients.rs:255-271`. | Not needed for core Codex conclusions because official/client source is present. | Whether every current Codex release still uses the same client id and all headers should be rechecked when upgrading UAPI constants. |
| Claude Code | UAPI maps `anthropic` + `api_format=claude_code` to `FormatClaudeCode` in `internal/relay/handler.go:446-451`; OAuth token accounts send bearer auth, `anthropic-beta: oauth-2025-04-20`, `x-app: cli`, Claude CLI UA, and process session id in `internal/relay/provider/anthropic/adaptor.go:38-51`; OAuth constants/scopes/profile URLs are in `internal/relay/provider/anthropic/auth.go:23-38`. | Claude Code source exposes `claude-code/${VERSION}` UA in `upstream/claude-code-source-1/src/utils/userAgent.ts:9`; OAuth beta and scopes including `user:sessions:claude_code` are in `upstream/claude-code-source-2/src/constants/oauth.ts:36-89`. | Not needed for base Claude Code conclusions. | UAPI does not implement Bifrost's Claude Code raw passthrough gate. Future parity must verify exact current Claude Code request headers and safe header passthrough behavior against the current client release. |
| Gemini CLI / Code Assist | UAPI maps `gemini` + `api_format=gemini_code` to `FormatGeminiCode` in `internal/relay/handler.go:452-457`; Code Assist URL/header handling is in `internal/relay/provider/gemini/adaptor.go:36-61`; OAuth/API-key branch is in `adaptor.go:73-80`; Gemini CLI envelope parser/emitter is registered through native protocols in `internal/relay/provider/convert/native_protocols.go:62-86`, `183-199`. | Gemini CLI source/test data show `generateContentStream` envelope events in `upstream/gemini-cli/packages/sdk/test-data/tool-success.json:1`; Gemini CLI core calls `generateContentStream` in `upstream/gemini-cli/packages/core/src/core/geminiChat.ts:853`. | Not needed for standard Gemini CLI envelope existence, but competitors are useful for v1internal edge cases. | UAPI's `gemini_code` is specifically Code Assist/v1internal transport, not a byte-for-byte Gemini CLI local process protocol. Exact current Code Assist headers and user-agent version must be revalidated when bumping constants. |
| Antigravity | UAPI maps channel type `antigravity` to `FormatAntigravity` in `internal/relay/handler.go:458-460`; URL and headers are in `internal/relay/provider/antigravity/adaptor.go:95-115`; v1internal envelope and model routing/schema sanitation are in `adaptor.go:117-164`; usage parsing is tested by `internal/relay/provider/antigravity/auth_test.go:18-36`. | No official Antigravity client source is present in this repository. | Antigravity-Manager docs identify v1internal limitations and fixes: googleSearch/functionDeclarations conflict in `upstream/Antigravity-Manager/README.md:492-493`, `536-541`; thinkingLevel to thinkingBudget mapping in `README.md:616-619`; dynamic UA/version spoofing in `README.md:625-627`, `682`. CLIProxyAPI config documents antigravity channel/protocol knobs in `upstream/CLIProxyAPI/config.example.yaml:132-151`, `333-395`. | Treat Antigravity non-UAPI behavior as competitor-informed until verified against real Antigravity traffic. Current UAPI assumptions about `requestType`, `sessionId`, model tier routing, and schema sanitation should be covered by integration tests and periodically revalidated. |

OAuth repair rules:

1. Do not collapse these channels into standard API headers. Each channel has distinct URL/header/envelope rules.
2. When a conclusion is copied from Antigravity-Manager, CLIProxyAPI, or cockpit-tools, label it as competitor fact in this document and in any follow-up issue.
3. When a field is only inferred from failures or competitor notes, write a test that captures UAPI's chosen behavior and mark external compatibility as still needing live verification.

### 9.1 Codex

Channel:

- `type=openai`
- `api_format=codex`
- upstream format `codex`

Protocol family:

- Request/response body shape is OpenAI Responses.
- Parser uses OpenAI Responses parser, then changes source protocol to `codex`.
- Emitter uses OpenAI Responses emitter.

URL:

- If configured base is empty, `https://api.openai.com`, or `https://api.openai.com/v1`, adaptor replaces base with `https://chatgpt.com/backend-api/codex`.
- Request URL is `base + "/responses"`.

Headers:

- `Authorization: Bearer <access token>`
- `originator: codex_cli_rs`
- `User-Agent: codex_cli_rs/0.0.0 (<OS> unknown; <arch>) unknown`
- optional `ChatGPT-Account-ID` from account metadata `chatgpt_account_id`
- optional `X-OpenAI-Fedramp: true` from account metadata `chatgpt_account_is_fedramp`
- `Content-Type: application/json` if missing

OAuth:

- Auth URL: `https://auth.openai.com/oauth/authorize`
- Token URL: `https://auth.openai.com/oauth/token`
- default client id: `app_EMoamEEZ73f0CkXaXp7hrann`
- default redirect URI: `http://localhost:1455/auth/callback`
- scope: `openid profile email offline_access api.connectors.read api.connectors.invoke`
- PKCE S256 is used.
- Auth query includes:
  - `id_token_add_organizations=true`
  - `codex_cli_simplified_flow=true`
  - `originator=codex_cli_rs`
- Device auth endpoints:
  - user code: `https://auth.openai.com/api/accounts/deviceauth/usercode`
  - token: `https://auth.openai.com/api/accounts/deviceauth/token`
  - auth page: `https://auth.openai.com/codex/device`
  - redirect: `https://auth.openai.com/deviceauth/callback`
- Refresh token request includes `originator` and Codex user-agent headers.

UAPI-specific behavior:

- `api_format=codex` forces upstream stream when client did not request stream.
- Force-stream buffers upstream SSE, converts to OpenAI Chat non-stream first, then converts to downstream format if needed.
- Codex response/request conversion is currently OpenAI Responses-compatible, not a separate Codex-native schema beyond headers/base URL/normalization.

**Stream Converter Analysis**:

- Codex upstream uses standard OpenAI Responses streaming format (`/v1/responses`), confirmed in `upstream/codex/codex-rs/responses-api-proxy/README.md`: `wire_api='responses'`.
- `FormatOpenAIResponses` and `FormatCodexResponses` share the same stream parser/emitter (`newResponsesIRParser`/`newResponsesIREmitter`) in `internal/relay/provider/stream/ir_openai.go:1194-1197`.
- However, `stream.NewConverter` only checks `upstream != client`, not whether they share the same parser/emitter, resulting in unnecessary IR conversion.
- **Force-stream path requirement**: When client sends non-streaming request to Codex channel, `handleForceStream` executes `Codex Responses → OpenAI Chat SSE → non-stream JSON → clientFormat` conversion chain.
  - First conversion (`handler.go:767`): `newStreamConverterFunc(upstreamFormat, provider.FormatOpenAIChatCompletions)`.
  - This conversion is unnecessary because Codex Responses and OpenAI Responses formats are identical.
- **Specification requirement**: Codex's Responses streaming interface is 100% compatible with OpenAI Responses. The conversion chain MUST be optimized to either pass-through directly or perform only OpenAI Responses → clientFormat conversion.

### 9.2 Claude Code

Channel:

- `type=anthropic`
- `api_format=claude_code`
- upstream format `claude_code`

Protocol family:

- Request/response body shape is Anthropic Messages.
- Parser uses Anthropic parser, then changes source protocol to `claude_code`.
- Emitter uses Anthropic emitter.
- Anthropic and Claude Code are treated as same native family for request raw preservation.

URL:

- Always `base + "/messages"` from account/channel endpoint.

API-key headers:

- `x-api-key: <key>`
- `anthropic-version: 2023-06-01`
- `Content-Type: application/json`

OAuth headers:

- `Authorization: Bearer <access token>`
- `anthropic-beta: oauth-2025-04-20`
- `x-app: cli`
- `User-Agent: claude-cli/2.1.156 (external, cli)`
- `X-Claude-Code-Session-Id: <uuid generated at process start>`
- `anthropic-version: 2023-06-01`
- `Content-Type: application/json`

OAuth:

- Auth URL: `https://claude.com/cai/oauth/authorize`
- Token URL: `https://platform.claude.com/v1/oauth/token`
- default client id: `9d1c250a-e61b-44d9-88ed-5944d1962f5e`
- redirect URI: `https://platform.claude.com/oauth/code/callback`
- scope:
  - `org:create_api_key`
  - `user:profile`
  - `user:inference`
  - `user:sessions:claude_code`
  - `user:mcp_servers`
  - `user:file_upload`
- Auth URL includes `code=true`, OAuth code flow, PKCE S256, and state.
- Exchange code request is JSON, not form encoded.
- Metadata fetch endpoints:
  - profile: `https://api.anthropic.com/api/oauth/profile`
  - roles: `https://api.anthropic.com/api/oauth/claude_cli/roles`
  - first token date: `https://api.anthropic.com/api/organization/claude_code_first_token_date`
  - usage: `https://api.anthropic.com/api/oauth/usage`

UAPI-specific boundary:

> **Design clarification**: UAPI's Claude Code handling uses "native family preservation" design, NOT "unimplemented Bifrost passthrough gate".
>
> - UAPI treats `claude_code` as Anthropic Messages native family: see `sameNativeRequestFamily` logic at `internal/relay/provider/convert/registry.go:161-188`.
> - This means: Claude Code requests use standard Anthropic emitter/header behavior, preserving more native blocks than cross-protocol would allow.
> - **Different from Bifrost**: Bifrost's Claude Code path (`checkAnthropicPassthrough` at `anthropic.go:311-373`) is a gated passthrough that still sanitizes the raw body. UAPI does NOT replicate this — it normalizes Claude Code through the IR like any other protocol.
> - This is an architectural choice, NOT a missing feature.

- For reference: Bifrost's Claude Code gate checks User-Agent and model, then sets `UseRawRequestBody` but still sanitizes in `requestbuilder.go:82-104`, `174-196`.
- UAPI equivalent would require adding explicit Claude Code request detection, safe header passthrough, and raw-body sanitizers if Bifrost parity is desired.
- Current UAPI behavior is: `anthropic` + `api_format=claude_code` → `FormatClaudeCode` → parse/emit through Anthropic family.

**Stream Converter Analysis**:

- `FormatAnthropic` and `FormatClaudeCode` share the same stream parser/emitter (`newAnthropicIRParser`/`newAnthropicIREmitter`) in `internal/relay/provider/stream/ir_anthropic_gemini.go:690-695`.
- This means when `clientFormat=Anthropic, upstreamFormat=ClaudeCode`, an unnecessary IR conversion is created.
- However, because they use the same parser/emitter, the actual conversion is pass-through with no data loss.
- **Specification requirement**: Claude Code's streaming interface is 100% compatible with standard Anthropic Messages. No special handling required beyond current implementation.

### 9.3 Gemini Code Assist / Gemini CLI

Channel:

- `type=gemini`
- `api_format=gemini_code`
- upstream format `gemini_code`

Protocol family:

- Body envelope is Gemini CLI / Code Assist style.
- Parser for `gemini_code` uses Gemini CLI envelope parser and changes source protocol to `gemini_code`.
- Standard Gemini inner request remains GenAI `contents/generationConfig/tools/toolConfig`.

Standard Gemini OAuth/API-key behavior:

- API-key channel:
  - URL query `key=<api key>`
  - no Authorization header.
- OAuth channel:
  - `Authorization: Bearer <access token>`
  - no `key` query parameter.

Standard Gemini URL:

- non-stream: `base + "/models/" + model + ":generateContent"`
- stream: `base + "/models/" + model + ":streamGenerateContent?alt=sse"`

Gemini Code Assist URL:

- non-stream: `https://cloudcode-pa.googleapis.com/v1internal:generateContent`
- stream: `https://cloudcode-pa.googleapis.com/v1internal:streamGenerateContent?alt=sse`
- If configured endpoint is not empty and not a generativelanguage URL, it is used as Code Assist base.

Gemini Code Assist headers:

- `Content-Type: application/json`
- `Authorization: Bearer <access token>`
- `User-Agent: GeminiCLI/0.44.0-nightly.20260512.g022e8baef/<model> (<goos>; <arch>; cli)`

OAuth:

- Auth URL: `https://accounts.google.com/o/oauth2/v2/auth`
- Token URL: `https://oauth2.googleapis.com/token`
- default client id: `681255809395-oo8ft2oprdrnp9e3aqf6av3hmdib135j.apps.googleusercontent.com`
- default client secret: `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`
- redirect URI: `http://127.0.0.1:1456/oauth2callback`
- scope:
  - `https://www.googleapis.com/auth/cloud-platform`
  - `https://www.googleapis.com/auth/userinfo.email`
  - `https://www.googleapis.com/auth/userinfo.profile`
- Auth URL uses `access_type=offline`.
- PKCE is included only if a code challenge is provided.

Code Assist request envelope:

- `model`: resolved Code Assist model
- `user_prompt_id`: random 16-byte hex
- `request`: Gemini inner request
- optional `project`: from account metadata project id
- optional `enabled_credit_types:["GOOGLE_ONE_AI"]` when account paid tier has enough Google One AI credits and model is eligible

Model aliases:

- empty, `auto`, `auto-gemini-2.5`, `pro` -> `gemini-2.5-pro`
- `flash` -> `gemini-2.5-flash`
- `flash-lite` -> `gemini-2.5-flash-lite`
- `auto-gemini-3` -> `gemini-3-pro-preview`
- otherwise unchanged

Session:

- inner request uses `session_id`, not `sessionId`.
- session id priority:
  - request metadata/native `session_id`
  - request metadata/native `sessionId`
  - account metadata `session_id`
  - account metadata `sessionId`
  - deterministic `uapi-<sha256 first 8 bytes>` from account/request seed
  - random `uapi-<hex>` fallback

Metadata/setup:

- Code Assist metadata setup calls `loadCodeAssist`.
- If account is not onboarded, UAPI selects default tier and calls onboard flow.
- Validation-required state is stored in metadata when Code Assist requires validation.

### 9.4 Antigravity

Channel:

- `type=antigravity`
- upstream format `antigravity`

Protocol family:

- Native body is a v1internal Cloud Code style envelope around Gemini inner request.
- Parser uses Gemini CLI parser and changes protocol to `antigravity`.
- Emitter starts from Gemini IR output and wraps it with Antigravity-specific fields.

**Stream Converter Analysis**:

- `FormatGemini`, `FormatGeminiCode`, `FormatGeminiCLI`, and `FormatAntigravity` share the same stream parser/emitter (`newGeminiIRParser`/`newGeminiIREmitter`) in `internal/relay/provider/stream/ir_anthropic_gemini.go:692-701`.
- This means streaming conversions between these formats are effectively pass-through with no data loss.
- **Potential difference**: Although they share the same parser/emitter, the request/response conversion layer (`native_protocols.go`) has special handling for Antigravity (v1internal envelope wrapping).
- **Specification requirement**: Gemini/CLI/Code Assist/Antigravity family streaming interfaces are compatible at IR layer, but request/response conversion requires special envelope handling.

URL:

- default base: `https://cloudcode-pa.googleapis.com`
- version: `v1internal`
- non-stream: `<base>/v1internal:generateContent`
- stream: `<base>/v1internal:streamGenerateContent?alt=sse`
- daily endpoint constant exists: `https://daily-cloudcode-pa.googleapis.com`

Headers:

- `Content-Type: application/json`
- `Authorization: Bearer <access token>`
- `User-Agent: antigravity/<latest-or-fallback-version> darwin/arm64`
- missing access token is a hard error.

OAuth:

- Auth URL: `https://accounts.google.com/o/oauth2/v2/auth`
- Token URL: `https://oauth2.googleapis.com/token`
- default client id: `1071006060591-tmhssin2h21lcre235vtolojh4g403ep.apps.googleusercontent.com`
- default client secret: `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`
- redirect URI: `http://localhost:51121/oauth-callback`
- scope:
  - `openid`
  - `https://www.googleapis.com/auth/cloud-platform`
  - `https://www.googleapis.com/auth/userinfo.email`
  - `https://www.googleapis.com/auth/userinfo.profile`
  - `https://www.googleapis.com/auth/cclog`
  - `https://www.googleapis.com/auth/experimentsandconfigs`
- Auth URL includes:
  - `access_type=offline`
  - `prompt=consent`
  - `include_granted_scopes=true`
- Token exchange and refresh are form encoded.
- Exchange request uses native OAuth user-agent.

Version:

- UAPI periodically fetches latest version from `https://antigravity-auto-updater-974169037036.us-central1.run.app/releases`.
- Fallback version is `2.0.1`.
- Version cache TTL is 6 hours.

Account metadata:

- Fetches Google userinfo.
- Calls Code Assist load/onboard flow.
- Stores `oauth_provider:"antigravity"`, `setup_status`, `email`, `load_code_assist`, and `project_id` when available.

Request envelope:

- `model`: selected upstream model
- `userAgent:"antigravity"`
- `requestType`: `image_gen` if model contains `image`, otherwise `agent`
- `requestId:"agent-" + uuid`
- `request`: Gemini inner request
- optional `project` from account metadata

Antigravity model routing:

- Channel settings:
  - `thinking_routing`
  - `tier_fallback`
  - `medium_token_threshold`, default 8000
  - `long_token_threshold`, default 32000
  - `tier_groups`
- Request effort is read from:
  - `Generation.Reasoning.effort`
  - `Generation.Thinking.thinkingLevel`
  - `Generation.Thinking.effort`
  - metadata/native `reasoning_effort`
  - metadata/native `reasoning.effort`
- Request size is estimated from instruction/turn content chars plus max tokens.
- Model is resolved by effort and size using Antigravity model catalog and channel tier groups.

Inner request mutation:

- Removes `safetySettings`.
- Adds inner `sessionId` based on stable hash of first user text, or random timestamp fallback.
- Normalizes tools:
  - `function_declarations` renamed to `functionDeclarations`
  - empty-name declarations removed
  - `parametersJsonSchema` renamed to `parameters`
  - schema fields stripped:
    - `$schema`, `$id`, `$defs`, `definitions`, `title`, `default`, `examples`, `format`, `additionalProperties`, `external_web_access`
  - JSON schema types uppercased.
  - root missing type defaults to `OBJECT`.
  - root missing properties defaults to empty object.
  - union type arrays choose first non-null type.
- If tools exist, or if model contains `claude` and toolConfig exists, function calling mode is forced to `VALIDATED` unless already non-auto/non-unspecified.
- For non-Claude models, `generationConfig.maxOutputTokens` is removed.

Response usage:

- Antigravity parses response as standard Gemini response IR.

### 9.4.1 Antigravity Version And Behavior Contract

#### Version fetch behavior

- **Source fact**: UAPI fetches version from `https://antigravity-auto-updater-974169037036.us-central1.run.app/releases` in `internal/relay/provider/antigravity/auth.go:20-39`.
- **Current behavior**: Version is fetched **asynchronously on application startup** and cached for **6 hours** (hardcoded TTL, not configurable). The version is used for User-Agent construction in inference requests.
- **Trigger condition**: The fetch happens once at startup. There is no per-request version check.
- **Fallback behavior**: If the version fetch fails, UAPI falls back to version `2.0.1` (hardcoded constant).
- **Repair/test requirement**: Add integration test to verify:
  1. Version cache expiry after 6 hours triggers refresh
  2. Network failure during fetch falls back to `2.0.1`
  3. Version is correctly embedded in User-Agent header

#### thinkingLevel to thinkingBudget conversion

- **Source fact (standard Gemini)**: Standard Gemini conversion already handles `thinkingLevel` → `thinkingBudget` alias normalization in `internal/relay/provider/convert/gemini.go:566-575`. When `thinkingBudget` exists, `thinkingLevel` is deleted to avoid conflicts. This applies to standard Gemini and Gemini Code Assist paths.

- **Source fact (Antigravity)**: The Antigravity adaptor (`internal/relay/provider/antigravity/adaptor.go`) converts string `thinkingLevel` to numeric nested `generationConfig.thinkingConfig.thinkingBudget` before marshaling the v1internal envelope.

- **Competitor fact for Antigravity**: Antigravity-Manager implements this conversion at `upstream/Antigravity-Manager/src-tauri/src/proxy/mappers/gemini/wrapper.rs:192-213`. The v1internal API does NOT accept `thinkingLevel` (string), only accepts `thinkingBudget` (number).

- **Current UAPI behavior**:
  - Standard Gemini / Gemini Code Assist: `thinkingLevel` is normalized and deleted when `thinkingBudget` exists.
  - Antigravity: UAPI deletes `thinkingLevel` and writes numeric `thinkingBudget` under `generationConfig.thinkingConfig`.

- **Mapping rules for Antigravity**:
  - `thinkingLevel: "NONE"` → `thinkingBudget: 0`
  - `thinkingLevel: "LOW"` → `thinkingBudget: cap/4` (min 4096)
  - `thinkingLevel: "MEDIUM"` → `thinkingBudget: cap/2` (min 8192)
  - `thinkingLevel: "HIGH"` → `thinkingBudget: cap`

- **Test coverage**: `internal/relay/provider/antigravity/auth_test.go` verifies nested `thinkingBudget`, removal of `thinkingLevel`, and explicit zero budget for `NONE`.

#### googleSearch + functionDeclarations conflict

- **Competitor fact**: Antigravity-Manager documents that `googleSearch` tool and `functionDeclarations` cannot coexist in the same request at `README.md:492-493`.
- **Current UAPI behavior**: UAPI detects this conflict in `internal/relay/provider/antigravity/adaptor.go` and returns a conversion error instead of sending the request upstream.
- **Test coverage**: `internal/relay/provider/antigravity/auth_test.go` verifies conflict detection helpers for a request containing both tool types.

#### Request fingerprint headers (competitor fact, not implemented)

- **Competitor fact**: Antigravity-Manager documents dynamic headers including `X-Client-Name`, `X-Client-Version`, `X-Machine-Id`, `X-VSCode-SessionId` at `README.md:682-684`.
- **Current UAPI behavior**: **NOT IMPLEMENTED**. UAPI only sends `Content-Type`, bearer `Authorization`, and generic `User-Agent: antigravity/<version>`.
- **Status**: These are competitor-derived behaviors, NOT requirements. Do NOT implement unless explicitly requested by product decision.

## 10. Streaming Conversion

### 10.1 Relay streaming contract

For true streaming:

- Upstream request is sent with fasthttp streaming response body.
- Downstream response headers:
  - `Content-Type: text/event-stream`
  - `Cache-Control: no-cache`
  - `Connection: keep-alive`
  - `X-Accel-Buffering: no`
- UAPI reads SSE frames using `SSEStreamReader`.
- `newStreamConverterFunc(upstreamFormat, clientFormat)` returns a converter only when formats differ, formats are not in the same wire-compatible streaming family, and parser/emitter exist.
- UAPI appends `[DONE]` only when downstream `clientFormat == openai_chat`, **except** for force-stream paths (see below).
- **Force-stream path behavior** (Codex forced-stream, or channel with `ForceStream=true`):
  - `handleForceStream` at `internal/relay/handler.go:691-803` forces upstream response through OpenAI Chat SSE intermediate format first (line 779-781: `newStreamConverterFunc(upstreamFormat, provider.FormatOpenAIChatCompletions)`).
  - `convertSSEBufferWithConverter` at `internal/relay/handler.go:1266-1302` unconditionally appends `[DONE]` if not already present (line 1299-1301).
  - Then `StreamToNonStreamChecked` at `internal/relay/stream_converter.go:29-270` converts the buffered SSE to non-stream JSON. The `[DONE]` marker is consumed to detect stream completion, but is NOT included in the final JSON output.
  - Finally, `provider.ConvertResponse` converts the OpenAI Chat JSON to the final client format if needed.
  - **Result**: Force-stream paths DO append `[DONE]` in the intermediate SSE buffer stage, but the final non-stream JSON output does NOT contain `[DONE]`. The `[DONE]` is used only for completion detection, not as SSE termination in the final response.
- Usage is tracked from chunks and cache-token extraction.
- Stream is considered successful only if a terminal/finalized event is observed.

### 10.2 Stream parsers

OpenAI Chat parser:

- Reads `data: ...` lines.
- Ignores `[DONE]`.
- Parses `id`, `created`, `model`, `choices[].delta`, `finish_reason`, `usage`.
- Emits:
  - response created when id first appears
  - message start on role delta
  - content deltas
  - reasoning deltas from `reasoning_content` and `reasoning_details`
  - tool call start when name arrives with no args
  - tool arg delta when arguments arrive
  - response done on finish reason
- If stream ends without finish but id exists, `Done()` emits stop.

OpenAI Responses parser:

- Handles event `type` fields:
  - `response.created`
  - `response.output_text.delta`
  - `response.output_text.done`
  - `response.content_part.done`
  - `response.reasoning.delta`
  - `response.reasoning_text.delta`
  - `response.reasoning_summary_text.delta`
  - `response.reasoning.done`
  - `response.reasoning_text.done`
  - `response.reasoning_summary_text.done`
  - `response.output_item.added`
  - `response.output_item.done`
  - `response.function_call_arguments.delta`
  - other function-call/done paths implemented in the parser
- Maintains tool argument accumulation and tool call metadata.

Anthropic parser:

- Handles:
  - `message_start`
  - `content_block_start`
  - `content_block_delta`
  - `message_delta`
  - `message_stop`
- Content block start records block index/type/call id/name.
- `tool_use` starts emit tool call start.
- `redacted_thinking` emits encrypted reasoning.
- delta text emits content.
- delta thinking/signature emits reasoning.
- delta partial_json emits tool argument delta.
- `message_delta` emits response done and usage/cache tokens.

Gemini parser:

- Accepts raw Gemini SSE body, `{response:...}` wrapper, and `{"method":"generateContentStream","params":...}` envelope.
- Emits response created/message start when first candidate appears.
- Text with `thought:true` emits reasoning.
- Text emits content.
- `thoughtSignature` emits encrypted reasoning.
- `functionCall` emits tool call start and argument delta.
- `functionResponse` currently emits textual content from response.
- `executableCode` emits fenced code text.
- `codeExecutionResult` emits result output text.
- Terminal finish is any finish reason except empty, `NOT_STARTED`, and `SPECIFIED`.

### 10.3 Stream emitters

Stream emitters project IR events into target protocol SSE frames. They are intentionally lossy for protocol concepts that have no streaming equivalent.

Important repair rule:

- Streaming and non-streaming conversion must share the same semantic mappings for text, tool calls, reasoning, usage, cache tokens, and finish reasons.
- If a non-stream converter is fixed, the equivalent stream parser/emitter should be checked in the same change.

## 11. Error Handling

> **Design Decision**: UAPI keeps relay-originated errors simple, but upstream HTTP error bodies are normalized to the downstream client protocol family. UAPI does not implement Bifrost's full route-level `ErrorConverter` matrix; it uses a compact relay normalizer for upstream error statuses.

Current UAPI error behavior:

- Request JSON parse failure -> 400 `{"error":"invalid request body"}`
- Missing model -> 400 `{"error":"model is required"}`
- Gateway model mismatch -> 401 `{"error":"gateway model mismatch"}`
- Token/auth errors -> 401 generic relay JSON errors
- Model/permission/IP errors -> 403 generic relay JSON errors
- No route/channel/account -> 404 generic relay JSON errors
- Unsupported route -> 400 `{"error":"unsupported route"}`
- Unsupported selected channel request type -> 400 `{"error":"request type not supported by selected channel"}`
- Request conversion failure -> 400 with `convert request failed: ...`
- Same-protocol parse/normalize failure -> 400 with `normalize request failed: ...`
- Upstream transport failure -> 502 generic relay JSON error
- Upstream HTTP error status triggers refund/account refresh logic, extracts a sanitized message, and normalizes the response body by downstream client format:
  - OpenAI Chat / OpenAI Responses / Codex Responses -> `{"error":{"message":"...","type":"relay_error"}}`
  - Anthropic / Claude Code -> `{"type":"error","error":{"type":"api_error","message":"..."}}`
  - Gemini / Gemini Code / Gemini CLI / Antigravity -> `{"error":{"code":HTTP_STATUS,"message":"...","status":"..."}}`
- 413 upstream errors whose provider body has only a generic message are rewritten to `upstream returned HTTP 413: request body too large`.
- Response conversion failure -> 502 generic relay JSON error
- Stream ended without terminal event -> 502
- Client disconnect in stream -> usage recorded with status 499 when possible

**Source facts**:

- Unsupported route: `internal/relay/handler.go:251-254` returns generic 400 before auth.
- Parse errors: `internal/relay/handler.go:368-371` returns generic 400.
- Same-protocol normalize errors: `handler.go:548-551` returns 400 with `normalize request failed`.
- Conversion errors: `handler.go:554-558` returns 400 with `convert request failed`.
- Streaming upstream error statuses: `handler.go:620-634` calls `refundOnError`.
- Force-stream upstream error statuses and converted stream error chunks: `handler.go:776-783`, `handler.go:812-820` call `refundOnError` or a 502 relay error.
- Buffered upstream error statuses: `handler.go:1149-1151` calls `refundOnError`.
- Upstream error body normalization: `handler.go:2055-2068` chooses OpenAI, Anthropic, or Gemini-family error shape from `clientFormat`.
- Error message extraction and sanitization: `handler.go:2071-2120`.
- Normalized OpenAI/Anthropic/Gemini error emitters: `handler.go:2123-2153`.
- Refund + response write path: `handler.go:2179-2185`.

**Comparison with Bifrost** (for reference, not a requirement to clone exactly):

- Bifrost route configs require an `ErrorConverter`: `upstream/bifrost/transports/bifrost-http/integrations/router.go:510`, `router.go:615-616`.
- Bifrost Anthropic routes call `ToAnthropicChatCompletionError`: `upstream/bifrost/transports/bifrost-http/integrations/anthropic.go:127-129`; implementation is `upstream/bifrost/core/providers/anthropic/errors.go:11-29`.
- Bifrost Gemini routes call `ToGeminiError`: `upstream/bifrost/transports/bifrost-http/integrations/genai.go:261-262`.
- UAPI's behavior is narrower: relay-originated errors remain generic; upstream error statuses are normalized by client family, not by every Bifrost route type.

## 12. Async, Large Payload, Files, Batch, And Unsupported Boundaries

Current UAPI conversion core does not implement Bifrost-equivalent support for:

- `x-bf-async`
- `x-bf-async-id`
- `x-bf-async-job-result-ttl`
- async create/retrieve converters
- Bifrost large-payload hook that avoids body materialization and records metadata
- `x-bf-passthrough-extra-params`
- Count tokens routes as conversion-layer first-class endpoints
- OpenAI Responses input tokens endpoint
- Anthropic Messages count tokens endpoint
- Gemini `:countTokens`
- Anthropic files/batches
- Gemini files/cached contents/batch/operations
- OpenAI containers/files/batches/audio/images within the conversion IR, except separate media passthrough/special handling

Current large body behavior:

- Relay request bodies are materialized through `ctx.PostBody()` before request classification/conversion.
- UAPI has no Bifrost-style `LargePayloadHook`, `LargeResponseHook`, large payload context metadata, or large payload request reader path in the conversion layer.
- UAPI skips `cleanJSONUndefinedPlaceholders` above configurable `largePayloadBytes` (`largePayloadThresholdBytesDefault = 256MB`, runtime override via `SetLargePayloadThreshold`). Requests at or below that threshold may still be JSON parsed and re-marshaled by cleanup before protocol conversion.
- Buffered upstream responses are capped by `maxResponseSize = 100 * 1024 * 1024`; force-stream buffering reads one byte past that cap and returns 502 `upstream response too large` when exceeded.
- Streaming upstream error bodies are read with `io.LimitReader(stream, maxResponseSize)`.
- Upstream HTTP 413 bodies are normalized by client protocol family and generic provider messages are rewritten to `upstream returned HTTP 413: request body too large`.

Bifrost large payload standard approach:

- Bifrost uses a 10MB threshold (`DefaultLargePayloadRequestThresholdBytes = 10 * 1024 * 1024`) at `upstream/bifrost/core/schemas/bifrost.go:338-340`.
- When request body exceeds threshold, Bifrost enters "large payload mode" and uses streaming body reader instead of materializing the full body.
- Large payload mode preserves the original request body encoding without JSON parse→re-serialize cycle.
- Bifrost context keys for large payload: `BifrostContextKeyLargePayloadMode`, `BifrostContextKeyLargePayloadReader`, `BifrostContextKeyLargePayloadContentLength`, `BifrostContextKeyLargePayloadContentType`, `BifrostContextKeyLargePayloadMetadata`.
- Provider utilities apply large payload body via `ApplyLargePayloadRequestBodyWithModelNormalization` at `upstream/bifrost/core/providers/utils/utils.go:700-740`, which uses `req.SetBodyStream()` instead of `req.SetBody()`.
- Middleware at `upstream/bifrost/transports/bifrost-http/handlers/middlewares.go:557-576` skips body copy when large payload mode is active.

Implemented repair:

- UAPI now avoids the JSON parse→re-serialize cleanup cycle for requests exceeding the configured large-payload threshold.
- Remaining gap: UAPI still materializes the request body through fasthttp and does not implement Bifrost-style large payload streaming reader/hooks.

Unsupported route contract:

- `/v1/async/*` must return 400 `{"error":"unsupported route"}`.
- `/v1/responses/*` is unsupported except `/v1/responses` and `/v1/responses/`.
- `/v1/messages/*` is unsupported except `/v1/messages` and `/v1/messages/`.
- `/v1/files*`, `/v1/containers*`, and `/v1/batches*` must return unsupported route.
- Gemini unsupported resources: `/upload/v1beta/files*`, `/v1beta/files*`, `/v1beta/cachedContents*`, `/v1beta/batches*`, `/v1beta/operations*`.
- Gemini unsupported actions: `:countTokens`, `:embedContent`, `:batchEmbedContents`, `:predict`, `:predictLongRunning`, `:batchGenerateContent`.
- OpenAI media endpoints (`/v1/images/*`, `/v1/audio/*`, `/v1/videos*`, `/v1/embeddings`, `/v1/moderations`, `/v1/realtime/*`) are not part of the text conversion IR. They use separate media passthrough/special handling and are gated by `supportsRelayRequestType`.

Source facts:

- Bifrost async headers: `upstream/bifrost/core/schemas/async.go:16-21`.
- Bifrost async routes and retrieval: `upstream/bifrost/transports/bifrost-http/handlers/asyncinference.go:65-90`, `492-520`.
- Bifrost large payload threshold constant: `upstream/bifrost/core/schemas/bifrost.go:338-340` (`DefaultLargePayloadRequestThresholdBytes = 10MB`).
- Bifrost large payload middleware skip: `upstream/bifrost/transports/bifrost-http/handlers/middlewares.go:557-576`.
- Bifrost large payload body streaming: `upstream/bifrost/core/providers/utils/utils.go:675-740` (`ApplyLargePayloadRequestBodyWithModelNormalization`).
- Bifrost large payload context keys: defined in `upstream/bifrost/core/schemas/bifrost.go` (search for `BifrostContextKeyLargePayload*`).
- Bifrost large payload router hooks: `upstream/bifrost/transports/bifrost-http/integrations/router.go:523-560`.
- Bifrost route type split for batch/file/container/cached content: `upstream/bifrost/transports/bifrost-http/integrations/router.go:601-607`, `823-864`, `2288-2406`.
- Bifrost Gemini batch/file/cached content/large-payload examples: `upstream/bifrost/transports/bifrost-http/integrations/genai.go:177-190`, `420-430`, `931-990`, `1236-1248`, `1420-1426`.
- UAPI large-payload threshold config: `internal/relay/handler.go:102-140`, `internal/config/config.go:141-145`, `internal/server/server.go:61-64`.
- UAPI conditional JSON cleanup: `internal/relay/handler.go:351-360`.
- UAPI JSON cleanup function: `internal/relay/handler.go:2201-2215` (`cleanJSONUndefinedPlaceholders` parses JSON, removes `[undefined]` sentinels, then re-marshals when cleanup changes the body).
- UAPI unsupported route detection: `internal/relay/request_type.go:32-53`, `80-99`.
- UAPI early unsupported response: `internal/relay/handler.go:227-231`.
- UAPI request body materialization: `internal/relay/handler.go:351`.
- UAPI media special handling: `internal/relay/handler.go:509-512`, `831-925`.
- UAPI response/error size limits: `internal/relay/handler.go:45-46`, `755-763`, `1253-1263`.
- Server request body cap is derived from `server.max_body_size_mb`: `internal/server/server.go:106-108`.
- Streaming SSE scanner line/event buffer max is 10 MB: `internal/relay/streaming.go:16-17`, `streaming.go:279-280`; WS bridge uses the same 10 MB cap at `internal/relay/ws_bridge.go:157-158`.

## 13. Provider Capability And Sanitization Gaps Relative To Bifrost

UAPI currently has narrower sanitizer/capability logic than Bifrost.

Implemented or partially implemented:

- Same-protocol raw-ish forwarding preserves native provider fields while removing `[undefined]` sentinels.
- Cross-protocol native field loss recording records unsupported provider-specific fields rather than silently dropping all evidence.
- OpenAI Responses `input_file` emission removes unsupported source document metadata such as `file_type`, `mime_type`, and `title`, while preserving usable file data and filename.
- OpenAI Responses `input_file` emission defaults a filename when only `file_data` is available.
- Anthropic forced tool choice suppresses `thinking` / `output_config.effort`.
- Anthropic web search tools are projected from OpenAI-style web search tools.
- Gemini max output token cap is 65536.
- Gemini `response_format` is mapped to `generationConfig.responseMimeType` / `responseSchema`.
- Gemini `thinkingConfig` snake_case aliases are normalized and `thinkingBudget` wins over `thinkingLevel`.
- Gemini `topK`, `safetySettings`, and `systemInstruction` have explicit IR paths where possible.
- Antigravity has schema sanitization, nested `generationConfig.thinkingConfig.thinkingBudget`, and googleSearch/functionDeclarations conflict validation.
- Codex uses native headers/base URL handling and forced-stream normalization through the relay path.

Still narrower than Bifrost:

- UAPI does not honor `x-bf-passthrough-extra-params`; unknown top-level fields are preserved as native same-protocol fields or recorded as cross-protocol loss, not forwarded through Bifrost-style `ExtraParams`.
- UAPI does not enforce Bifrost's model-specific Gemini thinking budget range validation; it normalizes aliases and caps `maxOutputTokens`, but does not reject out-of-range explicit thinking budgets.
- UAPI does not implement Bifrost's full Anthropic model/version matrix for adaptive thinking, native effort, Opus-specific `top_k` stripping, or Responses structured-output synthetic tool handling.
- UAPI does not implement Bifrost's full URL sanitization/fetch policy for image/video URLs; text conversion primarily preserves/rewrites inline/file parts and records loss.
- UAPI does not implement Bifrost's full unsupported-tool-call-to-text fallback matrix; only the converter-local tool/result projections are implemented.
- UAPI does not implement Bifrost provider families beyond the configured OpenAI/Anthropic/Gemini/native CLI channel formats.

Source facts:

- Bifrost passthrough extra params header: `upstream/bifrost/transports/bifrost-http/lib/ctx.go:537-541`.
- Bifrost Gemini thinking support/range validation and response format mapping: `upstream/bifrost/core/providers/gemini/utils.go:210-240`, `1172-1228`, `1229-1248`, `2673-2688`.
- Bifrost Gemini image URL sanitization: `upstream/bifrost/core/providers/gemini/utils.go:2001-2008`.
- Bifrost Anthropic structured-output, thinking, `top_k`, unsupported tool, and web-search sanitization logic: `upstream/bifrost/core/providers/anthropic/responses.go:2361-2374`, `2415-2445`, `2516-2521`, `3364-3370`, `4344-4351`.
- UAPI Anthropic forced tool choice thinking suppression: `internal/relay/provider/convert/anthropic.go:207-213`, `344-362`.
- UAPI Anthropic web search projection: `internal/relay/provider/convert/anthropic.go:389-462`.
- UAPI Gemini max output token cap, response format, thinking alias normalization, tool projection, topK/safety/systemInstruction: `internal/relay/provider/convert/gemini.go:223-266`, `558-590`, `610-627`, `707-742`.
- UAPI file/default filename sanitization tests: `internal/relay/provider/convert/protocol_redesign_test.go:1396-1412`, `1529-1542`; emitter default filename logic at `internal/relay/provider/convert/openai_responses.go:522-528`.
- UAPI Antigravity schema/thinking/function-calling checks: `internal/relay/provider/antigravity/adaptor.go:139-166`, `373-399`, `648-655`, `696-725`; tests in `internal/relay/provider/antigravity/auth_test.go:142-231`.
- UAPI Codex native header/base URL tests: `internal/relay/provider/oauth_fingerprint_test.go:13-46`, `internal/relay/provider/openai/refresh_test.go:11-22`.

## 14. Converter Function And Test Index

This index is the repair map. Any change to one converter should check its parser, emitter, response converter, stream converter, adaptor, and tests in the same row.

| Format | Request parser/emitter | Response parser/emitter | Stream conversion | Adaptor / URL / headers | Existing tests to extend |
|---|---|---|---|---|---|
| OpenAI Chat Completions | `parseOpenAIChatRequestIR` and `emitOpenAIChatRequestIR` in `internal/relay/provider/convert/internal.go:25-31`; direct implementation and registration in `internal/relay/provider/convert/openai_chat.go:12-180`, `441-443`. | `parseOpenAIChatResponseIR` and `emitOpenAIChatResponseIR` in `internal/relay/provider/convert/internal.go:69-75`; registration in `response_openai.go:700-701`. | OpenAI stream parser/emitter registration in `internal/relay/provider/stream/ir_openai.go:1196-1198`; relay stream conversion uses `stream.NewConverter` at `internal/relay/streaming.go:437`. | OpenAI URL/header logic in `internal/relay/provider/openai/adaptor.go:32-82`. | `internal/relay/provider/convert/protocol_redesign_test.go:34-63`, `459-487`; stream tests in `internal/relay/streaming_test.go:408-483`. |
| OpenAI Responses | `parseOpenAIResponsesRequestIR` and `emitOpenAIResponsesRequestIR` in `internal/relay/provider/convert/internal.go:33-43`; registration in `openai_responses.go:561-562`. | `parseOpenAIResponsesResponseIR` and `emitOpenAIResponsesResponseIR` in `internal/relay/provider/convert/internal.go:77-83`; registration in `response_openai.go:702-703`. | Responses stream parser/emitter registration in `internal/relay/provider/stream/ir_openai.go:1199-1200`; stream tests cover completion, usage, cache, Chat conversion, and errors in `stream_redesign_test.go:101-165`, `437-448`. | OpenAI Responses URL selected by `api_format=responses` in `internal/relay/provider/openai/adaptor.go:40-41`. | `internal/relay/provider/convert/protocol_redesign_test.go:848-871`, `1088-1123`, `1558-1570`; `stream_redesign_test.go:101-165`. |
| Codex | `parseCodexRequest`/`emitCodexRequest` in `internal/relay/provider/convert/native_protocols.go:10-34`; request/response registration in `native_protocols.go:184-187`. | `parseCodexResponse`/`emitCodexResponse` in `native_protocols.go:19-34`. | Codex aliases use Responses stream parser/emitter registration in `internal/relay/provider/stream/ir_openai.go:1201-1202`; alias coverage in `internal/relay/streaming_test.go:498-557`. | Codex backend/headers in `internal/relay/provider/openai/adaptor.go:32-82`; endpoint tests in `internal/relay/provider/openai/channel_contract_test.go:11-49`, `adaptor_test.go:33-52`; OAuth tests in `oauth_fingerprint_test.go:13-46` and `openai/refresh_test.go:11-22`. | `internal/relay/request_type_test.go:82-173`; `internal/relay/provider/convert/native_protocols_test.go:13-44`, `104-132`. |
| Anthropic Messages | `parseAnthropicRequestIR`/`emitAnthropicRequestIR` in `internal/relay/provider/convert/internal.go:45-51`; registration in `anthropic.go:749-750`. | `parseAnthropicResponseIR`/`emitAnthropicResponseIR` in `internal/relay/provider/convert/internal.go:85-91`; registration in `response_anthropic.go:225-226`. | Anthropic stream parser/emitter registration in `internal/relay/provider/stream/ir_anthropic_gemini.go:690-691`; stream tests in `stream_redesign_test.go:137-144`, `200-257`, `360-390`. | Anthropic URL/header/OAuth branch in `internal/relay/provider/anthropic/adaptor.go:33-58`. | `internal/relay/provider/convert/anthropic_test.go:8-505`; `protocol_redesign_test.go:1016-1072`, `1118-1150`, `1385-1439`. |
| Claude Code | `parseClaudeCodeRequest`/`emitClaudeCodeRequest` in `internal/relay/provider/convert/native_protocols.go:36-60`; request/response registration in `native_protocols.go:188-191`. | `parseClaudeCodeResponse`/`emitClaudeCodeResponse` in `native_protocols.go:45-60`. | Claude Code aliases use Anthropic stream parser/emitter registration in `internal/relay/provider/stream/ir_anthropic_gemini.go:694-695`; alias coverage in `internal/relay/streaming_test.go:521-529`. | Claude Code OAuth headers in `internal/relay/provider/anthropic/adaptor.go:38-51`; OAuth fingerprint tests in `internal/relay/provider/oauth_fingerprint_test.go:50-79`. | `internal/relay/provider/convert/native_protocols_test.go:13-44`; `internal/relay/provider/convert/anthropic_test.go:100-249`. |
| Gemini / Google GenAI | `parseGeminiRequestIR`/`emitGeminiRequestIR` in `internal/relay/provider/convert/internal.go:53-59`; direct implementation and registration in `gemini.go:12-72`, `191-266`, `901-903`. | `parseGeminiResponseIR`/`emitGeminiResponseIR` in `internal/relay/provider/convert/internal.go:93-99`; registration in `response_gemini.go:260-261`. | Gemini stream parser/emitter registration in `internal/relay/provider/stream/ir_anthropic_gemini.go:692-693`; raw EOF tests in `internal/relay/streaming_test.go:156-191`; cache tests in `stream_redesign_test.go:149-155`. | Standard Gemini URL/key/OAuth handling in `internal/relay/provider/gemini/adaptor.go:45-90`. | `internal/relay/provider/convert/protocol_matrix_test.go:10-52`; `protocol_redesign_test.go:269-314`, `579-647`, `1442-1499`; `native_protocols_test.go:134-214`. |
| Gemini Code Assist / Gemini CLI envelope | Gemini CLI request parser/emitter registration is in `internal/relay/provider/convert/gemini_cli.go:12-80`; `parseGeminiCodeRequest`/`emitGeminiCodeRequest` wrap it in `internal/relay/provider/convert/native_protocols.go:62-86`; Gemini Code request/response registration in `native_protocols.go:192-195`; base envelope helper in `internal/relay/provider/gemini/codeassist_convert.go`. | Gemini CLI response parser/emitter and registration are in `response_gemini.go:228-263`; `parseGeminiCodeResponse`/`emitGeminiCodeResponse` wrap them in `native_protocols.go:71-86`. | Gemini Code/CLI aliases use Gemini stream parser/emitter registration in `internal/relay/provider/stream/ir_anthropic_gemini.go:696-699`; alias coverage in `internal/relay/streaming_test.go:535-544`. | Code Assist URL/UA in `internal/relay/provider/gemini/adaptor.go:36-61`; account envelope tests in `internal/relay/provider/gemini/codeassist_convert_test.go:11-51`; OAuth tests in `oauth_fingerprint_test.go:87-113`. | `internal/relay/provider/convert/protocol_redesign_test.go:896-933`; `internal/relay/provider/gemini/codeassist_convert_test.go:11-51`. |
| Antigravity | `parseAntigravityRequest`/`emitAntigravityRequest` in `internal/relay/provider/convert/native_protocols.go:88-127`; request/response registration in `native_protocols.go:196-199`; adaptor usually emits through `AntigravityAdaptor.FromIR`. | `parseAntigravityResponse`/`emitAntigravityResponse` in `native_protocols.go:97-127`. | Antigravity alias uses Gemini stream parser/emitter registration in `internal/relay/provider/stream/ir_anthropic_gemini.go:700-701`; alias coverage in `internal/relay/streaming_test.go:542-557`; response usage tests in `internal/relay/provider/antigravity/auth_test.go:21-36`. | URL/header/envelope/model routing/schema sanitation in `internal/relay/provider/antigravity/adaptor.go:95-164`; image special path in `internal/relay/antigravity_images.go`. | `internal/relay/provider/convert/protocol_redesign_test.go:704-807`, `932-989`; `internal/relay/provider/antigravity/auth_test.go:43-123`; `internal/relay/antigravity_images_test.go:13-122`. |

## 15. Raw, Native, Loss, And Unsupported Matrix

| Scenario | Current UAPI behavior | Source fact | Required repair/test detail |
|---|---|---|---|
| Same protocol JSON request | Cleans `"[undefined]"`, validates by parser, returns original body after cleanup. It does not emit a fresh IR body. | `internal/relay/provider/convert/registry.go:31-44`; relay branch at `internal/relay/handler.go:546-554`. | Covered by `TestOpenAIChatSameFormatPreservesExplicitFalseAndNativeFields`, `TestSameFormatGeminiPreservesUnmodeledCodeAssistFields`, and relay production-path tests. |
| Gemini same protocol | Relay does not inject model into body because Gemini standard carries model in URL. | `internal/relay/handler.go:497-510`; Gemini URL built in `internal/relay/provider/gemini/adaptor.go:63-71`; official CLI also hoists REST-only root fields in `upstream/gemini-cli/packages/core/src/utils/apiConversionUtils.ts:24-54`. | Covered by same-format Gemini tests; body must not receive top-level `model` in standard Gemini same-protocol forwarding. |
| Cross protocol top-level native fields | Native fields, metadata, and generation extras are recorded as warning losses and dropped unless same native family. | `internal/relay/provider/convert/registry.go:161-188`. | Covered by protocol matrix/native loss tests; remaining desired coverage is per-protocol-pair loss shape, not a known implementation blocker. |
| Anthropic/Claude Code native family | Anthropic and Claude Code preserve more native request raw because same native request family returns true. | `internal/relay/provider/convert/registry.go:183-188`; Claude Code wrappers in `native_protocols.go:36-60`. | Covered by native protocol preservation tests. |
| Codex/OpenAI Responses | Codex reuses OpenAI Responses body shape but has Codex protocol marker and Codex transport headers/base URL. | `internal/relay/provider/convert/native_protocols.go:10-34`; `internal/relay/provider/openai/adaptor.go:32-70`. | Covered by Codex same-protocol/native identity tests; header/base URL behavior remains adaptor-level, not IR conversion output. |
| Missing tool call name/id | OpenAI Chat emission fails hard for missing tool call name/id. | `internal/relay/provider/convert/openai_chat.go:228-240`. | Covered by `TestOpenAIChatEmitterRejectsMissingToolIdentifiers`. |
| Tool result without call id | OpenAI Chat emission fails hard for missing `tool_call_id`; Anthropic target behavior is target-specific and Bifrost has separate conversion paths for ordinary function outputs, computer outputs, and unsupported tool calls. | UAPI emitter at `internal/relay/provider/convert/openai_chat.go:237-240`; Bifrost ordinary tool-result blocks at `upstream/bifrost/core/providers/anthropic/responses.go:4362-4382`, computer tool outputs at `4671-4690`, and unsupported tool-call text fallback at `3358-3371`, `4645-4666`. | Covered by `TestOpenAIChatEmitterRejectsMissingToolIdentifiers`; other target emitters are target-specific and must keep explicit tests. |
| File/PDF blocks | UAPI content IR can represent file/document/image/audio; cross-protocol emitters may drop file type, mime type, URLs, or default filenames. PDF-compatible Chat target paths emit `image_url`, not Chat `file` blocks. | IR file/image/audio structs in `internal/relay/provider/ir/content.go:101-120`; existing PDF/file conversion tests in `internal/relay/provider/convert/protocol_redesign_test.go:1241-1575`; documented UAPI gaps in sections 5.3, 6.3, 7.3, 8.3. | Existing tests cover Responses/Gemini/Anthropic PDF -> Chat `image_url`, Gemini PDF inlineData -> Responses `input_file`, and Chat file -> Gemini `inlineData`. Remaining risk is broader pairwise coverage for file URL/id, filename defaults, and mime loss records. |
| Gemini `cachedContent` | Parser lifts top-level `cachedContent` into `Request.Cache.CachedContent`; Gemini emission writes it back as top-level `cachedContent`. Same-protocol raw cleanup still preserves the original field. | UAPI IR field at `internal/relay/provider/ir/types.go:61`; parser/emitter at `internal/relay/provider/convert/gemini.go:42-43` and `gemini.go:265-266`; Bifrost maps shared `cached_content` extra into Gemini `CachedContent` at `upstream/bifrost/core/providers/gemini/chat.go:60-62`; Gemini CLI hoists SDK `cachedContent` to REST root at `upstream/gemini-cli/packages/core/src/utils/apiConversionUtils.ts:24-54`. | Covered by `TestGeminiCachedContentMapsThroughIR` and same-format Gemini preservation tests. |
| Reasoning/thinking/signatures | UAPI projects major reasoning shapes across OpenAI/Anthropic/Gemini but does not implement full Bifrost provider gates. | Reasoning item fields in `internal/relay/provider/ir/content.go:122-130`; existing tests in `internal/relay/provider/convert/protocol_redesign_test.go:1103-1228`. | Covered for current IR projections; remaining risk is provider/model-specific sanitizer parity, not base conversion. |
| Provider unsupported capabilities | UAPI has limited sanitizer compared with Bifrost. | Section 13 gap matrix; Bifrost raw Anthropic request builder chains tool stripping, empty-thinking stripping, tool version remapping, region deletion, and unsupported-field stripping at `upstream/bifrost/core/providers/anthropic/requestbuilder.go:158-196`. | Keep as explicit narrower-than-Bifrost contract unless provider/model capability gates are implemented. |
| Unsupported route families | File/container/batch/async/Gemini cache/count/embed/batch/operation routes are rejected before conversion instead of falling through to chat/generate conversion. | Detection in `internal/relay/request_type.go:29-100`; early HTTP 400 in `internal/relay/handler.go:250-254`; Bifrost has first-class GenAI file, batch, count token, and cached content route families in `upstream/bifrost/transports/bifrost-http/integrations/genai.go:128-227`, `420-556`, `591-686`, and `935-1132`. | Covered by route detection/support tests; unsupported body is `{"error":"unsupported route"}`. |

## 16. Error, Header, Status, And Streaming Contract Matrix

| Area | Current UAPI behavior | Source fact | Missing detail / repair target |
|---|---|---|---|
| Request parse errors | Invalid JSON returns HTTP 400 `{"error":"invalid request body"}` before conversion. | UAPI: `internal/relay/handler.go:349-381`; gateway model/body extraction boundary at `internal/gateway/gateway.go:254-267`. Bifrost pre-stream errors are JSON HTTP errors, not SSE events: `upstream/bifrost/transports/bifrost-http/integrations/utils_test.go:72-135`, `185-221`. | Relay-originated parse errors are generic JSON errors, not protocol-shaped IR errors. |
| Missing model | Returns HTTP 400 `{"error":"model is required"}`; Gemini model can be derived from `/v1beta/models/{model}:action` when body model is absent. | Gateway/relay paths: `internal/gateway/gateway.go:264-267`; `internal/relay/handler.go:378-386`; path extraction in `internal/httputil/httputil.go:31-52`. | Covered by `TestModelFromRequestPathExtractsGeminiModel`; full handler integration still relies on relay auth setup. |
| Gateway selected model mismatch | Gateway-authenticated relay rejects if signed model differs from parsed request model. | `internal/relay/handler.go:383-386`. | Keep as relay security invariant; add full tampered internal request integration test if the auth harness is expanded. |
| Unsupported media/request type | Relay checks selected channel type support for non-text media and rejects unsupported conversion routes before conversion. | `internal/relay/request_type.go:29-100`, `122-135`, `167-179`; early unsupported route error in `internal/relay/handler.go:251-254`; selected-channel support error at `handler.go:450-453`; media handler support error at `handler.go:871-874`. | Covered by route detection/support tests. |
| Upstream auth failure | OAuth accounts may refresh on auth-like failures and retry once. | Streaming retry path: `internal/relay/handler.go:620-635`; force-stream/media/buffered retry paths: `handler.go:756-782`, `914-944`, `1067-1076`; refresh and predicates in `handler.go:1720-1778`. | Covered by `oauth_auth_failure_test.go`; predicates are status 401/403 plus auth-like body for OAuth credential types only. |
| Upstream error body | Upstream HTTP errors are refunded/reported and normalized into the downstream client's error family. This is not stream-event IR conversion. | UAPI normalization in `internal/relay/handler.go:2055-2185`; tests in `internal/relay/error_handling_test.go:10-87`. Bifrost route-level error conversion also returns JSON for pre-stream errors: `upstream/bifrost/transports/bifrost-http/integrations/utils.go:203-222`, `utils_test.go:185-221`. | Current contract is intentional: OpenAI-style, Anthropic-style, or Gemini-style error JSON selected by client format. |
| Response headers | Gateway and relay copy non-hop-by-hop response headers; converted responses sanitize content/encoding/range headers and set JSON content type. Provider adaptors set upstream request headers. | Gateway copy in `internal/gateway/gateway.go:841-857`; relay copy in `internal/relay/handler.go:1189-1195`, `2474-2498`; Bifrost forwards provider headers and additionally emits routed identity headers at `upstream/bifrost/transports/bifrost-http/integrations/utils.go:238-260`, `utils_test.go:267-371`. | UAPI does not emit Bifrost-style `x-bifrost-*`; this remains an explicit non-goal. |
| Streaming headers | Relay sets `Content-Type: text/event-stream`, `Cache-Control: no-cache`, `Connection: keep-alive`, `X-Accel-Buffering: no`. | UAPI: `internal/relay/handler.go:638-643`; Bifrost passthrough streaming headers use the same invariants at `upstream/bifrost/transports/bifrost-http/integrations/router.go:3056-3066`. | Streaming handler header behavior is implementation-fixed; add end-to-end header tests when the relay harness can exercise upstream streaming. |
| `[DONE]` | UAPI appends `[DONE]` only for downstream OpenAI Chat in normal streaming. Force-stream path (`Codex` or `ForceStream=true` channel) still appends `[DONE]` in `convertSSEBufferWithConverter`, because it converts to OpenAI Chat SSE intermediate format first. | UAPI normal streaming `sendDone` is selected at `internal/relay/handler.go:650-653`; `SSEStreamReader.SendDone` writes `data: [DONE]` at `internal/relay/sse_reader.go:71-73`; force-stream append at `internal/relay/handler.go:1311-1346`; tests in `internal/relay/streaming_test.go:408-467` and `TestStreamAndForwardNormalNonChatTargetsDoNotAppendDone`. Bifrost suppresses `[DONE]` for Anthropic, Responses, image generation, GenAI, and Bedrock at `upstream/bifrost/transports/bifrost-http/integrations/router.go:2594-2596`, `2814-2820`. | Do not change globally; non-Chat normal streaming targets have explicit no-DONE regression coverage. |
| Stream terminal detection | Raw/converted streams must reach terminal event or fail; client disconnect is status 499 when usage can be recorded. | UAPI: `internal/relay/handler.go:668-726`; tests in `internal/relay/streaming_test.go:98-248`, `389-467`, `599-610`. Bifrost Gemini stream terminal detection uses finish reason plus usage metadata at `upstream/bifrost/core/providers/gemini/chat.go:332-334`, `486-510`. | Stream event matrix remains documented in section 24; this section validates terminal/failure contract. |

## 17. Required Repair Checklist For Next Model

Use this checklist to audit and repair UAPI without changing the high-level architecture. Items marked complete are current source facts, not future work.

1. ✅ Keep Gateway free of protocol conversion. Gateway routes/signs and forwards to Relay; protocol conversion is in Relay/provider conversion code. Sources: `internal/gateway/gateway.go:254-398`, `internal/relay/handler.go:248-548`.
2. ✅ Keep Relay as the conversion boundary. Relay detects client format, selects upstream format, and calls `NormalizeRequestSameProtocol` or `ConvertRequestWithAdaptor`. Source: `internal/relay/handler.go:248-548`.
3. ✅ Preserve same-protocol raw-ish forwarding for performance. Same-format requests use `NormalizeRequestSameProtocol`, which cleans and validates raw JSON without IR re-emission. Sources: `internal/relay/handler.go:546-548`, `internal/relay/provider/convert/registry.go:31-44`, `internal/relay/plan_contracts_test.go:93-123`.
4. ✅ Protocol-family preference is not part of Gateway routing. Gateway routing uses model support, weight, and health; Relay decides same-protocol versus cross-protocol from `clientFormat` and `upstreamFormat`. Sources: `internal/gateway/gateway.go:254-398`, `internal/relay/handler.go:468-548`.
5. ✅ Baseline request/response pair matrix exists for all registered formats. `TestRequestProtocolMatrixEmitsValidJSON` and `TestResponseProtocolMatrixEmitsValidJSON` cover OpenAI Chat, OpenAI Responses, Codex, Anthropic, Claude Code, Gemini, Gemini CLI, and Antigravity pairwise JSON validity. Source: `internal/relay/provider/convert/protocol_matrix_test.go:10-74`.
6. ⚠️ Request semantic coverage is broad but still grows by field class. Existing tests cover text, images, PDF/file blocks, tool definitions/choices/calls/results, structured output, reasoning/thinking, cache fields, native top-level fields, and loss records; future additions should target uncovered protocol pairs rather than reworking architecture.
7. ⚠️ Response semantic coverage is broad but still grows by field class. Existing tests cover text, tool calls, reasoning/encrypted/redacted thinking, usage/cache tokens, finish reasons, and native output items; refusal/safety/content-filter pairwise coverage remains a focused expansion area.
8. ⚠️ Stream tests cover core lifecycle, usage, terminal events, aliases, multiline SSE, no-DONE for non-Chat targets, and selected semantic conversions. Future work should mirror every new non-stream semantic regression with an SSE case. Sources: `internal/relay/streaming_test.go:98-620`, `internal/relay/provider/convert/stream_redesign_test.go`.
9. ⚠️ OAuth/native tests exist for current native channels, including Codex, Claude Code, Gemini Code Assist, and Antigravity envelopes/headers/usage. Keep adding tests when native envelope fields or upstream CLI behavior changes.
10. ✅ Unsupported route behavior is explicit for count tokens, batch, files, cached contents, async, and large unsupported route families. Sources: `internal/relay/request_type.go:29-100`, `internal/relay/request_type_test.go:34-90`, `internal/relay/handler.go:251-254`.
11. ✅ Antigravity `thinkingLevel` -> nested `thinkingBudget` conversion is implemented. Sources: `internal/relay/provider/antigravity/adaptor.go:647-655`; competitor evidence for budget-based Antigravity request shaping is in `upstream/CLIProxyAPI/internal/thinking/provider/antigravity/apply.go:133-182`; tests: `internal/relay/provider/antigravity/auth_test.go:127-197`.
12. ✅ Antigravity `googleSearch` + `functionDeclarations` conflict detection is implemented. Sources: `internal/relay/provider/antigravity/adaptor.go:158-162`, `adaptor.go:696-730`; competitor source shows both tool families are emitted in Antigravity translators at `upstream/CLIProxyAPI/internal/translator/antigravity/openai/chat-completions/antigravity_openai_request.go:339-438`; tests: `internal/relay/provider/antigravity/auth_test.go:199-234`.

## 18. Non-Negotiable Semantics

| Semantic | Source evidence | Required consequence |
|---|---|---|
| Do not convert Anthropic Messages through internal Chat. | `FormatAnthropic` has direct IR parser/emitter registration in `internal/relay/provider/convert/anthropic.go:749-750` and pairwise matrix coverage in `internal/relay/provider/convert/protocol_matrix_test.go:10-74`. | Anthropic request/response conversion must use IR, not a synthetic OpenAI Chat intermediate. |
| Do not convert Gemini generateContent through internal Chat. | Gemini has direct request/response IR conversion in `internal/relay/provider/convert/gemini.go:902-903`, `response_gemini.go:260-263`, and stream IR registration in `internal/relay/provider/stream/ir_anthropic_gemini.go:689-701`. | Gemini request/response/stream conversion must preserve Gemini-specific parts through IR where possible. |
| Do not promise cross-protocol losslessness. | Cross-protocol native extras are recorded and dropped by `internal/relay/provider/convert/registry.go:161-181`; content-specific losses are attached by converter helpers. | Unsupported/native fields must either emit target-compatible data or record warning losses. |
| Do not drop reasoning/thinking signatures silently. | Reasoning/signature fields are represented in IR at `internal/relay/provider/ir/content.go:122-130`; request/response tests cover Anthropic signatures and Responses encrypted reasoning in `protocol_redesign_test.go`. | Preserve as encrypted/redacted/thought-signature reasoning when target supports it, otherwise record loss. |
| Do not treat precision-sensitive arguments as float64-decoded generic JSON. | Shared generic decoding uses `provider.DecodeJSONUseNumber` at `internal/relay/provider/types.go:61-67`; request rewrite tests cover large integer preservation. | Tool arguments and raw native fields must avoid lossy float64 coercion. |
| Do not append `[DONE]` for every normal stream. | Normal streaming sets `sendDone := clientFormat == FormatOpenAIChatCompletions` at `internal/relay/handler.go:650-653`; force-stream conversion still appends `[DONE]` at `handler.go:1311-1346`; no-DONE tests cover non-Chat normal targets. Bifrost similarly suppresses `[DONE]` for Anthropic, Responses, GenAI, Bedrock, and images at `upstream/bifrost/transports/bifrost-http/integrations/router.go:2594-2597`, `2814-2820`. | Only OpenAI Chat downstream normal streams get `[DONE]`; force-stream keeps its documented OpenAI Chat intermediate behavior. |
| Do not let same-protocol traffic pay full IR re-emission cost. | Relay same-format branch uses `NormalizeRequestSameProtocol` at `internal/relay/handler.go:546-548`; contract test forbids emitter rebuild in `internal/relay/plan_contracts_test.go:93-123`. | Same-protocol JSON should stay raw-ish unless a targeted sanitizer is intentionally added. |
| Do not let OAuth CLI channels use standard API headers/envelopes. | Native formats are distinct in `internal/relay/provider/convert/native_protocols.go:10-110`; provider adaptors own headers/envelopes for Codex, Claude Code, Gemini Code Assist, and Antigravity. | Native channels must keep their own URL/header/envelope behavior. |
| Do not route unsupported media/count-token/batch/file/cache paths into chat conversion by default. | Unsupported route detection is in `internal/relay/request_type.go:29-100`; early 400 is in `internal/relay/handler.go:251-254`. | Implement first-class route support or explicitly reject; never silently coerce to chat/generate. |

## 19. Bifrost Full Route Appendix

This appendix is the strict route-level source map that the earlier Bifrost review did not fully enumerate. The entries are Bifrost source facts, not UAPI behavior.

### 19.1 Bifrost Generic Route Machinery

| Capability | Bifrost source fact | Implementation consequence for UAPI repair |
|---|---|---|
| Extra params passthrough | `upstream/bifrost/transports/bifrost-http/integrations/router.go:84-90` defines `RequestWithSettableExtraParams`; the router extracts provider-specific `extra_params` only when the request type implements it and the passthrough header is enabled. | UAPI has no `x-bf-passthrough-extra-params` equivalent. Cross-protocol native extras are recorded as losses and dropped by `internal/relay/provider/convert/registry.go:161-181`. |
| Batch/file/container/cached content converters | `router.go:92-156` defines `BatchRequest`, `FileRequest`, `ContainerRequest`, `ContainerFileRequest`, `CachedContentRequest` and their converter function types. | UAPI conversion IR does not model these route families as first-class conversion endpoints. Do not let them fall into chat conversion. |
| Same-provider raw is still mediated | Bifrost raw request body is stored in route/provider context, but providers still own URL/header/model/body sanitizer logic; Anthropic raw-body mutation is in `upstream/bifrost/core/providers/anthropic/requestbuilder.go:103-196`, `305-312`. | UAPI same-protocol behavior is different: `NormalizeRequestSameProtocol` validates and returns cleaned raw JSON at `internal/relay/provider/convert/registry.go:31-44`, then adaptors rebuild URL/headers. |

### 19.2 Bifrost OpenAI Route Families

| Family | Method/path | Request parser/body | Internal target | Response/stream/error converter | UAPI delta |
|---|---|---|---|---|---|
| Azure dynamic route | `POST {pathPrefix}/openai/deployments/{deploymentPath:*}` at `openai.go:300-325`; dispatches chat, responses/input_tokens, responses, completions, embeddings, audio, images by suffix. | `GetRequestTypeInstance` dispatches by `BifrostContextKeyHTTPRequestType` at `openai.go:346-371`; multipart dispatch for transcription/image edit/variation at `openai.go:373-392`. | Chat/Responses/Text/Embedding/Speech/Transcription/Image requests at `openai.go:394-437`. | Multiple response converters and stream converters at `openai.go:439-557`. | UAPI has no Azure deployment public route. |
| Chat Completions | `POST {pathPrefix}/v1/chat/completions`, `POST {pathPrefix}/chat/completions` at `openai.go:561-570`. | `OpenAIChatRequest`; large-payload prehook at `openai.go:570`. | `BifrostChatRequest`. | Chat stream converter at `openai.go:642-694`; error converter returns `BifrostError`. | UAPI only accepts `/v1/chat/completions`. |
| Responses | `POST {pathPrefix}/v1/responses`, `/responses`, `/openai/responses` at `openai.go:712-719`. | `OpenAIResponsesRequest`; prehook hydrates large payload and Azure UA at `openai.go:781-788`. | `BifrostResponsesRequest` at `openai.go:727-731`. | Raw OpenAI response first at `openai.go:735-741`; async converter at `openai.go:743-759`; stream converter at `openai.go:764-779`. | UAPI only accepts exact `/v1/responses` and `/v1/responses/` as Responses; subroutes are unsupported in `internal/relay/request_type.go:35-39`. |
| Responses input tokens | `POST {pathPrefix}/v1/responses/input_tokens`, `/responses/input_tokens`, `/openai/responses/input_tokens` at `openai.go:792-805`. | `OpenAIResponsesRequest`. | `CountTokensRequest` at `openai.go:813-818`. | Count token response converter at `openai.go:818-820`. | UAPI has no first-class `/v1/responses/input_tokens` conversion route and now rejects it before conversion via `internal/relay/request_type.go:35-39`. |
| Audio speech | `POST {pathPrefix}/v1/audio/speech`, `/audio/speech` at `openai.go:871-912`. | `OpenAISpeechRequest`; large-payload prehook. | `SpeechRequest` at `openai.go:889-891`. | Speech stream converter at `openai.go:900-907`. | UAPI detects `/v1/audio/speech` as media at `internal/relay/request_type.go:60-61` and allows it only for OpenAI channels at `request_type.go:157-158`; it is not conversion IR. |
| Audio transcription | `POST {pathPrefix}/v1/audio/transcriptions`, `/audio/transcriptions` at `openai.go:915-968`. | Multipart parser `parseTranscriptionMultipartRequest` at `openai.go:932`, implemented at `openai.go:2914-2967`. | `TranscriptionRequest` at `openai.go:933-938`. | Plain-text special response at `openai.go:941-950`; stream converter at `openai.go:956-963`. | UAPI media route, not conversion IR. |
| Images generation | `POST {pathPrefix}/v1/images/generations`, `/images/generations` at `openai.go:971-1020`. | `OpenAIImageGenerationRequest`; JSON. | `ImageGenerationRequest` at `openai.go:988-993`. | Raw-first image response at `openai.go:996-1002`; stream converter at `openai.go:1007-1018`. | UAPI detects image media routes at `internal/relay/request_type.go:54-59` and allows OpenAI plus Antigravity at `request_type.go:153-156`; this is media handling/special Antigravity image conversion, not four-family IR. |
| Images edits/variations | `POST {pathPrefix}/v1/images/edits`, `/images/edits` at `openai.go:1023-1072`; `POST {pathPrefix}/v1/images/variations`, `/images/variations` at `openai.go:1074-1123`. | Multipart parsers at `openai.go:1039`, `1090`; image edit parser extracts `image[]`/`image`, reference images, mask, partial images at `openai.go:3008-3083`. | `ImageEditRequest` and `ImageVariationRequest` at `openai.go:1040-1045`, `1091-1096`. | Image response and stream converters at `openai.go:1048-1070`, `1099-1120`. | UAPI media path only, detected at `internal/relay/request_type.go:54-59` and gated at `request_type.go:153-156`. |
| Videos | Create/list/retrieve/download/delete/remix routes start at `openai.go:1126-1320`, with create `POST /v1/videos`, retrieve `GET /v1/videos/{video_id}`, content `GET /v1/videos/{video_id}/content`, delete `DELETE /v1/videos/{video_id}`, remix `POST /v1/videos/{video_id}/remix`. | Multipart parser for create at `openai.go:1143`; path extraction prehooks at `openai.go:1158-1164`, `1198`, `1232`, `1266`, `1300`. | Dedicated video request types. | Video response/download converters at `openai.go:1152-1154`, `1192-1194`, `1226-1227`. | UAPI detects `/v1/videos` and `/v1/video/` as media routes in `internal/relay/request_type.go:72-73` and gates them to OpenAI at `request_type.go:159-160`; no conversion IR. |
| Files | Upload route `POST {pathPrefix}/files`, `{pathPrefix}/openai/files` at `openai.go:1688-1715`; additional file list/retrieve/delete/content routes continue through `openai.go:1913`. | Multipart upload parser `parseOpenAIFileUploadMultipartRequest` at `openai.go:1701`. | `FileRequest` with upload/list/retrieve/delete/content. | File converters are route-specific. | UAPI has no OpenAI files conversion-core route. |
| Containers | Create/list/retrieve/delete `containers` routes start at `openai.go:2300-2325`; container file routes continue through `openai.go:2709`. | JSON and multipart/container file request structs. | `ContainerRequest` and `ContainerFileRequest`. | Container response converters are route-specific. | UAPI has no containers conversion route. |

### 19.3 Bifrost Anthropic Route Families

| Family | Method/path | Request parser/body | Internal target | Response/stream/error converter | UAPI delta |
|---|---|---|---|---|---|
| Messages | `POST {pathPrefix}/v1/messages`, `POST {pathPrefix}/v1/messages/{path:*}` at `anthropic.go:74-84`. | `AnthropicMessageRequest` at `anthropic.go:88-90`. | `BifrostResponsesRequest` at `anthropic.go:92-99`; `normalizeBifrostInputContentBlocks` at `anthropic.go:94-95`. | Response converter at `anthropic.go:102-109`; async at `anthropic.go:111-126`; error at `anthropic.go:127-129`; stream at `anthropic.go:130-170`. | UAPI only accepts exact `/v1/messages` and `/v1/messages/`; subroutes are unsupported in `internal/relay/request_type.go:40-44`. |
| Claude Code passthrough gate | `checkAnthropicPassthrough` runs as prehook at `anthropic.go:70`, `173`. It checks UA/model at `anthropic.go:338-374`, stores full path/headers for non-API-key OAuth-like flow at `anthropic.go:346-355`, and allowlists safe headers for API-key flow at `anthropic.go:356-361`. | Same body shape as Anthropic Messages, but Bifrost may attach raw body and raw response flags. | Still sent through provider request builder. | Anthropic provider raw builder still deletes fields, strips unsupported raw fields, remaps tools, injects beta headers at `requestbuilder.go:103-196`, `305-312`. | UAPI models `claude_code` as Anthropic native family; no Bifrost-style UA/path/header raw gate. |
| Models | `GET {pathPrefix}/v1/models` at `anthropic.go:249-275`. | `BifrostListModelsRequest`. | `ListModelsRequest`. | `ToAnthropicListModelsResponse` at `anthropic.go:269-270`. | UAPI gateway handles `/v1/models` locally at `internal/gateway/gateway.go:445-476` and may emit Anthropic model-list shape based on auth style; path helpers are `gateway.go:615-621`. |
| Count tokens | `POST {pathPrefix}/v1/messages/count_tokens` at `anthropic.go:415-448`. | `AnthropicMessageRequest`; same prehook passthrough. | `CountTokensRequest` at `anthropic.go:430-435`. | `ToAnthropicCountTokensResponse` at `anthropic.go:439-440`. | UAPI rejects this path before conversion via `internal/relay/request_type.go:40-44`. |
| Batches | `POST {pathPrefix}/v1/messages/batches` create at `anthropic.go:450-529`; `GET {pathPrefix}/v1/messages/batches` list starts at `anthropic.go:531-560` and further batch retrieve/cancel/results/delete routes follow. | Anthropic batch request structs. | `BatchRequest` with create/list/retrieve/cancel/results/delete. | Anthropic batch response converters. | UAPI has no first-class batch route. |

### 19.4 Bifrost Gemini / GenAI Route Families

| Family | Method/path | Request parser/body | Internal target | Response/stream/error converter | UAPI delta |
|---|---|---|---|---|---|
| Dynamic model action | `POST {pathPrefix}/v1beta/models/{model:*}` at `genai.go:103-106`. | `GeminiGenerationRequest` or specialized request based on context at `genai.go:111-122`; prehook `extractAndSetModelAndRequestType` at `genai.go:287`. | Responses/countTokens/embedding/speech/transcription/image/video/batch paths at `genai.go:124-176`. | Response converters for embedding/responses/speech/transcription/countTokens/image/video/batch at `genai.go:234-263`; stream converter at `genai.go:264-286`; error converter `gemini.ToGeminiError` at `genai.go:261-262`. | UAPI accepts Gemini generate/stream generate paths as Gemini generate format, but rejects unsupported GenAI actions/resources via `internal/relay/request_type.go:45-99`. |
| Action classification | `:countTokens` at `genai.go:1201-1203`, `1355-1357`; `:embedContent` sets embed context at `genai.go:1371-1375`; `:predictLongRunning` video at `genai.go:1378-1380`; `:batchGenerateContent` at `genai.go:1383-1385`. | Large payload and streamed body classification fall back conservatively at `genai.go:1236-1260`, `1412-1444`. | Dedicated Bifrost request families. | Converter selected by `RequestType`. | UAPI does not implement this dynamic action matrix; count/embed/predict/batch actions are explicitly unsupported in `internal/relay/request_type.go:89-99`. |
| Speech/transcription/image classification | Speech if `responseModalities` contains `AUDIO` or `speechConfig` at `genai.go:1476-1487`; transcription if audio input exists and not speech at `genai.go:1494-1537`; image generation/edit at `genai.go:1544-1570`. | Gemini generate body. | Speech/Transcription/Image requests. | Gemini response converters. | UAPI standard Gemini path does not promote these to media request types. |
| Files | Upload route `POST {pathPrefix}/upload/v1beta/files` at `genai.go:351-378`; step 2 handles raw binary by `upload_id` at `genai.go:365-370`. | Two-phase metadata/binary parser. | `FileRequest`. | File response converters in same route family. | UAPI has no first-class Gemini files route and rejects `/upload/v1beta/files*` and `/v1beta/files*` via `internal/relay/request_type.go:79-86`. |
| Batches | Batch list starts at `genai.go:578-605`; batch create via dynamic `:batchGenerateContent` at `genai.go:177-233`. | Gemini batch request structs. | `BatchRequest`. | Gemini batch response converters at `genai.go:258-260`, `600-605`. | UAPI has no first-class batch route and rejects `/v1beta/batches*` plus `:batchGenerateContent` via `internal/relay/request_type.go:83-99`. |
| Cached contents | `POST {pathPrefix}/v1beta/cachedContents` create at `genai.go:931-960`; list/retrieve/update/delete follow in the same function. | Validates raw JSON and required `model` at `genai.go:946-960`. | `CachedContentRequest`. | Cached content converters in route family. | UAPI does not implement cached content CRUD and rejects `/v1beta/cachedContents*` via `internal/relay/request_type.go:83-86`; request-side generateContent `cachedContent` is lifted into `Cache.CachedContent` by `internal/relay/provider/convert/gemini.go:42-43` and emitted at `gemini.go:265-266`. |

## 20. Bifrost Field Loss And Capability Matrix

This matrix describes Bifrost behavior that UAPI must either implement, explicitly reject, or document as a non-goal. It should be used before copying any Bifrost design into UAPI.

| Field/capability | Bifrost source fact | Known UAPI behavior | Required UAPI repair/test |
|---|---|---|---|
| OpenAI Chat content parts | OpenAI dynamic route parses JSON or multipart and can dispatch Chat, Responses, audio, images by suffix at `openai.go:346-437`. The Bifrost review records Chat message content string/block handling at `BIFROST_PROTOCOL_CONVERSION_LAYER_REVIEW.md:85-93`. | UAPI Chat parser maps string, `image_url`, `file`, `input_file`, `input_image`, `input_text`, `output_text` into IR at section 5.2; direct parser is `internal/relay/provider/convert/openai_chat.go:11-90`. | Keep Chat conversion focused on `/v1/chat/completions`; do not absorb audio/image/file APIs into Chat IR. |
| OpenAI Responses opaque/server items | Bifrost Responses route maps to `BifrostResponsesRequest` at `openai.go:727-731` and has Responses stream/async converters at `openai.go:743-779`. The review identifies Responses fallback-to-Chat semantic loss at `BIFROST_PROTOCOL_CONVERSION_LAYER_REVIEW.md:172`. | UAPI preserves Responses-family native raw turns when emitting Responses/Codex at `internal/relay/provider/convert/openai_responses.go:216-221`; opaque output raw can be emitted in `response_openai.go:452-476`; tests cover `file_search_call`, namespace/MCP projection, and image generation response items. | Continue adding pairwise tests for MCP approval, code interpreter, and unknown item preservation/loss per target. |
| Anthropic tool result without matching tool use | Bifrost Anthropic adapter may convert unmatched tool result to user text to avoid 400; review notes this at `BIFROST_PROTOCOL_CONVERSION_LAYER_REVIEW.md:226-227`. | UAPI Anthropic request emitter requires tool result id at section 7.3 and emits string-only tool-result content. | Add UAPI-specific tests for unmatched tool result in every target; do not assume Bifrost fallback exists. |
| Anthropic structured output | Bifrost review records native `output_config.format` versus synthetic tool behavior at `BIFROST_PROTOCOL_CONVERSION_LAYER_REVIEW.md:229-232`. | UAPI has only partial structured-output projection; section 13 lists full Anthropic structured output selection as missing. | Add source-level mapper table before implementing: Anthropic direct, Vertex Claude, thinking enabled, file citations, synthetic tool leak prevention. |
| Anthropic raw passthrough fields | Bifrost passthrough safe headers are `anthropic-beta`, `anthropic-dangerous-direct-browser-access`, `anthropic-version` at `anthropic.go:186-190`; raw body sanitizer strips/remaps unsupported fields at `requestbuilder.go:174-196`, deletes `fallbacks` at `requestbuilder.go:305-308`, and injects beta headers into body at `requestbuilder.go:310-315`. | UAPI Claude Code uses OAuth headers and Anthropic emitter; no raw gate/sanitizer. | Add explicit non-goal or implement gate. Tests must prove unsupported raw fields do not leak if implemented. |
| Gemini `cachedContent` | Bifrost dynamic route identifies count/cached content route families; cached content CRUD starts at `genai.go:931-960`. Bifrost Gemini request conversion maps `cachedContent` into provider semantics per review `BIFROST_PROTOCOL_CONVERSION_LAYER_REVIEW.md:282-299`; provider code maps shared `cached_content` extra into Gemini `CachedContent` at `upstream/bifrost/core/providers/gemini/chat.go:60-62`. | UAPI generateContent parser lifts top-level `cachedContent` into `Request.Cache.CachedContent` and Gemini emitter writes it back at `internal/relay/provider/convert/gemini.go:42-43`, `gemini.go:265-266`; cached content CRUD routes remain explicitly unsupported. | Covered by `TestGeminiCachedContentMapsThroughIR`; keep CRUD route unsupported unless a first-class cached-content route family is implemented. |
| Gemini `thoughtSignature` | Bifrost review states `thoughtSignature` is used for multi-turn tool state and maps into encrypted reasoning/function call id with validator bypass behavior at `BIFROST_PROTOCOL_CONVERSION_LAYER_REVIEW.md:313-314`. | UAPI request parser preserves `thoughtSignature` on reasoning items at `internal/relay/provider/convert/gemini.go:106-132`; stream tests cover Gemini -> Anthropic and Anthropic -> Gemini signatures at `internal/relay/provider/convert/stream_redesign_test.go:360-385`. | Add non-stream round-trip tests with tool call/functionResponse and missing signature boundary. |
| Provider/model sanitizers | Bifrost has model/provider-specific filters for OpenAI, Anthropic, Gemini, Vertex, Azure, Bedrock; Anthropic raw sanitizer is concrete in `requestbuilder.go:174-196`. | UAPI only has limited target-specific losses and a few sanitizers: `registry.go:81-121`, Antigravity schema sanitizer in `internal/relay/provider/antigravity/adaptor.go:150-160`. | Build a provider capability matrix before widening sanitizers. |
| SSE terminal semantics | Bifrost does not append `[DONE]` for Anthropic, Responses, GenAI, Bedrock, and image generation according to review `BIFROST_PROTOCOL_CONVERSION_LAYER_REVIEW.md:64-67`. | UAPI normal streaming appends `[DONE]` only for downstream OpenAI Chat at `handler.go:615-616`. Force-stream path (`Codex` or `ForceStream` channel) unconditionally appends `[DONE]` in `convertSSEBufferWithConverter` at `handler.go:1303-1304` because it converts to OpenAI Chat SSE intermediate first. Tests cover Chat `[DONE]` and non-Chat no-DONE in `internal/relay/streaming_test.go:408-505`. | Keep UAPI rule; force-stream `[DONE]` remains documented intermediate behavior. |

## 21. OAuth / Native Channel Evidence Appendix

This appendix separates UAPI facts, official/client source facts, competitor facts, and still-unverified behavior. It extends the summary in section 9.

| Channel | UAPI source fact | Official/client source fact | Competitor fact | Inference / still needs verification |
|---|---|---|---|---|
| Codex | `openai` + `api_format=codex` maps to `FormatCodexResponses` at `internal/relay/handler.go:473`; Codex URL base replacement and `/responses` route are in `internal/relay/provider/openai/adaptor.go:32-43`; headers are in `openai/adaptor.go:56-70`; OAuth constants in `openai/auth.go:23-34`, auth params in `openai/auth.go:106-119`. | Codex auth query uses `openid profile email offline_access api.connectors.read api.connectors.invoke`, PKCE S256, `id_token_add_organizations`, `codex_cli_simplified_flow`, and `originator` at `upstream/codex/codex-rs/login/src/server.rs:491-508`; official Responses client path ends with `/responses` at `upstream/codex/codex-rs/codex-api/tests/clients.rs:254-272`; ChatGPT backend cookie tests target `https://chatgpt.com/backend-api/codex/responses` at `upstream/codex/codex-rs/codex-client/src/chatgpt_cloudflare_cookies.rs:130-205`. | cockpit-tools Codex local access allows official Codex headers `X-Codex-Turn-State`, `X-Codex-Turn-Metadata`, `X-Client-Request-Id`, `X-ResponsesAPI-Include-Timing-Metrics`, `Version`, `Originator`, `Session_id`, `Conversation_id`, `ChatGPT-Account-Id` at `upstream/cockpit-tools/src-tauri/src/modules/codex_local_access.rs:133-140`, injects empty official headers at `codex_local_access.rs:7441-7449`, and aligns session/conversation ids at `codex_local_access.rs:7451-7467`. | UAPI currently sends a smaller header set than cockpit-tools. Verify whether official Codex now requires any of the additional empty headers before adding them. |
| Claude Code | `anthropic` + `api_format=claude_code` maps to `FormatClaudeCode` at `internal/relay/handler.go:481`; OAuth headers are set at `internal/relay/provider/anthropic/adaptor.go:38-51`; constants/scopes/profile endpoints at `anthropic/auth.go:23-38`. | Claude Code user-agent helper returns `claude-code/${VERSION}` at `upstream/claude-code-source-1/src/utils/userAgent.ts:8-10`; OAuth beta, scopes, token/profile URLs are in `upstream/claude-code-source-2/src/constants/oauth.ts:33-93`. | CLIProxyAPI has Claude device profile logic that derives UA/runtime/package headers from client headers and applies a baseline at `upstream/cockpit-tools/sidecars/cockpit-cliproxy/cdk/CLIProxyAPI/internal/runtime/executor/helps/claude_device_profile.go:209-354`; Claude executor reads client UA and entrypoint at `.../claude_executor.go:1484-1516`. | UAPI does not implement device profile/header mirroring. Verify current Claude Code releases before adding Stainless/package/runtime headers. |
| Gemini CLI / Code Assist | `gemini` + `api_format=gemini_code` maps to `FormatGeminiCode` at `internal/relay/handler.go:487`; Code Assist URL/header logic at `internal/relay/provider/gemini/adaptor.go:36-61`; OAuth/API key branch at `gemini/adaptor.go:73-80`; v1internal envelope at `gemini/codeassist_convert.go:17-44`; session id logic at `codeassist_convert.go:89-110`. | Gemini CLI test data shows `generateContentStream` envelope events at `upstream/gemini-cli/packages/sdk/test-data/tool-success.json:1-8`; Gemini CLI core calls `generateContentStream` with model/contents/config at `upstream/gemini-cli/packages/core/src/core/geminiChat.ts:851-858`. | CLIProxyAPI config exposes Gemini CLI internal endpoint switch and signature-cache knobs at `upstream/CLIProxyAPI/config.example.yaml:132-151`; OAuth model alias/exclusion and protocol-specific payload config are documented at `config.example.yaml:333-395`. | UAPI `gemini_code` is Code Assist/v1internal transport, not a byte-for-byte Gemini CLI process clone. Verify current UA and project/session requirements when bumping constants. |
| Antigravity | `antigravity` maps to `FormatAntigravity` at `internal/relay/handler.go:492`; URL/header in `internal/relay/provider/antigravity/adaptor.go:95-115`; v1internal envelope/model routing/schema sanitation/conflict detection at `adaptor.go:117-164`; thinkingLevel -> thinkingBudget conversion at `adaptor.go:638-655`; OAuth constants and dynamic version endpoints in `internal/relay/provider/antigravity/auth.go:20-39`, auth URL at `auth.go:146-167`. | No official Antigravity source is present in this repository. | Antigravity-Manager documents v1internal `googleSearch`/`functionDeclarations` conflict at `upstream/Antigravity-Manager/README.md:492-493`, thinkingLevel -> thinkingBudget conversion at `README.md:616-619`, OAuth exchange dynamic UA at `README.md:625-627`, and dynamic request fingerprint headers including `X-Client-Name`, `X-Client-Version`, `X-Machine-Id`, `X-VSCode-SessionId` at `README.md:682-684`. cockpit-tools notes Cloud Code quota/onboarding derives IDE version/platform/client headers including `x-goog-api-client` at `upstream/cockpit-tools/CHANGELOG.md:719-722`. CLIProxyAPI Antigravity auth sets short UA for userinfo at `upstream/CLIProxyAPI/internal/auth/antigravity/auth.go:187-193`, request UA for loadCodeAssist at `auth.go:238-247`, and `X-Goog-Api-Client` during onboarding at `auth.go:319-323`. | UAPI inference currently sends only `Content-Type`, bearer auth, and `User-Agent`; auth helpers implement dynamic version/User-Agent discovery, but additional inference/onboarding fingerprint headers remain competitor facts until verified against live official traffic. |

## 22. UAPI Test Coverage Matrix

This matrix replaces broad "add tests" language with exact current coverage and remaining gaps.

| Area | Existing tests | Current coverage | Remaining gaps |
|---|---|---|---|
| Request type routing | `internal/relay/request_type_test.go:12-75` covers protocol family detection and unsupported conversion subroutes; `request_type.go:29-100` is the implementation. | Four main protocol families, media classes, and unsupported subpaths such as `/v1/responses/input_tokens`, `/v1/messages/count_tokens`, Gemini `:countTokens`, files/batches/cache are covered. | Add tests only when new route families are added. |
| Same-protocol raw-ish path | Contract test `internal/relay/plan_contracts_test.go:93-125`; converter tests around `NormalizeRequestSameProtocol`; implementation `registry.go:31-44`, relay branch `handler.go:546-548`. | Same protocol validates and cleans undefined sentinels without IR re-emission; unknown top-level fields and Gemini no body-model injection are covered. | Add per-provider sanitizer tests if a same-protocol sanitizer is intentionally added. |
| Cross-protocol basics | `internal/relay/provider/convert/protocol_redesign_test.go:35-99`, `828-875`, `946-957`, `1083-1138`; pairwise native aliases in `protocol_matrix_test.go:10-68`. | Chat to major targets, rich content/tool calls, cross-protocol extra dropping, and all registered format pairs including Codex/Claude Code/Gemini Code/Antigravity native aliases are covered for valid JSON. | Add field-specific assertions per pair as new gaps are found. |
| File/PDF/document | `protocol_redesign_test.go:1554-1583` covers Gemini PDF inlineData -> Responses input_file; `1585-1597` covers Chat file -> Gemini inlineData; `1599-1627` covers Anthropic document -> Chat file. | Basic PDF data URI/document conversion exists. | Add file URL/id, filename defaulting, mime type loss records, Anthropic document citations, OpenAI `input_file` to Anthropic/Gemini, and every protocol pair. |
| Structured tool-result output | `protocol_redesign_test.go:1669-1686` verifies Responses function_call_output object is preserved in IR and same Responses output; missing tool-result call id rejection across Chat/Responses/Anthropic/Gemini is covered at `protocol_redesign_test.go:414-449`. | Responses same-family structured output and core emitter identifier rejection are covered. | Add cross-protocol tool-result object loss/projection tests for Chat, Anthropic, Gemini, Antigravity. |
| Reasoning/thinking/signatures | `protocol_redesign_test.go:1140-1152`, `1201-1209`; stream coverage in `stream_redesign_test.go:360-397`. | Anthropic/Gemini/OpenAI reasoning state is partially preserved across response and stream conversions. | Add request-side multi-turn Gemini `thoughtSignature`/functionResponse tests and Anthropic `redacted_thinking` request tests. |
| Cache usage | Adapter and stream tests: `internal/relay/provider/openai/adaptor_test.go:54-82`, `anthropic/adaptor_test.go:9-13`, `gemini/adaptor_test.go:44-59`, `antigravity/auth_test.go:18-38`, `stream_redesign_test.go:125-155`; request-side Gemini `cachedContent` test in `protocol_redesign_test.go`. | Non-stream/stream cache token parsing exists for major providers; Gemini request-side `cachedContent` enters IR and emits back to Gemini. | Add cross-protocol cache-control/cache-marker tests for non-Gemini request-side cache features. |
| Streaming parser/emitter | `internal/relay/provider/convert/stream_redesign_test.go:101-170`, `278-397`; relay streaming tests `internal/relay/streaming_test.go:48-156`, `279-357`, `408-557`. | Covers Responses, Anthropic, Gemini cache usage, multiline SSE, `[DONE]`, aliases, signatures, and non-Chat downstream no-DONE protocols. | Add per-event matrix tests for any parser/emitter event still missing in section 24. |
| OAuth fingerprints | `internal/relay/provider/oauth_fingerprint_test.go:13-113`; provider-specific header tests include `openai/adaptor_test.go:55`, `anthropic/adaptor_test.go:47`, `gemini/adaptor_test.go:44`, and `antigravity/auth_test.go:77`. | Auth URLs, PKCE/user-agent fingerprints, Codex/Claude Code/Gemini Code Assist/Antigravity request headers, Antigravity UA, and usage parsing are covered. | Add Codex forced-stream and force-stream back-conversion tests. |
| Antigravity image/native special path | `internal/relay/antigravity_images_test.go:13-122`; implementation in `internal/relay/antigravity_images.go`; native fixes in `internal/relay/provider/antigravity/auth_test.go:127-233`. | OpenAI Images API to Antigravity and response conversion; competitor-derived `googleSearch` + `functionDeclarations` rejection and `thinkingLevel` -> `thinkingBudget` conversion are covered. | Full fingerprint header decisions remain pending live/official verification. |

> **Design Decision**: Count tokens routes (`/v1/responses/input_tokens`, `/v1/messages/count_tokens`, Gemini `:countTokens`) are explicitly rejected before conversion. This is the current contract until first-class count-token conversion is implemented.

**Current behavior**:
- `/v1/responses/input_tokens`: `requestTypeUnsupported`
- `/v1/messages/count_tokens`: `requestTypeUnsupported`
- `/v1beta/models/{model}:countTokens`: `requestTypeUnsupported`

**Future options** (pick one):
1. **Implement**: Add proper count-tokens conversion paths (requires new IR request type and converter)
2. **Keep rejecting explicitly**: Continue returning 400 `{"error":"unsupported route"}` before adaptor URL rewrite

**Source facts**:
- Exact-route and unsupported-subroute detection at `internal/relay/request_type.go:35-100`
- OpenAI adaptor always uses `/responses`: `internal/relay/provider/openai/adaptor.go:40-43`
- Anthropic adaptor always uses `/messages`: `internal/relay/provider/anthropic/adaptor.go:33-35`
- Gemini URL builder at `internal/relay/provider/gemini/adaptor.go:63-71`

## 23. Unsupported Route Behavior Matrix

Current UAPI path detection explicitly rejects route families that Bifrost supports but UAPI does not yet implement as first-class conversion or passthrough routes. Unsupported detection happens before provider selection, auth forwarding, request conversion, and adaptor URL rewriting; `HandleRelay` returns HTTP 400 with `{"error":"unsupported route"}`.

| Client path | Upstream evidence | Current UAPI detection source | Current behavior | Required behavior |
|---|---|---|---|
| `/v1/responses/input_tokens` | Bifrost exposes `/v1/responses/input_tokens` and OpenAI provider builds `/v1/responses/input_tokens` for `CountTokensRequest` (`upstream/bifrost/docs/openapi/openapi.yaml:178`, `upstream/bifrost/transports/bifrost-http/integrations/openai.go:792-813`, `upstream/bifrost/core/providers/openai/openai.go:4043`). | `internal/relay/request_type.go:35-39` treats only exact `/v1/responses` and `/v1/responses/` as Responses; other `/v1/responses...` paths are unsupported. | Early HTTP 400; no adaptor rewrite to `/responses`. Covered by `internal/relay/request_type_test.go:36`. | Keep rejecting unless a first-class count-tokens route is implemented. |
| `/v1/messages/count_tokens` | Bifrost exposes Anthropic count tokens and provider forwards to `/v1/messages/count_tokens` (`upstream/bifrost/docs/openapi/openapi.yaml:374`, `upstream/bifrost/transports/bifrost-http/integrations/anthropic.go:418-445`, `upstream/bifrost/core/providers/anthropic/anthropic.go:2446`). | `internal/relay/request_type.go:40-44` treats only exact `/v1/messages` and `/v1/messages/` as Messages; subroutes are unsupported. | Early HTTP 400; no adaptor rewrite to `/messages`. Covered by `internal/relay/request_type_test.go:37`. | Keep rejecting unless a first-class count-tokens route is implemented. |
| `/v1/messages/batches...` | Bifrost exposes Anthropic Messages batch create/list/retrieve/cancel/results (`upstream/bifrost/docs/openapi/openapi.yaml:378-385`, `upstream/bifrost/transports/bifrost-http/integrations/anthropic.go:450-657`, `upstream/bifrost/core/providers/anthropic/anthropic.go:1403`). | Same `/v1/messages` exact-route guard at `internal/relay/request_type.go:40-44`. | Early HTTP 400. Covered by `internal/relay/request_type_test.go:38`. | Keep rejecting unless batch routing and batch response contracts are implemented. |
| `/v1beta/models/{model}:countTokens`, `:embedContent`, `:batchEmbedContents`, `:predict`, `:predictLongRunning`, `:batchGenerateContent` | Bifrost Gemini provider recognizes these actions (`upstream/bifrost/core/providers/gemini/types.go:2332-2337`); GenAI routing classifies `:countTokens`, `:embedContent`, `:batchEmbedContents`, `:predict`, `:predictLongRunning`, and `:batchGenerateContent` (`upstream/bifrost/transports/bifrost-http/integrations/genai.go:1155-1158`, `upstream/bifrost/transports/bifrost-http/integrations/genai.go:1186-1202`, `upstream/bifrost/transports/bifrost-http/integrations/genai.go:1354-1375`); provider code builds concrete `:batchEmbedContents`, `:predictLongRunning`, `:batchGenerateContent`, and `:countTokens` URLs (`upstream/bifrost/core/providers/gemini/gemini.go:1209-1210`, `upstream/bifrost/core/providers/gemini/gemini.go:2237-2238`, `upstream/bifrost/core/providers/gemini/gemini.go:2578-2587`, `upstream/bifrost/core/providers/gemini/gemini.go:3979-3981`). | `internal/relay/request_type.go:45-49` calls `isUnsupportedGeminiRoute`; actions are rejected at `internal/relay/request_type.go:89-99`. | Early HTTP 400; no rewrite to `:generateContent` or `:streamGenerateContent`. Covered by `internal/relay/request_type_test.go:50-55`. | Keep rejecting unless each action has an explicit converter/adaptor contract. |
| `/v1beta/cachedContents`, `/upload/v1beta/files`, `/v1beta/files`, `/v1beta/batches`, `/v1beta/operations` | Bifrost GenAI/OpenAPI exposes files, batches, cachedContents, and operations-like batch routes (`upstream/bifrost/docs/openapi/openapi.yaml:413-429`, `upstream/bifrost/transports/bifrost-http/integrations/genai.go:351-546`, `upstream/bifrost/transports/bifrost-http/integrations/genai.go:580-686`, `upstream/bifrost/transports/bifrost-http/integrations/genai.go:931-1137`, `upstream/bifrost/core/providers/gemini/cachedcontents.go:182`, `upstream/bifrost/core/providers/gemini/cachedcontents.go:236`). | `internal/relay/request_type.go:79-87` rejects these resources before Gemini generate detection succeeds. | Early HTTP 400. Covered by `internal/relay/request_type_test.go:56-59`. | Keep rejecting unless lifecycle/file/batch routes are implemented as native route families. |
| `/v1/files`, `/v1/containers`, `/v1/batches` | Bifrost exposes unified/OpenAI file, container, and batch routes (`upstream/bifrost/docs/openapi/openapi.yaml:180-202`, `upstream/bifrost/transports/bifrost-http/integrations/openai.go:1384`, `upstream/bifrost/transports/bifrost-http/integrations/openai.go:1685`, `upstream/bifrost/transports/bifrost-http/integrations/openai.go:2304`). | `internal/relay/request_type.go:50-53` rejects these prefixes before the default Chat fallback. | Early HTTP 400. Covered by `internal/relay/request_type_test.go:46-49`. | Keep rejecting unless route-specific passthrough/conversion is implemented. |

## 24. Stream Event Matrix For UAPI Parsers

### 24.1 Parser and Emitter Architecture

UAPI stream conversion uses an IR-based architecture:
- Each protocol format has a parser (converts upstream SSE to IR StreamEvent)
- Each protocol format has an emitter (converts IR StreamEvent to downstream SSE)
- Conversions are registered at `internal/relay/provider/stream/ir_openai.go:1196-1203` and `ir_anthropic_gemini.go:689-701`.

**Registered parsers:**
| Format | Parser Type | Implementation File:Line |
|--------|-------------|-------------------------|
| `openai_chat` | chatIRParser | `ir_openai.go:13` |
| `openai_responses` / `codex` | responsesIRParser | `ir_openai.go:177` |
| `anthropic` / `claude_code` | anthropicIRParser | `ir_anthropic_gemini.go:11` |
| `gemini` / `gemini_code` / `gemini_cli` / `antigravity` | geminiIRParser | `ir_anthropic_gemini.go:132` |

**Registered emitters:**
| Format | Emitter Type | Implementation File:Line |
|--------|-------------|-------------------------|
| `openai_chat` | chatIREmitter | `ir_openai.go:470` |
| `openai_responses` / `codex` | responsesIREmitter | `ir_openai.go:604` |
| `anthropic` / `claude_code` | anthropicIREmitter | `ir_anthropic_gemini.go:272` |
| `gemini` / `gemini_code` / `gemini_cli` / `antigravity` | geminiIREmitter | `ir_anthropic_gemini.go:461` |

### 24.2 IR Stream Event Types

The IR stream event enum defines these event types (defined in `internal/relay/provider/ir/stream.go:5-27`); each parser emits the subset applicable to its protocol:
- `EventResponseCreated` / `EventMessageStart` - Response/message starts
- `EventContentDelta` / `EventContentPartStart` / `EventContentPartEnd` - Text content and part boundaries
- `EventReasoningStart` / `EventReasoningDelta` / `EventReasoningEnd` - Reasoning/thinking
- `EventToolCallStart` / `EventToolArgDelta` / `EventToolCallEnd` - Tool calls
- `EventItemStart` / `EventItemDone` - Item boundaries
- `EventUsage` - Token usage
- `EventMessageDone` / `EventResponseDone` / `EventDone` - Terminal events
- `EventSafetyBlock` - Safety content
- `EventError` - Error events

### 24.3 Complete Stream Conversion Matrix

**Key:** ✅ = Covered and tested | ❌ = Not covered / missing | ⚠️ = Partially covered

| From (Parser) | To (Emitter) | Status | Test File:Line | Description |
|---------------|--------------|--------|----------------|-------------|
| **Chat Parser** | | | | |
| Chat | → Chat (same) | ❌ | N/A | Does not trigger converter (upstream==client) |
| Chat | → Responses | ✅ | `stream_redesign_test.go:175`, `streaming_test.go:434` | Chat → Responses full lifecycle test |
| Chat | → Anthropic | ✅ | `stream_redesign_test.go:200` | Chat → Anthropic text block test |
| Chat | → Gemini | ✅ | `stream_redesign_test.go:186` | Chat → Gemini tool args accumulation test |
| **Responses Parser** | | | | |
| Responses | → Chat | ✅ | `streaming_test.go:408`, `stream_redesign_test.go:534` | Responses → Chat [DONE] test, error conversion test |
| Responses | → Responses (same) | ❌ | N/A | Does not trigger converter |
| Responses | → Anthropic | ✅ | `stream_redesign_test.go:320` | Responses → Anthropic no duplicate completion text |
| Responses | → Gemini | ✅ | `stream_redesign_test.go:332`, `stream_redesign_test.go:462` | Responses → Gemini no duplicate completion text, reasoning text, function_call arguments, and terminal event test |
| **Anthropic Parser** | | | | |
| Anthropic | → Chat | ✅ | `streaming_test.go:563`, `stream_redesign_test.go:351` | Anthropic → Chat first content event test |
| Anthropic | → Responses | ✅ | `stream_redesign_test.go:415` | Text, tool_call, thinking, usage, and terminal event test |
| Anthropic | → Anthropic (same) | ❌ | N/A | Does not trigger converter |
| Anthropic | → Gemini | ✅ | `stream_redesign_test.go:399` | Anthropic → Gemini preserve signatureDelta |
| **Gemini Parser** | | | | |
| Gemini | → Chat | ✅ | `streaming_test.go:156`, `stream_redesign_test.go:343` | Gemini → Chat first content event test |
| Gemini | → Responses | ✅ | `stream_redesign_test.go:374` | Text, thinking/signature, usage, and terminal event test |
| Gemini | → Anthropic | ✅ | `stream_redesign_test.go:360` | Gemini → Anthropic preserve thoughtSignature |
| Gemini | → Gemini (same) | ❌ | N/A | Does not trigger converter |

### 24.4 Missing Tests Detail

Previously missing tests have been added:

| Conversion Direction | Covered Scenarios | Test File:Line |
|----------------------|-------------------|----------------|
| Gemini → Responses | Text delta, thinking/signature, usage, and completion conversion | `stream_redesign_test.go:374` |
| Anthropic → Responses | Text block, tool_call, thinking, usage, and completion conversion | `stream_redesign_test.go:415` |
| Responses → Gemini | Text delta, function_call_arguments flow, reasoning text, and completion conversion | `stream_redesign_test.go:462` |

### 24.5 Stream Event to Test Mapping

| Parser Event | Chat→Responses | Chat→Anthropic | Chat→Gemini | Responses→Chat | Responses→Anthropic | Responses→Gemini | Anthropic→Chat | Anthropic→Responses | Anthropic→Gemini | Gemini→Chat | Gemini→Anthropic | Gemini→Responses |
|--------------|----------------|----------------|-------------|----------------|---------------------|------------------|----------------|--------------------|------------------|-------------|------------------|------------------|
| text_delta | ✅:175 | ✅:200 | ✅:186 | - | ✅:320 | ✅:332,462 | ✅:351 | ✅:415 | - | ✅:343 | - | ✅:374 |
| reasoning_start/delta | ⚠️:175 | ❌ | ⚠️:186 | - | - | ✅:462 | - | ✅:415 | ✅:399 | - | ✅:360 | ✅:374 |
| tool_call_start | ⚠️:175 | ❌ | ✅:186 | - | ✅:211 | ✅:462 | - | ✅:415 | - | - | ✅:399 | - |
| tool_arg_delta | ⚠️:175 | ❌ | ✅:186 | - | ✅:211 | ✅:462 | - | ✅:415 | - | - | ✅:399 | - |
| usage | ✅:125,137,149 | ❌ | ❌ | ✅:301,326 | ❌ | ❌ | ✅:137 | ✅:415 | ❌ | ✅:149 | ❌ | ✅:374 |
| error | - | - | - | ✅:534 | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | - |
| response_done/completed | ✅:113 | ❌ | ❌ | ✅:408 | ✅:320 | ✅:332,462 | ✅:351 | ✅:415 | ✅:399 | ✅:343 | ✅:360 | ✅:374 |

**Source facts:**
- Stream converter creation: `internal/relay/provider/stream/converter.go:44-50`
- Chat parser events: `internal/relay/provider/stream/ir_openai.go:23-131`
- Responses parser events: `internal/relay/provider/stream/ir_openai.go:194-457`
- Anthropic parser events: `internal/relay/provider/stream/ir_anthropic_gemini.go:29-127`
- Gemini parser events: `internal/relay/provider/stream/ir_anthropic_gemini.go:141-267`
- Chat emitter events: `internal/relay/provider/stream/ir_openai.go:484-590`
- Responses emitter events: `internal/relay/provider/stream/ir_openai.go:629-791`
- Responses emitter usage projection: `internal/relay/provider/stream/ir_openai.go:948-972`
- Anthropic emitter events: `internal/relay/provider/stream/ir_anthropic_gemini.go:298-457`
- Gemini emitter events: `internal/relay/provider/stream/ir_anthropic_gemini.go:472-550`

### 24.6 Required Test Additions

The previously required additions are now covered:

1. **Anthropic → Responses Stream**
   - Covered by `TestDirectAnthropicToResponsesStreamCoversTextToolThinkingAndUsage`.
   - Verifies text block content conversion, tool_call conversion, thinking/reasoning conversion, usage conversion, and `response.completed`.

2. **Responses → Gemini Stream (Enhanced)**
   - Covered by `TestDirectResponsesToGeminiStreamCoversTextFunctionCallAndReasoningText`.
   - Verifies output item flow, function_call_arguments flow, reasoning_text conversion, and Gemini `finishReason`.

3. **Gemini → Responses Stream**
   - Covered by `TestDirectGeminiToResponsesStreamCoversTextThinkingUsageAndTerminal`.
   - Verifies Gemini text, thought text, thoughtSignature, usageMetadata, and `response.completed`.

4. **All conversion edge cases**
   - Empty content, concurrent tool_calls, and broader split reasoning/thinking remain edge-case coverage candidates.
   - Error propagation is covered for Responses→Chat and Chat→Responses by `stream_redesign_test.go:534` and `stream_redesign_test.go:545`.

## 25. Error/Header/Status Matrix

| Area | Current UAPI source fact | Difference from Bifrost | Repair/test requirement |
|---|---|---|---|
| Parse errors | Relay invalid JSON returns HTTP 400 `{"error":"invalid request body"}` at `internal/relay/handler.go:368-371`; same-protocol parse failure returns 400 `normalize request failed` at `handler.go:547-551`; unsupported routes return 400 `{"error":"unsupported route"}` at `handler.go:251-254`. | Bifrost routes send errors through route-level `ErrorConverter` functions (`upstream/bifrost/transports/bifrost-http/integrations/utils.go:193`, `anthropic.go:127-128`, `genai.go:261-262`). | UAPI relay-originated parse/routing errors remain generic JSON. Keep this as explicit UAPI behavior unless a protocol-specific relay error layer is requested. |
| Conversion errors | Cross-protocol request conversion failure returns 400 `convert request failed: ...` at `handler.go:554-558`; response conversion failure returns relay 502 paths in stream/force-stream/buffered flows, including force-stream response conversion at `handler.go:829-833`. | Bifrost has route-level `ErrorConverter` functions, e.g. Anthropic `ToAnthropicChatCompletionError` at `upstream/bifrost/core/providers/anthropic/errors.go:11-29`, Gemini `ToGeminiError` at `upstream/bifrost/core/providers/gemini/errors.go:12-28`. | UAPI conversion errors remain relay generic errors; do not clone Bifrost's full route error converter matrix without a product decision. |
| Upstream error status | Streaming upstream status >=400 reads upstream body, refreshes OAuth once if eligible, otherwise calls `refundOnError` at `handler.go:621-635`; force-stream equivalent is `handler.go:758-783`; buffered path also retries OAuth once at `handler.go:1067-1075` and calls `refundOnError` at `handler.go:1149-1151`. `refundOnError` normalizes the body through `normalizeErrorResponse` at `handler.go:2179-2184`. | Bifrost converts errors through per-route/provider converters. UAPI normalizes only by downstream client protocol family: OpenAI-style, Anthropic-style, or Gemini-style (`handler.go:2055-2068`). | Current behavior is **not raw upstream body passthrough**. It is tested by `internal/relay/error_handling_test.go:11` and `:71`; keep normalized family shapes and 413 rewrite. |
| Response headers | Gateway copies non-hop-by-hop response headers at `internal/gateway/gateway.go:841-857`; relay streaming sets `Content-Type`, `Cache-Control`, `Connection`, `X-Accel-Buffering` at `internal/relay/handler.go:638-643`; force-stream JSON response sets `Content-Type: application/json` at `handler.go:839-840`. | Bifrost adds route metadata headers such as `x-bifrost-provider` and `x-bifrost-request-type` (`upstream/bifrost/transports/bifrost-http/integrations/utils.go:239-243`) and also sets stream buffering headers (`router.go:3056-3060`). | UAPI does not add `x-bifrost-*` routing headers. Keep header copying and streaming headers as current contract. |
| OAuth refresh | UAPI refreshes OAuth credentials after auth-like upstream failures in streaming, force-stream, and buffered paths at `handler.go:621-628`, `758-779`, `1067-1075`; refresh gating is `isOAuthAuthFailure` at `handler.go:1753-1768`; refresh implementation is `handler.go:1720-1745`. | Bifrost provider/auth retry semantics differ by provider. | Auth-failure predicate tests exist at `internal/relay/oauth_auth_failure_test.go:10-28`; keep refresh limited to OAuth accounts with refresh tokens and auth-like 401/403 bodies. |

## 26. Strict Completion Addendum

This addendum closes the gaps called out by the acceptance review. It does not claim that UAPI already implements every Bifrost feature. It makes every remaining ambiguity explicit as one of:

- **Current UAPI behavior**: verified in local UAPI source and tests.
- **Bifrost source fact**: verified in `upstream/bifrost`.
- **Client source fact**: verified in an official/client source tree present under `upstream`.
- **Competitor fact**: verified in competitor source, not an official Antigravity or vendor source.
- **Inference / must verify**: not safe to implement as required behavior without live capture or official source.

Any row marked unsupported, inference, or must verify is a repair contract, not a current implementation guarantee.

## 27. Bifrost Review Line-By-Line Acceptance

`BIFROST_PROTOCOL_CONVERSION_LAYER_REVIEW.md` is accepted as a useful Bifrost summary, not as a complete implementation checklist. The table below is the strict status of each original section against current Bifrost source.

| Review section | Source-backed status | Missing or corrected source fact | Required correction in this spec |
|---|---|---|---|
| Preamble and core architecture, review `1-31` | Accurate with clarification. | Bifrost has **two distinct internal request types** (NOT a single unified IR):<br>- `BifrostChatRequest` for OpenAI Chat Completions at `openai.go:578-584`<br>- `BifrostResponsesRequest` for OpenAI Responses, Anthropic Messages, and Gemini generateContent at `openai.go:727-731`, `anthropic.go:92-99`, `genai.go:124-156`<br>This is architecturally different from UAPI's single IR layer. | **Clarified**: Bifrost keeps Chat and Responses families separate internally. UAPI normalizes ALL protocols to one IR. Do not rewrite UAPI around Chat as "the" central IR — Chat is just one of four protocol families in UAPI's IR. |
| Generic route layer, review `33-69` | Accurate but incomplete. | `RouteConfig` supports batch/file/container/container-file/cached-content converters at `router.go:92-156`, not just four inference route families. Bifrost extra params are gated by `RequestWithSettableExtraParams` and `x-bf-passthrough-extra-params` at `router.go:84-90`. | UAPI must reject or implement unsupported route families explicitly; do not let prefix detection fall through to Chat. |
| OpenAI Chat, review `71-117` | Mostly accurate for Chat. | Review covers Chat endpoints and request/response behavior, but OpenAI dynamic deployment can also dispatch responses/input_tokens, completions, embeddings, audio, and images via `openai.go:312-329` and downstream converters at `openai.go:394-557`. | Section 19.2 is authoritative for route families; Chat implementation work must stay scoped to `/v1/chat/completions` in UAPI. |
| OpenAI Responses, review `119-172` | Accurate for main Responses and count tokens. | Review does not fully expand all server/native Responses item types and fallback-to-Chat losses. Bifrost Responses async exists at `openai.go:743-759`; stream converter emits event names from `resp.Type` at `openai.go:764-779`. | UAPI must document unsupported `/v1/responses/input_tokens`, async, and opaque/server item loss tests separately. |
| Anthropic Messages, review `174-242` | Accurate for main Messages and Claude Code raw gate. | Review mentions files/batch as non-main, but source has separate count tokens route at `anthropic.go:415-448` and batch routes beginning `anthropic.go:450-560`. **Clarification on raw gate**: Claude Code passthrough is gated by UA/model at `anthropic.go:338-374`, but "sanitized" means the provider still executes field deletion, beta header injection, and Vertex body/URL adjustment at `requestbuilder.go:103-196`, `305-315`. Raw body is NOT byte-for-byte transparent — unsupported fields are stripped, empty thinking blocks removed, and beta headers auto-injected. | UAPI must not claim Claude Code is Bifrost raw passthrough. UAPI current `claude_code` is Anthropic-family OAuth emission through `internal/relay/provider/convert/native_protocols.go:36-59`, registered at `native_protocols.go:188-191`. |
| Gemini / GenAI, review `244-322` | Accurate but route-family incomplete. | Dynamic route dispatches countTokens, embeddings, speech, transcription, image, video, batch at `genai.go:124-180`; files are separate at `genai.go:351-380`; cached content starts at `genai.go:931-960`; action classification includes `:countTokens`, `:embedContent`, `:predictLongRunning`, `:batchGenerateContent` at `genai.go:1354-1385`. | UAPI now rejects unsupported Gemini actions/resources before `requestTypeGeminiGenerate` at `internal/relay/request_type.go:45-99`; future work must implement first-class routes before allowing them. |
| IR/loss checklist, review `324-343` | Directionally accurate but not complete enough to drive UAPI repairs. | Review lists representative losses but not a per-target loss contract. UAPI current loss machinery is `internal/relay/provider/convert/registry.go:71-181` and native aliases are in `native_protocols.go:10-200`. | Use sections 28-32 below as the repair checklist, not the original review alone. |

## 28. Bifrost Field-Level Protocol Matrix

This matrix records the Bifrost behavior UAPI must either emulate, reject, or explicitly document as not implemented. It is intentionally field-level, because endpoint coverage alone is not enough.

| Protocol | Field group | Bifrost source fact | UAPI current status | Repair contract |
|---|---|---|---|---|
| OpenAI Chat | Endpoint/method | `POST {pathPrefix}/v1/chat/completions` and `/chat/completions` at `openai.go:561-570`; Azure dynamic `POST {pathPrefix}/openai/deployments/{deploymentPath:*}` at `openai.go:300-325`. | UAPI detects `/v1/chat/completions` only at `internal/relay/request_type.go:30-31`. | Do not add Bifrost `/openai/...` aliases unless server routing explicitly supports them. |
| OpenAI Chat | Request body | Bifrost parses `OpenAIChatRequest` and converts to `BifrostChatRequest` at `openai.go:574-584`; large payload hook is `openAILargePayloadPreHook` at `openai.go:570`. | UAPI parser is `parseOpenAIChatRequestDirectIR` at `internal/relay/provider/convert/openai_chat.go:12-129`; same-protocol only validates and returns cleaned raw body at `registry.go:31-44`. Same-protocol native preservation and cross-protocol loss records are covered at `protocol_redesign_test.go:177-204`, `206-233`. | Keep same-protocol raw validation behavior; do not synthesize Bifrost large-payload metadata unless UAPI implements that route-level hook. |
| OpenAI Chat | Messages/content parts | Review says string/block content and assistant tool calls are preserved at `BIFROST...REVIEW.md:85-93`; OpenAI route builds internal Chat at `openai.go:578-584`. | UAPI maps string, image, file, input_file/input_image/input_text/output_text and reasoning parts at `openai_chat.go:12-129`; PDF/file cross-protocol behavior is tested at `protocol_redesign_test.go:1307-1444`, `1583-1595`. | Keep content-block file/image/PDF conversion tested; audio/video content parts remain unsupported unless schema and target emitters get explicit mappings. |
| OpenAI Chat | Tools/function calling | Bifrost stores Chat parameters including `tools`, `tool_choice`, `response_format`, `reasoning`, `web_search_options`, `prediction`, `service_tier`, `extra_params` per review `BIFROST...REVIEW.md:87-92`. | UAPI converts tools via `irTool` in `openai_chat.go:85`, preserves raw `tool_choice`, and emits function tools in `openai_chat.go:131-424`; precision and native tool fields are tested at `protocol_redesign_test.go:139-175`, and missing tool identifiers hard-error at `protocol_redesign_test.go:379-442`. | Keep non-function tools target-specific: native Chat preserves them for same-protocol; cross-protocol emits only supported function/server-tool projections or records loss. |
| OpenAI Chat | Structured output/json schema | Bifrost carries `response_format` in Chat params per review `BIFROST...REVIEW.md:90`. | UAPI stores `response_format` in `Generation.ResponseFormat` at `openai_chat.go:69` and emits it at `openai_chat.go:172`; Chat-to-Gemini schema mapping is covered at `protocol_redesign_test.go:732-755`. | Add/keep explicit Anthropic and Responses structured-output loss or projection tests when those target-specific semantics change. |
| OpenAI Chat | Multimodal/file/image/audio/PDF | Bifrost has separate media/file/container routes besides Chat: files at `openai.go:1688-1913`, containers at `openai.go:2300-2709`, audio at `openai.go:871-968`, images at `openai.go:971-1123`, videos at `openai.go:1126-1320`. | UAPI media routes are detected separately at `request_type.go:50-69`; conversion IR handles content-block files/images/PDF but not File/Container route families. `/v1/files`, `/v1/containers`, and `/v1/batches` are rejected before default Chat fallback at `request_type.go:50-53`. | Keep these route families rejected unless first-class file/container/batch routes are implemented. |
| OpenAI Chat | Response/stream/error | Chat response converter raw-firsts OpenAI raw response at `openai.go:587-592`; stream raw-first at `openai.go:642-650`; error converter returns `BifrostError` at `openai.go:639-640`. | UAPI response conversion is `response_openai.go:12-278`; stream `[DONE]` is appended only for downstream Chat at `handler.go:1344-1345`. | Generic UAPI relay error shape remains a non-goal for Bifrost parity unless a protocol-shaped error matrix is implemented. |
| OpenAI Responses | Endpoint/method | `POST {pathPrefix}/v1/responses`, `/responses`, `/openai/responses` at `openai.go:710-789`; input tokens at `openai.go:792-805`. | UAPI only accepts exact `/v1/responses` and `/v1/responses/` as Responses at `request_type.go:35-39`; `/v1/responses/input_tokens` and other subroutes are rejected before adaptor URL rewrite. The adaptor uses `/responses` for accepted Responses/Codex requests at `openai/adaptor.go:40-43`. | Keep subroutes explicitly rejected unless a first-class `/v1/responses/input_tokens` route is implemented. |
| OpenAI Responses | Request body/input | Bifrost converts `OpenAIResponsesRequest` to `BifrostResponsesRequest` at `openai.go:727-731`; count tokens reuses request into `CountTokensRequest` at `openai.go:813-818`. | UAPI parser is `parseOpenAIResponsesRequestDirectIR` at `openai_responses.go:12-200`; emitter is `emitOpenAIResponsesRequestDirectIR` at `openai_responses.go:201-329`; string/message/function_call/function_call_output/opaque item coverage is in `ir_items_test.go:10-85`, `protocol_redesign_test.go:1669-1715`. | Keep unknown Responses items preserved as native opaque IR with loss records for cross-protocol audit. |
| OpenAI Responses | Server tools/opaque items | Bifrost allows Responses provider path and may fallback unsupported Responses to Chat per review `BIFROST...REVIEW.md:172`. | UAPI preserves Responses-family raw turns at `openai_responses.go:216-221`; response opaque raw output can re-emit at `response_openai.go:452-476`. Tests cover `file_search_call`, `namespace`, MCP projection, and image generation output at `ir_items_test.go:64-209`, `response_openai_images_test.go:1-85`. | Add additional fixture rows when new Responses server-tool item types are accepted; otherwise preserve as native opaque/loss. |
| OpenAI Responses | Streaming/SSE | Bifrost stream converter returns `string(resp.Type)` and `resp.WithDefaults()` at `openai.go:764-779`; no OpenAI Chat `[DONE]` per review `BIFROST...REVIEW.md:167-170`. | UAPI stream parser/emitter tests cover representative events at `stream_redesign_test.go:101-170`, `211-270`, `320-339`, `437-453`. | Fill event coverage gaps listed in section 24 for every downstream target. |
| Anthropic Messages | Endpoint/method | `POST {pathPrefix}/v1/messages` and wildcard at `anthropic.go:74-84`; count tokens at `anthropic.go:415-448`; batches start at `anthropic.go:450-560`. | UAPI only accepts exact `/v1/messages` and `/v1/messages/` as Anthropic Messages at `request_type.go:40-44`; `/v1/messages/count_tokens`, `/v1/messages/batches`, and other subroutes are rejected before adaptor URL rewrite. The adaptor sends `/messages` for accepted requests at `anthropic/adaptor.go:33-35`. | Keep subroutes explicitly rejected unless first-class count-token/batch/file routes are implemented. |
| Anthropic Messages | Request body/messages | Bifrost converts `AnthropicMessageRequest` to `BifrostResponsesRequest` and normalizes input blocks at `anthropic.go:92-99`. | UAPI parser is `parseAnthropicRequestDirectIR` at `anthropic.go:12-163`; emitter is `emitAnthropicRequestDirectIR` at `anthropic.go:164-748`; tool_result block parsing and document/PDF conversions are covered at `anthropic_test.go:23-41`, `protocol_redesign_test.go:1460-1475`, `1599-1617`. | Keep unmatched/invalid tool_result hard errors unless a target can safely degrade to text. |
| Anthropic Messages | Thinking/cache | Bifrost maps Anthropic thinking/display/effort and cache_control per review `BIFROST...REVIEW.md:198-204`, `227-232`. | UAPI maps thinking/redacted_thinking in `anthropic.go:125-128`; cache usage parsing in `internal/relay/provider/anthropic/adaptor.go:65-89`; thinking/cache conversion tests are at `protocol_redesign_test.go:1169-1178`, `1452-1469`, `1526-1581`. | Keep request-side cache_control loss records when emitted to non-Anthropic targets. |
| Anthropic Messages | Claude Code raw | Bifrost raw gate requires Claude Code request and Claude model at `anthropic.go:338-374`; safe headers are `anthropic.go:186-190`; provider sanitizer is `requestbuilder.go:103-196`, `305-315`. | UAPI `claude_code` maps to Anthropic-family parser/emitter at `native_protocols.go:36-59`; OAuth headers are in `anthropic/adaptor.go:38-51`. | Do not implement raw passthrough by copying Bifrost gate unless tests prove sanitized raw field behavior. |
| Gemini / GenAI | Endpoint/method | Dynamic `POST {pathPrefix}/v1beta/models/{model:*}` at `genai.go:103-106`; file upload at `genai.go:351-380`; cached contents at `genai.go:931-960`; classification at `genai.go:1355-1385`. | UAPI accepts Gemini generate/stream-generate family paths under `/v1beta/`, but rejects unsupported Gemini resources/actions before conversion: `/upload/v1beta/files*`, `/v1beta/files*`, `/v1beta/cachedContents*`, `/v1beta/batches*`, `/v1beta/operations*`, `:countTokens`, `:embedContent`, `:batchEmbedContents`, `:predict`, `:predictLongRunning`, and `:batchGenerateContent` at `request_type.go:44-99`. Accepted generate requests are rewritten by the standard adaptor to `/models/{model}:generateContent` or stream at `gemini/adaptor.go:63-70`. | Keep unsupported GenAI actions/resources rejected unless each gets an explicit converter/adaptor contract. |
| Gemini / GenAI | Request body/content parts | Bifrost handles systemInstruction, contents, inlineData, fileData, functionCall, functionResponse, thought, thoughtSignature per review `BIFROST...REVIEW.md:273-283`. | UAPI parser is `parseGeminiRequestDirectIR` in `internal/relay/provider/convert/gemini.go`; request-side `thoughtSignature` is preserved at `gemini.go:109-135`; functionResponse native/loss tests are at `ir_items_test.go:234-288`, and stream signature tests are at `stream_redesign_test.go:360-397`. | Keep same-protocol Gemini native parts raw-preserved and cross-protocol functionResponse extras recorded as loss. |
| Gemini / GenAI | Cache/safety/tools | Bifrost maps `safetySettings` and `cachedContent` through ExtraParams per review `BIFROST...REVIEW.md:281-299`; provider code maps `cached_content` back into Gemini `CachedContent` at `upstream/bifrost/core/providers/gemini/responses.go:145-153`. Gemini CLI hoists SDK `cachedContent` to the REST root at `upstream/gemini-cli/packages/core/src/utils/apiConversionUtils.ts:24-54`. | UAPI IR has `Cache.CachedContent` at `ir/types.go:56-63`; Gemini parser lifts top-level `cachedContent` at `gemini.go:42-43`, and the Gemini emitter writes it back at `gemini.go:265-266`. Safety settings are carried in `Safety.Settings`. | Covered by `TestGeminiCachedContentMapsThroughIR`; keep cached-content CRUD routes explicitly unsupported unless a route-family implementation is added. |
| Gemini / GenAI | Streaming/SSE | Bifrost stream converter stores `BifrostToGeminiStreamState` at `genai.go:264-286`; GenAI stream does not append `[DONE]`. | UAPI Gemini stream parser accepts Code Assist and multi-response `response` envelopes at `stream/ir_anthropic_gemini.go:154-172`; tests cover wrapper, cache usage, signatures, and Gemini-to-Responses terminal usage at `stream_redesign_test.go:149-155`, `310-318`, `360-397`. | Keep GenAI stream terminal behavior without `[DONE]`; add Antigravity-specific wrapper error tests if that native envelope begins emitting protocol-specific error bodies. |

## 29. OAuth / Native Channel Strict Evidence Matrix

The following table replaces broad OAuth guidance with source category and implementation status. If the source category is competitor or inference, it is not a UAPI requirement until verified or intentionally adopted.

| Channel | Current UAPI implementation | Official/client source facts | Competitor facts | Current contract |
|---|---|---|---|---|
| Codex | `openai` + `api_format=codex` selects `FormatCodexResponses` at `internal/relay/handler.go:470-478`; URL switches platform base to `https://chatgpt.com/backend-api/codex` and sends `/responses` at `internal/relay/provider/openai/adaptor.go:32-43`; headers are `Authorization`, `originator`, `User-Agent`, optional `ChatGPT-Account-ID`, optional `X-OpenAI-Fedramp`, `Content-Type` at `openai/adaptor.go:56-70`; OAuth constants/scopes are `openai/auth.go:22-35`, auth URL params at `openai/auth.go:106-120`. | Official/client Codex auth query uses scope `openid profile email offline_access api.connectors.read api.connectors.invoke`, PKCE S256, `id_token_add_organizations`, `codex_cli_simplified_flow`, `originator` at `upstream/codex/codex-rs/login/src/server.rs:491-508`; official Responses client path ends with `/responses` at `upstream/codex/codex-rs/codex-api/tests/clients.rs:254-272`; ChatGPT backend cookie tests target `/backend-api/codex/responses` at `upstream/codex/codex-rs/codex-client/src/chatgpt_cloudflare_cookies.rs:130-205`. | cockpit-tools allows/injects additional Codex headers at `upstream/cockpit-tools/src-tauri/src/modules/codex_local_access.rs:133-140`, `7441-7467`. | UAPI must keep current smaller header set unless official/client source requires more. Competitor-only empty headers are optional, not mandatory. Current URL/auth/header contract is covered by `internal/relay/provider/oauth_fingerprint_test.go`, `internal/relay/provider/openai/channel_contract_test.go`, and `internal/relay/provider/openai/adaptor_test.go`. |
| Claude Code | `anthropic` + `api_format=claude_code` selects `FormatClaudeCode` at `handler.go:479-484`; OAuth inference headers are `Authorization`, `anthropic-beta: oauth-2025-04-20`, `x-app: cli`, `User-Agent: claude-cli/2.1.156 (external, cli)`, `X-Claude-Code-Session-Id`, `anthropic-version`, `Content-Type` at `internal/relay/provider/anthropic/adaptor.go:38-51`; auth constants/scopes are `anthropic/auth.go:22-38`. | `claude-code-source-1/src/utils/userAgent.ts:8-10`, `claude-code-source-2/src/utils/userAgent.ts:8-10`, and `claude-code-source-3/src/utils/userAgent.ts:8-10` all define `claude-code/${VERSION}` helper. OAuth scopes/beta/token/profile endpoints are in `upstream/claude-code-source-2/src/constants/oauth.ts:33-93`. Source-3 also contains remote OAuth/session-ingress behavior such as token file descriptor handling at `upstream/claude-code-source-3/src/utils/authFileDescriptor.ts:169-177` and upstream proxy session token handling at `upstream/claude-code-source-3/src/upstreamproxy/upstreamproxy.ts:96-134`. | CLIProxyAPI/cockpit sidecar derives device/runtime/package headers at `upstream/cockpit-tools/sidecars/cockpit-cliproxy/cdk/CLIProxyAPI/internal/runtime/executor/helps/claude_device_profile.go:209-354` and reads client UA/entrypoint at `.../claude_executor.go:1484-1516`. | UAPI currently implements OAuth inference headers, not full Claude device profile mirroring. The official source confirms `claude-code/${VERSION}` helper; the `claude-cli/... (external, cli)` header is UAPI's current inference contract. Do not add Stainless/runtime/package headers without official release source or live capture. Current auth/header/session-id contract is covered by `internal/relay/provider/oauth_fingerprint_test.go` and `internal/relay/provider/anthropic/adaptor_test.go`. |
| Gemini CLI / Code Assist | `gemini` + `api_format=gemini_code` selects `FormatGeminiCode` at `handler.go:485-490`; Code Assist URL is `/v1internal:generateContent` or stream at `internal/relay/provider/gemini/adaptor.go:36-61`; OAuth/API-key split is `gemini/adaptor.go:73-80`; Code Assist envelope and session id are `internal/relay/provider/gemini/codeassist_convert.go:17-44`, `89-110`; auth constants are `gemini/auth.go:20-33`, and UAPI browser OAuth omits PKCE unless a challenge is passed at `gemini/auth.go:59-74`. | Gemini CLI core calls `generateContentStream` with `{model, contents, config}` at `upstream/gemini-cli/packages/core/src/core/geminiChat.ts:851-858`; SDK test data has `generateContentStream` envelope events at `upstream/gemini-cli/packages/sdk/test-data/tool-success.json:1-8`; SDK-to-REST conversion hoists `systemInstruction`, tools, safety, and `cachedContent` to REST root at `upstream/gemini-cli/packages/core/src/utils/apiConversionUtils.ts:39-56`. | CLIProxyAPI exposes Gemini CLI internal endpoints and model/signature knobs at `upstream/CLIProxyAPI/config.example.yaml:132-151`, model aliases/exclusions at `config.example.yaml:333-395`. | UAPI `gemini_code` is a Code Assist/v1internal transport compatible with Gemini CLI-like semantics, not a byte-for-byte CLI process clone. Current URL, UA, Authorization, envelope, `session_id`, and stream wrapper contracts are covered by `internal/relay/provider/gemini/adaptor_test.go`, `internal/relay/provider/gemini/codeassist_convert_test.go`, and stream tests in `internal/relay/provider/convert/stream_redesign_test.go`. |
| Antigravity | `antigravity` selects `FormatAntigravity` at `handler.go:491-492`; inference URL and headers are `internal/relay/provider/antigravity/adaptor.go:95-115`; request envelope/model routing/schema sanitation are `adaptor.go:117-164`; auth constants/dynamic endpoints are `antigravity/auth.go:20-39`, auth URL at `auth.go:146-168`; image special path is `internal/relay/antigravity_images.go:32-125` with tests `internal/relay/antigravity_images_test.go:13-122`. | No official Antigravity source exists in this repository. | Antigravity-Manager documents v1internal `googleSearch` + `functionDeclarations` conflict at `upstream/Antigravity-Manager/README.md:492-493`, `thinkingLevel` to `thinkingBudget` at `README.md:616-619`, OAuth exchange UA at `README.md:625-627`, request fingerprint headers at `README.md:682-684`. CLIProxyAPI sets Antigravity userinfo UA at `upstream/CLIProxyAPI/internal/auth/antigravity/auth.go:187-193`, loadCodeAssist UA at `auth.go:238-247`, and `X-Goog-Api-Client` during onboarding at `auth.go:319-323`. | Current UAPI inference contract is only `Content-Type`, bearer `Authorization`, and `User-Agent`. Competitor fingerprint headers remain competitor facts. Current URL/header/envelope/thinking/conflict/image contracts are covered by `internal/relay/provider/antigravity/auth_test.go`, `internal/relay/provider/convert/protocol_redesign_test.go`, and `internal/relay/antigravity_images_test.go`. |

## 30. UAPI Provider Capability And Unsupported Contract

This table is the current implementation contract for standard API and OAuth-native upstreams. It is the source of truth for the next AI when deciding whether to convert, raw-forward, reject, or record loss.

| Upstream format | Selected by | Request URL/header source | Same-format behavior | Cross-format behavior | Unsupported boundaries |
|---|---|---|---|---|---|
| `openai_chat` | channel `type=openai`, `api_format` empty/other at `handler.go:470-478` | URL `/chat/completions` at `openai/adaptor.go:40-43`; API key bearer and JSON content type at `openai/adaptor.go:56-70`. | `NormalizeRequestSameProtocol` validates OpenAI Chat parser and returns cleaned raw JSON at `registry.go:31-44`. | `ConvertRequestWithAdaptor` parses client format to IR and emits Chat at `handler.go:546-555`; Chat emitter hard-errors missing tool call name/id or tool result id per section 5.3. | No OpenAI files/containers/batches conversion; media routes are separate passthrough/special paths, while `/v1/files`, `/v1/containers`, and `/v1/batches` are explicitly unsupported at `request_type.go:50-53`. |
| `openai_responses` | channel `type=openai`, `api_format=responses` at `handler.go:471-475` | URL `/responses` at `openai/adaptor.go:40-43`; standard bearer headers. | Same-protocol preserves raw-ish body after validation. | Responses parser/emitter at `openai_responses.go:12-329`; opaque/native output preservation at `response_openai.go:452-476`. | `/v1/responses/input_tokens` and other Responses subroutes are explicitly unsupported at `request_type.go:35-39`. |
| `codex` | channel `type=openai`, `api_format=codex` at `handler.go:471-473` | Base URL rewrite and `/responses` at `openai/adaptor.go:32-43`; Codex headers at `openai/adaptor.go:56-70`. | Same-format validates with Codex parser alias over Responses at `native_protocols.go:10-34`; Codex requests are normalized after conversion at `handler.go:562-564`. | Parses/emits Responses-family IR with Codex protocol marking at `native_protocols.go:10-34`. | Forced stream can alter request shape at `handler.go:507-510`; current Codex URL/header/native identity contracts are tested, forced-stream response back-conversion remains a targeted gap. |
| `anthropic` | channel `type=anthropic`, `api_format` empty/other at `handler.go:479-483` | URL `/messages` at `anthropic/adaptor.go:33-35`; API key or OAuth headers at `anthropic/adaptor.go:38-51`. | Same-format validates with Anthropic parser and returns raw-ish JSON. | Anthropic parser/emitter at `anthropic.go:12-748`; response parser/emitter at `response_anthropic.go:11-225`. | `/v1/messages/count_tokens`, `/v1/messages/batches`, and other Messages subroutes are explicitly unsupported at `request_type.go:40-44`. |
| `claude_code` | channel `type=anthropic`, `api_format=claude_code` at `handler.go:479-481` | Same URL as Anthropic and OAuth headers at `anthropic/adaptor.go:38-51`. | Native alias over Anthropic at `native_protocols.go:36-59`; same native family preserves more fields at `registry.go:183-188`. | Emits Anthropic wire body with Claude Code protocol marker. | Not Bifrost raw gate; no device profile mirroring. |
| `gemini` | channel `type=gemini`, `api_format` empty/other at `handler.go:485-489` | Standard Gemini URL rewrite to `/models/{model}:generateContent` or stream at `gemini/adaptor.go:63-70`; API key query or OAuth bearer at `gemini/adaptor.go:73-80`. | Gemini same-format suppresses model injection via `rawGeminiSameFormat` at `handler.go:497-510`. | Gemini parser/emitter and stream parser handle standard GenAI shape. | Unsupported GenAI resources/actions are explicitly rejected at `request_type.go:45-99`; only generate/stream-generate family paths enter conversion. |
| `gemini_code` | channel `type=gemini`, `api_format=gemini_code` at `handler.go:485-487` | Code Assist `/v1internal:generateContent` or stream at `gemini/adaptor.go:36-61`; bearer OAuth. | Native alias over Gemini CLI envelope at `native_protocols.go:62-86`. | Emits Code Assist envelope through `gemini/codeassist_convert.go:17-44` and adaptor account context. | Not full Gemini CLI process clone; URL/header/envelope/session/stream wrapper contracts are covered by tests. |
| `antigravity` | channel `type=antigravity` at `handler.go:491-492` | Antigravity URL/header at `antigravity/adaptor.go:95-115`. | Native parser/emitter aliases Gemini CLI envelope with Antigravity protocol at `native_protocols.go:88-127`. | Emits Antigravity envelope with model routing, project, sessionId, function calling validation, schema sanitation at `antigravity/adaptor.go:117-170`. | Competitor fingerprint headers are not current UAPI inference requirements; thinkingLevel -> thinkingBudget conversion is implemented at `antigravity/adaptor.go:647-655` and tested for Antigravity v1internal. |

## 31. Explicit Unsupported Route Contract

Until these are implemented, UAPI rejects them before conversion instead of allowing prefix fallback. This is the current source contract.

| Client route | Current source behavior | Required contract | Test coverage |
|---|---|---|---|
| `POST /v1/responses/input_tokens` | Detected as unsupported by the exact `/v1/responses` guard at `request_type.go:35-39`; `HandleRelay` returns 400 before adaptor URL rewrite at `handler.go:250-254`. | Keep unsupported or implement CountTokensRequest as a first-class route. | `TestDetectRelayRequestTypeRejectsUnsupportedConversionRoutes`; `TestHandleRelayRejectsUnsupportedRoutesBeforeAuthOrAdaptor`. |
| `POST /v1/messages/count_tokens` | Detected as unsupported by the exact `/v1/messages` guard at `request_type.go:40-44`; no `/messages` upstream rewrite occurs. | Keep unsupported or implement Anthropic count_tokens as a first-class route. | Same request-type and handler tests. |
| `/v1/messages/batches...` | Detected as unsupported by the exact `/v1/messages` guard at `request_type.go:40-44`. | Keep unsupported unless batch support is implemented. | `TestDetectRelayRequestTypeRejectsUnsupportedConversionRoutes`. |
| `/v1beta/models/{model}:countTokens` | `isUnsupportedGeminiRoute` rejects `:countTokens` at `request_type.go:89-99`; no standard Gemini generate rewrite occurs. | Keep unsupported or implement countTokens mapping. | Request-type and handler tests. |
| `/v1beta/models/{model}:embedContent`, `:batchEmbedContents`, `:predict`, `:predictLongRunning`, `:batchGenerateContent` | `isUnsupportedGeminiRoute` rejects these actions at `request_type.go:89-99`. | Keep unsupported or implement media/embedding/video/batch families. | Per-action cases in `TestDetectRelayRequestTypeRejectsUnsupportedConversionRoutes`. |
| `/v1beta/cachedContents`, `/upload/v1beta/files`, `/v1beta/batches` | `isUnsupportedGeminiRoute` rejects these resources at `request_type.go:79-87`; `/v1beta/operations*` is also rejected. | Keep unsupported or implement Bifrost-like file/cache/batch route families. | Cached/file/batch/operations cases in `TestDetectRelayRequestTypeRejectsUnsupportedConversionRoutes`. |
| `/v1/files`, `/v1/containers`, `/v1/batches` | Detected as unsupported before default Chat fallback at `request_type.go:50-53`. | Keep unsupported unless OpenAI file/container/batch passthrough or conversion is implemented. | Request-type and handler tests. |

## 32. Required Verification And Test Matrix

This is the current verification matrix. Rows marked partial are not permission to silently coerce behavior; they identify where future changes must add tests before widening behavior.

| Area | Required test | Current coverage | Remaining gap |
|---|---|---|---|
| Same-protocol raw-ish | Unknown top-level fields remain in same-format raw body for Chat, Responses, Anthropic, Gemini; undefined placeholders are cleaned; parser failures return 400. | Same-protocol production and converter tests: `plan_contracts_test.go:93-122`, `protocol_redesign_test.go:139-207`, `483-572`, `request_type_test.go:68-85`. | Add explicit handler-level parser-failure 400 tests per protocol if same-protocol sanitizer behavior changes. |
| Cross-protocol native loss | Unknown top-level fields and `Generation.Extra` become warning loss records and are not emitted across non-native-family protocols. | Loss tests: `native_protocols_test.go:67-99`, `native_protocols_test.go:195-232`, `protocol_redesign_test.go:177-232`, `1483-1501`. | Add pair-specific native-extra loss assertions when new native fields are introduced. |
| OpenAI Chat | Missing tool call id/name and missing tool result id are hard errors; file URL/id/data behavior is asserted. | Hard-error tests at `protocol_redesign_test.go:381-447`; file/PDF tests at `1309-1414`, `1585-1629`, `1630-1699`. | Audio/video content-part policy remains route-specific and should be tested only if Chat schema starts accepting them. |
| OpenAI Responses | `file_search_call`, `image_generation_call`, MCP approval, namespace, code interpreter, unknown output item preserve/loss behavior for every target. | Ordered/native/opaque and namespace/MCP tests at `ir_items_test.go:10-190`, same-protocol Codex opaque test at `native_protocols_test.go:104-130`, image generation output tests in `response_openai_images_test.go`, pairwise JSON matrix in `protocol_matrix_test.go`. | MCP approval and code-interpreter item assertions are still not exhaustive for every target; add targeted tests before changing those item mappers. |
| Anthropic | System array, document URL/file/base64/text, citations, `thinking`, `redacted_thinking`, `cache_control`, unmatched tool result, MCP/server tools. | Anthropic/Claude Code tool tests in `anthropic_test.go`; reasoning/cache/file tests in `protocol_redesign_test.go:1171-1280`, `1462-1583`; unmatched tool-use safety in `provider_conversion_test.go:148-165`; usage tests in `anthropic/adaptor_test.go`. | Document citations, unmatched tool_result, and server-tool/MCP coverage remain partial. |
| Gemini | `inlineData`, `fileData`, functionCall, functionResponse, thought/thoughtSignature, `cachedContent`, `safetySettings`, `thinkingConfig`, action route rejection. | Gemini file/cache/functionResponse/signature/loss tests in `protocol_redesign_test.go:301-379`, `1380-1414`, `1558-1629`; `ir_items_test.go:234-304`; `provider_conversion_test.go:22-97`; route rejection in `request_type_test.go:34-85`. | Add non-stream multi-turn `functionResponse` plus missing `thoughtSignature` boundary tests before tightening Gemini state validation. |
| OAuth Codex | Base URL rewrite, `/responses`, `originator`, UA, optional account/FedRAMP headers, forced stream, response back-conversion. | URL/header/auth tests: `openai/channel_contract_test.go`, `openai/adaptor_test.go`, `openai/refresh_test.go`, `oauth_fingerprint_test.go`; Codex normalization tests in `request_type_test.go:109-190`. | Forced-stream response back-conversion remains a targeted gap. |
| OAuth Claude Code | OAuth headers, beta header, x-app, UA, session id, no Bifrost raw-gate behavior. | `oauth_fingerprint_test.go`, `anthropic/adaptor_test.go`, native identity tests. | Device/runtime/package header mirroring remains intentionally unsupported without official/live evidence. |
| OAuth Gemini Code | `/v1internal` URL, Authorization, Gemini CLI UA, envelope fields, session id, stream wrapper. | `gemini/adaptor_test.go`, `gemini/codeassist_convert_test.go`, `stream_redesign_test.go:149-158`, `310-318`, `protocol_redesign_test.go:301-379`. | Add multi-response Code Assist envelope tests if upstream envelope format changes. |
| OAuth Antigravity | URL, Authorization, UA, envelope model routing, project, sessionId, function calling VALIDATED, schema sanitation, image special path. | `antigravity/auth_test.go`, `protocol_redesign_test.go:859-940`, `1051-1113`, `antigravity_images_test.go:13-122`, model routing tests in `models_catalog_test.go`. | Competitor fingerprint headers remain competitor-only and not required. |
| Streaming: Anthropic -> Responses | Text block, tool_call, thinking, usage full event sequence test. | Covered by `stream_redesign_test.go:415-448`. | None for this listed scenario. |
| Streaming: Responses -> Gemini | output_item full sequence, function_call_arguments flow, reasoning_text events. | Covered by `stream_redesign_test.go:462-488`; encrypted reasoning covered at `450-459`. | Add more split-delta/concurrent tool-call cases if stream parser state machine changes. |
| Streaming baseline | Every accepted parser event in section 24 to Chat, Responses, Anthropic, Gemini targets; no `[DONE]` except downstream Chat. | Relay and converter stream tests in `streaming_test.go` and `stream_redesign_test.go`; non-Chat no-DONE covered by relay streaming tests. | Error propagation coverage is strongest for Responses/Chat; Anthropic/Gemini error-event cross-target tests remain partial. |
| Errors/headers | Invalid JSON, conversion error, upstream error, OAuth refresh failure/success, copied response headers, streaming headers. | Error normalization and OAuth auth failure tests: `error_handling_test.go`, `oauth_auth_failure_test.go`, `account_refresh_test.go`; streaming error conversion tests in `stream_redesign_test.go:534-555`. | Header-copy and invalid-JSON handler tests remain partial outside the specific covered paths. |

## 33. Final Acceptance Rule For Future Reviews

Future reviewers must fail this spec if any repair instruction treats a competitor fact or inference as a current UAPI requirement without a source-backed decision. Conversely, they must also fail it if UAPI silently routes unsupported Bifrost route families into Chat/Gemini generation fallback. The acceptable states are:

- Implemented and covered by tests.
- Explicitly unsupported with deterministic error behavior and tests.
- Competitor/inference-only and marked as non-requirement until verified.

Anything else is incomplete.
