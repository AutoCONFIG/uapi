# Gemini 3.1 Pro Preview

<br />

Built to refine the performance and reliability of the Gemini 3 Pro series,
Gemini 3.1 Pro Preview provides better thinking, improved token
efficiency, and a more grounded, factually consistent experience. It's optimized
for software engineering behavior and usability, as well as agentic workflows
requiring precise tool usage and reliable multi-step execution across real-world
domains.
[Try in Google AI Studio](https://aistudio.google.com/prompts/new_chat?model=gemini-3.1-pro-preview)

## Documentation

Visit the [Gemini 3 Developer Guide](https://ai.google.dev/gemini-api/docs/gemini-3) page for full
coverage of features and capabilities.

## gemini-3.1-pro-preview

| Property | Description |
|---|---|
| Model code | `gemini-3.1-pro-preview` |
| Supported data types | **Inputs** Text, Image, Video, Audio, and PDF **Output** Text |
| Token limits^[\[\*\]](https://ai.google.dev/gemini-api/docs/tokens)^ | **Input token limit** 1,048,576 **Output token limit** 65,536 |
| Capabilities | **Audio generation** Not supported **Batch API** Supported **Caching** Supported **Code execution** Supported **File search** Supported (AI Studio only) **Flex inference** Supported **Function calling** Supported **Grounding with Google Maps** Supported **Image generation** Not supported **Live API** Not supported **Priority inference** Supported **Search grounding** Supported **Structured outputs** Supported **Thinking** Supported **URL context** Supported |
| Versions | Read the [model version patterns](https://ai.google.dev/gemini-api/docs/models/gemini#model-versions) for more details. - Preview: `gemini-3.1-pro-preview` - Preview: `gemini-3.1-pro-preview-customtools` \* |
| Latest update | February 2026 |
| Knowledge cutoff | January 2025 |

#### gemini-3.1-pro-preview-customtools

\* *For those building with a mix of bash and custom tools, Gemini 3.1 Pro Preview
comes with a separate endpoint available via the API called
`gemini-3.1-pro-preview-customtools`. This endpoint is better at prioritizing
your custom tools (for example `view_file` or `search_code`).*

*Note that while `gemini-3.1-pro-preview-customtools` is optimized for agentic
workflows that use custom tools and bash, you may see quality fluctuations in
some use cases which don't benefit from such tools.*