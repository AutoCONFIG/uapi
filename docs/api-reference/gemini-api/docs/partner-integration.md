# Partner and library integrations

This guide outlines architectural strategies for building libraries, platforms,
and gateways on top of the Gemini API. It details the technical trade-offs
between using the official GenAI SDKs, the Direct API (REST/gRPC), and the
OpenAI compatibility layer.

Use this guide if you are building tools for other developers, such as
open-source frameworks, enterprise gateways, or SaaS aggregators, and need to
optimize for dependency hygiene, bundle size, or feature parity.

## What is partner integration?

> [!CAUTION]
> **Caution:** If you are building an end-user application (e.g., a customer support bot, a travel planner), skip this guide and use the [Gemini API
> Quickstart](https://ai.google.dev/gemini-api/docs/quickstart). The [GenAI
> SDK](https://ai.google.dev/gemini-api/docs/libraries) is the recommended choice for you.

A partner is anyone building an integration between the Gemini API and end-user
developers. We categorize partners into four archetypes. Identifying which one
you match most closely will help you choose the right integration path.

#### Ecosystem framework

- **Who you are:** Maintainer of an open-source framework (e.g., LangChain, LlamaIndex, Spring AI) or language-specific clients.
- **Your goal:** Broad compatibility. You want your library to work in any environment your user chooses without forcing conflicts.

#### Runtime and edge platform

- **Who you are:** SaaS platforms, AI Gateways, or cloud infrastructure providers (e.g., Vercel, Cloudflare, Zapier) where code execution happens in restricted environments.
- **Your goal:** Performance. You need low latency, minimal bundle size, and rapid cold starts.

#### Aggregator

- **Who you are:** Platforms, proxies, or internal "Model Gardens" that normalize access across many different LLM providers (e.g. OpenAI, Anthropic, Google) into a single interface.
- **Your goal:** Portability and uniformity.

#### Enterprise gateway

- **Who you are:** Internal Platform Engineering teams at large companies building "Golden Paths" for hundreds of internal developers.
- **Your goal:** Standardization, governance, and unified authentication.

## Comparison at a glance

**Global best practice:** All partners must send the [`x-goog-api-client`
header](https://ai.google.dev/gemini-api/docs/partner-integration#client-id) regardless of the path chosen.

| If you are... | Recommended path | Key benefit | Key trade-off | Best practice |
|---|---|---|---|---|
| **Enterprise gateway, ecosystem framework** | **[Google GenAI SDK](https://ai.google.dev/gemini-api/docs/partner-integration#genai-sdk)** | **Gemini Enterprise Agent Platform parity \& speed.** Built-in handling for types, auth, and complex features (e.g., file uploads). Seamless migration to Google Cloud. | **Dependency weight.** Transitive dependencies can be complex and outside of your control. Limited to supported languages (Python/Node/Go/Java). | **Lock versions.** Pin SDK versions in your internal base images to ensure stability across teams. |
| **Ecosystem framework, edge platforms, and aggregators** | **[Direct API](https://ai.google.dev/gemini-api/docs/partner-integration#rest)** *(REST / gRPC)* | **Zero dependencies.** You control the HTTP client and exact bundle size. Full access to all API and model features. | **High developer overhead.** JSON structures can be deeply nested and require strict manual validation and type-checking. | **Use OpenAPI specs.** Automate type generation using our official specs rather than writing them by hand. |
| **Aggregator using OpenAI SDKs that only require text-based workflows** *(Optimizing for legacy portability)* | **[OpenAI compatibility](https://ai.google.dev/gemini-api/docs/partner-integration#openai)** | **Instant portability.** Reuse existing OpenAI-compatible code or libraries. | **Feature ceiling.** Model-specific features (Native video, Caching) may not be available. | **Migration plan.** Use this for rapid validation, but plan to upgrade to Direct API for complete API feature. |

## Google GenAI SDK integration

For frameworks, implementing the [Google GenAI SDK](https://ai.google.dev/gemini-api/docs/libraries)
is often the simplest path, given the fewest lines of code in supported
languages.

For internal platform teams, your primary deliverable is often a "golden path"
that allows product engineers to move fast while complying with security
policies.

**Benefits:**

- **Unified interface for Gemini Enterprise Agent Platform migration:** Internal developers often prototype using API Keys (Gemini API) and deploy to Gemini Enterprise Agent Platform (IAM) for production compliance. The SDK abstracts these authentication differences. Similarly for frameworks, you can implement one codepath and support two sets of users.
- **Client-side helpers:** The SDK includes idiomatic utilities that reduce boilerplate for complex tasks.
  - *Examples:* Supporting `PIL` image objects directly in prompts, automatic function calling, and comprehensive types.
- **Day-zero feature access:** New API features are available at launch-time through the SDKs.
- **Improved code generation support:** Local SDK installation exposes type definitions and docstrings to coding assistants (e.g., Cursor, Copilot). This context improves code generation accuracy compared to generating raw REST requests.

**The trade-off:**

- **Dependency weight \& complexity:** The SDKs have their own dependencies, which can increase bundle size and potentially supply-chain risk.
- **Versioning:** New API features are often pinned to minimum SDK versions. You may need to push updates to users to access new features or models, which in some cases may require changes in transitive dependencies that affect your users.
- **Protocol limits:** The SDKs only support HTTPS for the main API and WebSockets (WSS) for the Live API. gRPC is not supported using the high-level SDK clients.
- **Language support:** The SDKs support *current* language versions. If you need to support EOL versions (e.g., Python 3.9), you would need to maintain a fork.

**Best practice:**

- **Lock versions:** Pin the SDK version in your internal base images to ensure stability across teams.

## Direct API integration

If you are distributing a library to thousands of developers, running in a
constrained environment, or building an aggregator that requires Gemini's
bleeding-edge features, you may need to integrate with the API directly using
REST or gRPC.

**Benefits:**

- **Full feature access:** Unlike the OpenAI compatibility layer, using the API directly enables Gemini-specific features, such as uploading to the File API, creating content caching and using the bi-directional Live API.
- **Minimal dependencies:** In an environment where dependencies are sensitive due to size or auditing costs. Using the API directly through a standard library like `fetch` or through a wrapper like `httpx` ensures your library remains lightweight.
- **Language agnostic:** This is the only path for languages not covered by the SDKs, such as Rust, PHP, and Ruby, as there are no language restrictions.
- **Performance:** The Direct API has zero initialization overhead, minimizing cold starts in serverless functions.

**The trade-off:**

- **Manual Gemini Enterprise Agent Platform implementation:** Unlike the SDK, using the API directly does not automatically handle the authentication differences between AI Studio (API Key) and Gemini Enterprise Agent Platform (IAM). You must implement separate auth handlers if you want to support both environments.
- **No native types or helpers:** You do not get code completions or compile- time checks for request objects unless you implement them yourself. There are no client "helpers" (e.g., function-to-schema converters), so you must manually write this logic yourself.

**Best practice**

We expose a machine-readable specification that you can use to generate type
definitions for your library, saving you from writing them by hand. Download the
spec during your build process, generate the types, and ship the compiled code.

- **Endpoint:** `https://generativelanguage.googleapis.com/$discovery/OPENAPI3_0`

## OpenAI SDK integration

If you are a platform that prioritizes a unified schema (OpenAI Chat
Completions) over model-specific features, this is your fastest route.

**Benefits:**

- **Low friction:** You can often add Gemini support by changing the `baseURL` and `apiKey`. This is a fast way to integrate "Bring Your Own Key" implementations, adding Gemini support without writing new code.
- **Constraints:** This path is only recommended if you are restricted to the OpenAI SDK and don't require advanced Gemini features like the File API, or manually adding support for tools like Grounding with Google Search.

**The trade-off:**

- **Feature limitations:** The compatibility layer provides limitations to core Gemini capabilities. The available server-side tools differ between platforms, and may require manual handling to work with the Gemini API tools.
- **Translation overhead:** Because the OpenAI schema does not map 1:1 to Gemini's architecture, relying on the compatibility layer introduces some complexities that require extra implementation work to solve, such as mapping a user "search" tool to the right platform tool. If you need a significant amount of special-casing, it may be more value to use a dedicated SDK or API for each platform.

**Best practice**

Where possible, integrate directly with the Gemini API. However for maximum
compatibility consider using a library that is aware of different providers and
can handle tool and message mapping for you.

## Best practice for all partners: client identification

When making calls to the Gemini API as a platform or library, you must
identify your client using the `x-goog-api-client` header.

This allows Google to identify your specific traffic segments, and if
your library is producing a specific error pattern, we can reach out to help
debug.

Use the format `company-product/version` (e.g., `acme-framework/1.2.0`).

### Implementation examples

### GenAI SDK

By providing the API client, the SDK automatically appends your custom
header to its internal headers.

    from google import genai

    client = genai.Client(
        api_key="...",
        http_options={
            "headers": {
                "x-goog-api-client": "acme-framework/1.2.0",
            }
        }
    )

### Direct API (REST)

    curl "https://generativelanguage.googleapis.com/v1beta/models/gemini-3.5-flash:generateContent?key=$GEMINI_API_KEY" \
        -H 'Content-Type: application/json' \
        -H 'x-goog-api-client: acme-framework/1.2.0' \
        -d '{...}'

### OpenAI SDK

    from openai import OpenAI

    client = OpenAI(
        api_key="...",
        base_url="https://generativelanguage.googleapis.com/v1beta/openai/",
        default_headers={
            "x-goog-api-client": "acme-framework-oai/1.2.0",
        }
    )

## Next steps

- Visit the [library overview](https://ai.google.dev/gemini-api/docs/libraries) to learn about the GenAI SDKs
- Browse the [API reference](https://ai.google.dev/api)
- Read the [OpenAI compatibility guide](https://ai.google.dev/gemini-api/docs/openai)