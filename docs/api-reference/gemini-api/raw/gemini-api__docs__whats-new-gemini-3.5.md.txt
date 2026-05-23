# What's new in Gemini 3.5 Flash

> [!NOTE]
> **Note** : You are viewing `generateContent`. For new projects, we recommend using the new [Interactions API](https://ai.google.dev/gemini-api/docs/interactions) (Beta)---built for agentic workflows, background tasks, and future Gemini capabilities.

Gemini 3.5 Flash is **generally available (GA)**, stable, and ready for scaled
production use. As our most intelligent Flash model, it delivers sustained
frontier performance in agentic execution, coding, and long-horizon tasks at
scale.

This guide contains an overview of improvements, API changes, and migration
guidance for Gemini 3.5 Flash.

## New model

| Model | Model ID | Description |
|---|---|---|
| Gemini 3.5 Flash | `gemini-3.5-flash` | Our most intelligent model for sustained frontier performance in agentic and coding tasks. |

Gemini 3.5 Flash supports the 1M token context window, 65k max output tokens,
thinking, and the same set of tools and platform features as Gemini 3 Flash.
[Computer Use](https://ai.google.dev/gemini-api/docs/computer-use) is not supported at this moment.

For complete specs, see the [models overview](https://ai.google.dev/gemini-api/docs/models/gemini-3.5-flash).
For pricing, see the [pricing page](https://ai.google.dev/gemini-api/docs/pricing#gemini-3.5-flash).

## Quickstart

All examples in this guide use the GenerateContent API. The Interactions API is also supported; the same configuration options and recommendations apply.

### Python

    from google import genai

    client = genai.Client()

    response = client.models.generate_content(
        model="gemini-3.5-flash",
        contents="Explain how parallel agentic execution works in three sentences.",
    )
    print(response.text)

### JavaScript

    import { GoogleGenAI } from "@google/genai";

    const ai = new GoogleGenAI({});

    async function main() {
      const response = await ai.models.generateContent({
        model: "gemini-3.5-flash",
        contents: "Explain how parallel agentic execution works in three sentences.",
      });
      console.log(response.text);
    }

    main();

### REST

    curl "https://generativelanguage.googleapis.com/v1beta/models/gemini-3.5-flash:generateContent" \
      -H "x-goog-api-key: $GEMINI_API_KEY" \
      -H 'Content-Type: application/json' \
      -X POST \
      -d '{
        "contents": [{
          "parts": [{"text": "Explain how parallel agentic execution works in three sentences."}]
        }]
      }'

## What's new

- **Sustained frontier performance:** Our most intelligent Flash model, optimized for agentic and coding tasks at scale.
- **Agentic execution:** Sub-agent deployment, problem solving, and rapid agentic loops at scale.
- **Coding:** Iterative coding cycles, rapid exploration, and prototyping to test alternate paths and dynamically explore solutions.
- **Long horizon:** Multi-step workflows and tool use at scale.
- **Thought preservation:** The model maintains intermediate reasoning across multi-turn conversations automatically. No API changes needed.
- **New default effort level:** Default thinking effort changed from `high` to `medium`. See [New default effort level](https://ai.google.dev/gemini-api/docs/whats-new-gemini-3.5#default-effort-level) for details.
- **Improved `low` thinking:** `low` is now significantly improved for code and agentic tasks that require fewer steps, offering strong quality at lower latency and cost.
- **GA release:** Stable model for scaled production use.

## Behavioral changes

### New default effort level: `medium`

The default thinking effort is now **`medium`** , changed from `high` in Gemini 3
Flash Preview. `medium` yields very good results across a wide range of tasks
while being faster and more cost-efficient. For complex problems, `high`
encourages the model to think more deeply.

| Effort level | When to use |
|---|---|
| `minimal` | Optimized for response speed. Chat-like use cases, quick factual answers, simpler tool calls. |
| `low` | Code and agentic tasks that require lower latency and fewer steps. Also works well for analysis and writing tasks that require some thinking. |
| **`medium` (default)** | **Best quality for most tasks. Recommended for complex code and agentic use cases.** |
| `high` | Maximizes the model's ability to think and use tools. Best for complex reasoning, hard math, and the most difficult code or agent tasks. Allows extended thoughts and function calls. |

To override the default, set `thinking_level` in your config:

### Python

    from google import genai
    from google.genai import types

    client = genai.Client()

    response = client.models.generate_content(
        model="gemini-3.5-flash",
        contents="Prove that the square root of 2 is irrational.",
        config=types.GenerateContentConfig(
            thinking_config=types.ThinkingConfig(thinking_level="high")
        ),
    )

    print(response.text)

### JavaScript

    import { GoogleGenAI } from "@google/genai";

    const ai = new GoogleGenAI({});

    async function main() {
      const response = await ai.models.generateContent({
        model: "gemini-3.5-flash",
        contents: "Prove that the square root of 2 is irrational.",
        config: {
          thinkingConfig: {
            thinkingLevel: "HIGH",
          },
        },
      });
      console.log(response.text);
    }

    main();

### REST

    curl "https://generativelanguage.googleapis.com/v1beta/models/gemini-3.5-flash:generateContent" \
      -H "x-goog-api-key: $GEMINI_API_KEY" \
      -H 'Content-Type: application/json' \
      -X POST \
      -d '{
        "contents": [{
          "parts": [{"text": "Prove that the square root of 2 is irrational."}]
        }],
        "generationConfig": {
          "thinkingConfig": {
            "thinkingLevel": "HIGH"
          }
        }
      }'

> [!TIP]
> **Tip:** Start with `medium`, it provides the best quality for the vast majority of tasks. Try `low` for a faster, cheaper experience with strong quality. Switch to `high` for complex reasoning, hard math, or difficult coding challenges. Use `minimal` to optimize for speed in simple queries.

### Thought preservation

The model maintains intermediate reasoning across multi-turn conversations
automatically. When present in the conversation history, reasoning context
carries forward, which improves performance on complex multi-step tasks
like iterative debugging and code refactoring. No API changes needed:

- **Interactions API**: Thoughts are already preserved automatically. No change in behavior.
- **GenerateContent API** : Beginning with Gemini 3.5 Flash, the model uses reasoning context from all previous turns when thought signatures are present in the conversation history. To enable this, pass the full, unmodified conversation history (including [thought signatures](https://ai.google.dev/gemini-api/docs/thought-signatures)) in `contents`. The SDKs handle this automatically.

> [!NOTE]
> **Note:** When using the GenerateContent API, preserved thoughts increase input token count over multiple turns. For simple queries, you can clear them to reduce cost. See the [thought signatures guide](https://ai.google.dev/gemini-api/docs/thought-signatures#thought-preservation) for details.

## Parameter updates and best practices in Gemini 3.x

The following apply to **all Gemini 3.x models**, including Gemini 3.5 Flash.

- **`temperature`, `top_p`, `top_k`**: we strongly recommend not changing the default values. Gemini 3's reasoning capabilities are optimized for the default settings.
- Use `thinking_level` instead of `thinking_budget`.
- **Function calling response matching** : `id`, `name`, and response count must match the preceding calls.
- **Multimodal function responses**: include multimodal content inside the function response, not outside it.
- **Inline instructions in function responses**: append to the function response text, not as separate parts.
- **Reduce unnecessary tool calls**: Use lower thinking levels or experiment with system instructions to reduce tool calls in agentic workflows.

See the sections below for how to update your code.

### Sampling parameters (no longer recommended)

`temperature`, `top_p`, and `top_k` are no longer recommended for all Gemini
3.x models. Gemini 3's reasoning capabilities are optimized for the default
settings. **Remove these parameters from all requests.**

    # ⚠️ Remove these parameters (not recommended)
    config = types.GenerateContentConfig(
        temperature=0.7,
        top_p=0.9,
        top_k=40
    )

To ensure determinism, we recommend defining a system instruction with explicit rules for your specific use case.

### `thinking_budget` (no longer recommended)

The raw numeric `thinking_budget` parameter is no longer recommended across all
Gemini 3.x models. Use the `thinking_level` string enum instead.

    # ⚠️ Before (not recommended)
    config = types.GenerateContentConfig(
        thinking_config=types.ThinkingConfig(thinking_budget=7500)
    )

    # ✅ After
    config = types.GenerateContentConfig(
        thinking_config=types.ThinkingConfig(thinking_level="medium")
    )

Available values: `minimal`, `low`, `medium` (default), and `high`.

### Function calling: strict response matching

The Interactions API already errors on mismatched function responses. The
GenerateContent API does not yet error, but mismatched responses cause the model
to return empty responses with `finish_reason: STOP` in most cases. Always
follow these conventions:

| Requirement | Details |
|---|---|
| Include `id` | Every `FunctionResponse` must include the `id` from the corresponding `FunctionCall` |
| Match `name` | The `name` in the response must match the `name` in the call |
| Match counts | Return exactly one `FunctionResponse` for each `FunctionCall` received |

### Python

    # ✅ Include matching id and name in the function response
    final_response = client.models.generate_content(
        model="gemini-3.5-flash",
        config=config,
        contents=[
            *previous_contents,
            response.candidates[0].content,
            types.Content(role="user", parts=[
                types.Part.from_function_response(
                    name=tool_call.name,
                    response={"result": result},
                    id=tool_call.id,
                )
            ]),
        ],
    )

### JavaScript

    // ✅ Include matching id and name in the function response
    const functionResponsePart = {
      functionResponse: {
        name: toolCall.name,
        response: { result: result },
        id: toolCall.id,
      },
    };

    const finalResponse = await ai.models.generateContent({
      model: "gemini-3.5-flash",
      contents: [
        ...previousContents,
        { role: "model", parts: [{ functionCall: toolCall }] },
        { role: "user", parts: [functionResponsePart] },
      ],
      config: config,
    });

### REST

    curl "https://generativelanguage.googleapis.com/v1beta/models/gemini-3.5-flash:generateContent" \
      -H "x-goog-api-key: $GEMINI_API_KEY" \
      -H 'Content-Type: application/json' \
      -X POST \
      -d '{
        "contents": [
          {"role": "user", "parts": [{"text": "..."}]},
          {"role": "model", "parts": [{"functionCall": {"name": "my_function", "args": {...}}}]},
          {"role": "user", "parts": [{"functionResponse": {"name": "my_function", "id": "call_id", "response": {"result": "..."}}}]}
        ]
      }'

### Multimodal function responses

We often see clients provide images outside function response. This can lead to
unexpected model behavior (e.g. thought leakage) and result in lower quality
outputs. Follow the recommendation in
[Multimodal Function Responses API docs](https://ai.google.dev/gemini-api/docs/function-calling?example=meeting#multimodal)
instead and include multimodal content in the function response parts that you
send to the model. The model can process this multimodal content in its next
turn to produce a more informed response.

### Python

    # ✅ Include multimodal content in the function response
    final_response = client.models.generate_content(
        model="gemini-3.5-flash",
        config=config,
        contents=[
            *previous_contents,
            response.candidates[0].content,
            types.Content(role="user", parts=[
                types.Part.from_function_response(
                    name=tool_call.name,
                    response={
                        "result": "instrument.jpg",
                        "image": base64_image_data,
                    },
                    id=tool_call.id,
                )
            ]),
        ],
    )

### JavaScript

    // ✅ Include multimodal content in the function response
    const finalResponse = await ai.models.generateContent({
      model: "gemini-3.5-flash",
      contents: [
        ...previousContents,
        { role: "model", parts: [{ functionCall: toolCall }] },
        {
          role: "user",
          parts: [{
            functionResponse: {
              name: toolCall.name,
              id: toolCall.id,
              response: {
                result: "instrument.jpg",
                image: base64ImageData,
              },
            },
          }],
        },
      ],
      config: config,
    });

### Inline instructions in function responses

We often see clients provide additional instructions along with function responses
as subsequent `Parts`. This can lead to unexpected model behavior (e.g.
thought leakage) and result in lower quality outputs. Instead, append any extra
instructions to the end of the function response text separated by two newlines.

### Python

    # ✅ Append inline instructions to the end of the function response separated by two newlines
    result_text = f"{json.dumps(result)}\n\n<your inline instructions>"

    final_response = client.models.generate_content(
        model="gemini-3.5-flash",
        config=config,
        contents=[
            *previous_contents,
            response.candidates[0].content,
            types.Content(role="user", parts=[
                types.Part.from_function_response(
                    name=tool_call.name,
                    response={"result": result_text},
                    id=tool_call.id,
                )
            ]),
        ],
    )

### JavaScript

    // ✅ Append inline instructions to the end of the function response separated by two newlines
    const resultText = `${JSON.stringify(result)}\n\n<your inline instructions>`;

    const finalResponse = await ai.models.generateContent({
      model: "gemini-3.5-flash",
      contents: [
        ...previousContents,
        { role: "model", parts: [{ functionCall: toolCall }] },
        {
          role: "user",
          parts: [{
            functionResponse: {
              name: toolCall.name,
              id: toolCall.id,
              response: { result: resultText },
            },
          }],
        },
      ],
      config: config,
    });

### Reducing unnecessary tool calls

If you experience an overuse of tool calls, two techniques help minimize them:

1. **Start by reducing the thinking level** (`medium`, `low`, or `minimal`):
   Higher thinking levels encourage the model to use more tools to explore and
   verify, so lowering the level can reduce tool calls.

2. **Add a system instruction:** If overuse persists after adjusting the
   thinking level, consider a prompt that restricts tool usage. For example:

       You have a limited action budget of <n> tool calls. Use them efficiently.

## Migration checklist

### Migrate from Gemini 3 Flash Preview

- Update model name: `gemini-3-flash-preview` → `gemini-3.5-flash`
- Review pricing. Gemini 3.5 Flash is more expensive than Gemini 3 Flash Preview. See the [pricing page](https://ai.google.dev/gemini-api/docs/pricing#gemini-3.5-flash) for details.
- Remove `temperature`, `top_p`, `top_k` from your config (no longer recommended).
- Replace `thinking_budget` with `thinking_level`.
- Add `id` and matching `name` to all `FunctionResponse` parts.
- Test your prompts. Default effort changed from `high` → `medium`; verify quality, speed, and cost.
- Thought preservation is now on by default. Reasoning context carries forward across turns, which improves performance but may increase token usage.
- Reduce unnecessary tool calls: start by reducing the thinking level (`medium`, `low`, or `minimal`); add a system instruction to constrain tool usage if overuse persists.
- [Computer Use](https://ai.google.dev/gemini-api/docs/computer-use) is not supported in Gemini 3.5 Flash at this moment. For Computer Use workloads, continue using Gemini 3 Flash Preview.

### Migrate from Gemini 2.5

All of the above, plus:

- Simplify prompts. If you used chain-of-thought prompt engineering to force reasoning, try `thinking_level: "medium"` or `"high"` with simpler prompts instead.
- Test PDF and media workloads. If you relied on specific behavior for dense document parsing, test the `media_resolution_high` setting to ensure continued accuracy. Migrating to Gemini 3 defaults may also increase token usage for PDFs but decrease it for video; if requests exceed the context window, explicitly reduce the `media_resolution`. See the [media resolution](https://ai.google.dev/gemini-api/docs/media-resolution) docs for details.
- Leverage [combined tool use](https://ai.google.dev/gemini-api/docs/tool-combination). Google Search, URL context, code execution, and custom functions can be used in the same request.
- If using multimodal function responses, move multimodal content inside function response parts, not alongside them.
- If using inline instructions with function responses, append them to the function response text separated by two newlines, not as separate parts.
- Image segmentation is not supported in Gemini 3.x. For segmentation workloads, continue using Gemini 2.5 Flash with thinking off, or [Gemini Robotics-ER 1.6](https://ai.google.dev/gemini-api/docs/robotics-overview).

## Gemini 3 family features

Gemini 3.5 Flash inherits all Gemini 3 family capabilities except Computer Use.
Features introduced in Gemini 3 that carry forward:

- [**Thinking**](https://ai.google.dev/gemini-api/docs/thinking)**:** Encrypted reasoning context preserved across API calls. Automatic in the Interactions API; implicit in GenerateContent.
- [**Structured outputs with tools**](https://ai.google.dev/gemini-api/docs/structured-output)**:** Combine JSON mode with built-in tools (Search, URL context, code execution, function calling).
- [**Multimodal function responses**](https://ai.google.dev/gemini-api/docs/function-calling#multimodal)**:** Return images, audio, and other media in function call results.
- [**Code execution with images**](https://ai.google.dev/gemini-api/docs/code-execution#images)**:** Execute code that processes and generates images.
- [**Combined tool use**](https://ai.google.dev/gemini-api/docs/tool-combination)**:** Use built-in tools and custom function calling in the same request.

## Next steps

- Read more about the Gemini 3 family in the [Gemini 3 developer guide](https://ai.google.dev/gemini-api/docs/gemini-3)
- Learn more about prompt design strategies in the [prompt engineering guide](https://ai.google.dev/gemini-api/docs/prompting-strategies).
- Get started with the [Gemini 3 Cookbook](https://github.com/google-gemini/cookbook)
- Learn about [Gemini API optimization and inference](https://ai.google.dev/gemini-api/docs/optimization)