# Gemini API quickstart

> [!IMPORTANT]
> We have updated our [Terms of Service](https://ai.google.dev/gemini-api/terms).

> [!NOTE]
> **Note** : This version of the page covers the new [Interactions API](https://ai.google.dev/gemini-api/docs/interactions), which is currently in Beta.  
> For stable production deployments, we recommend you continue to use the `generateContent` API. You can use the toggle on this page to switch between the versions.

This quickstart shows you how to install our [libraries](https://ai.google.dev/gemini-api/docs/libraries)
and make your first request, stream responses, build multi-turn conversations,
and use tools.

There are two ways you can use to send a request to the Gemini API:

- ***(Recommended)*** [Interactions API](https://ai.google.dev/api/interactions-api) is a new primitive with built-in support for multi-step tool use, orchestration, and complex reasoning flows through typed execution steps. Going forward, new models beyond the core mainline family, along with new agentic capabilities and tools, will launch exclusively on the Interactions API.
- [`generateContent`](https://ai.google.dev/gemini-api/docs/quickstart) provides a way to generate a stateless response from a model. While we recommend using Interactions API, `generateContent` is fully supported.

This version of the quickstart uses the Interactions API to send a request to
the Gemini API.

## Before you begin

To use the Gemini API, you need to have an API key to authenticate your requests, enforce security limits, and track usage to your account.

Create one on AI Studio for free to get started:

[Create a Gemini API Key](https://aistudio.google.com/app/apikey)

## Install the Google GenAI SDK

### Python

Using [Python 3.9+](https://www.python.org/downloads/), install the
[`google-genai` package](https://pypi.org/project/google-genai/)
using the following
[pip command](https://packaging.python.org/en/latest/tutorials/installing-packages/):

    pip install -q -U google-genai

### JavaScript

Using [Node.js v18+](https://nodejs.org/en/download/package-manager),
install the
[Google Gen AI SDK for TypeScript and JavaScript](https://www.npmjs.com/package/@google/genai)
using the following
[npm command](https://docs.npmjs.com/downloading-and-installing-node-js-and-npm):

    npm install @google/genai

## Generate text

Use the `interactions.create` method to
[generate a text response](https://ai.google.dev/gemini-api/docs/interactions/text-generation).

### Python

    from google import genai

    client = genai.Client()

    interaction = client.interactions.create(
        model="gemini-3.5-flash",
        input="Explain how AI works in a few words"
    )

    print(interaction.output_text)

### JavaScript

    import { GoogleGenAI } from "@google/genai";

    const ai = new GoogleGenAI({});

    async function main() {
      const interaction = await ai.interactions.create({
        model: "gemini-3.5-flash",
        input: "Explain how AI works in a few words",
      });

      console.log(interaction.output_text);
    }

    main();

### REST

    curl -X POST \
      "https://generativelanguage.googleapis.com/v1beta/interactions" \
      -H "x-goog-api-key: $GEMINI_API_KEY" \
      -H 'Content-Type: application/json' \
      -H "Api-Revision: 2026-05-20" \
      -d '{
        "model": "gemini-3.5-flash",
        "input": "Explain how AI works in a few words"
      }'

## Stream responses

By default, the model returns a response only after the entire generation
process is complete. For a faster, more interactive experience, you can
[stream the response](https://ai.google.dev/gemini-api/docs/interactions/streaming) chunks as they
are generated.

### Python

    stream = client.interactions.create(
        model="gemini-3.5-flash",
        input="Explain how AI works in detail",
        stream=True
    )

    for event in stream:
        if event.event_type == "step.delta":
            if event.delta.type == "text":
                print(event.delta.text, end="", flush=True)

### JavaScript

    async function main() {
      const stream = await ai.interactions.create({
        model: "gemini-3.5-flash",
        input: "Explain how AI works in detail",
        stream: true,
      });

      for await (const event of stream) {
        if (event.event_type === "step.delta") {
          if (event.delta.type === "text") {
            process.stdout.write(event.delta.text);
          }
        }
      }
    }

    main();

### REST

    # Use alt=sse for streaming
    curl -X POST \
      "https://generativelanguage.googleapis.com/v1beta/interactions?alt=sse" \
      -H "x-goog-api-key: $GEMINI_API_KEY" \
      -H 'Content-Type: application/json' \
      -H "Api-Revision: 2026-05-20" \
      --no-buffer \
      -d '{
        "model": "gemini-3.5-flash",
        "input": "Explain how AI works in detail",
        "stream": true
      }'

## Multi-turn conversations

The Gemini API has built-in support for building
[multi-turn conversations](https://ai.google.dev/gemini-api/docs/interactions/text-generation#multi-turn-conversations).
Simply pass the `id` returned from the previous interaction as the
`previous_interaction_id` parameter, and the server automatically manages the
conversation history.

### Python


    interaction1 = client.interactions.create(
        model="gemini-3.5-flash",
        input="I have 2 dogs in my house."
    )
    print("Response 1:", interaction1.output_text)

    interaction2 = client.interactions.create(
        model="gemini-3.5-flash",
        input="How many paws are in my house?",
        previous_interaction_id=interaction1.id
    )
    print("Response 2:", interaction2.output_text)

### JavaScript

    async function main() {
      const interaction1 = await ai.interactions.create({
        model: "gemini-3-flash-preview",
        input: "I have 2 dogs in my house.",
      });
      console.log("Response 1:", interaction1.output_text);

      const interaction2 = await ai.interactions.create({
        model: "gemini-3-flash-preview",
        input: "How many paws are in my house?",
        previous_interaction_id: interaction1.id,
      });
      console.log("Response 2:", interaction2.output_text);
    }

    main();

### REST

    # Turn 1: Start the conversation
    RESPONSE1=$(curl -s -X POST \
      "https://generativelanguage.googleapis.com/v1beta/interactions" \
      -H "Api-Revision: 2026-05-20" \
      -H "x-goog-api-key: $GEMINI_API_KEY" \
      -H 'Content-Type: application/json' \
      -d '{
        "model": "gemini-3-flash-preview",
        "input": "I have 2 dogs in my house."
      }')

    # Extract the interaction ID
    INTERACTION_ID=$(echo "$RESPONSE1" | jq -r '.id')

    # Turn 2: Continue the conversation
    curl -X POST \
      "https://generativelanguage.googleapis.com/v1beta/interactions" \
      -H "Api-Revision: 2026-05-20" \
      -H "x-goog-api-key: $GEMINI_API_KEY" \
      -H 'Content-Type: application/json' \
      -d "{
        \"model\": \"gemini-3-flash-preview\",
        \"input\": \"How many paws are in my house?\",
        \"previous_interaction_id\": \"$INTERACTION_ID\"
      }"

> [!NOTE]
> **Stateless mode** : If you prefer to manage the conversation
> history yourself on the client side, set `store=false` and pass the
> accumulated steps in the subsequent requests. Learn more in the
> [Stateless conversations guide](https://ai.google.dev/gemini-api/docs/interactions/text-generation#stateless-conversations).

## Use tools

Extend the model's capabilities by
[grounding responses with Google Search](https://ai.google.dev/gemini-api/docs/interactions/google-search)
to access real-time web content. The model automatically decides when to
search, executes queries, and synthesizes a response with citations.

The following example demonstrates how to enable Google Search:

### Python

    interaction = client.interactions.create(
        model="gemini-3-flash-preview",
        input="Who won the euro 2024?",
        tools=[{"type": "google_search"}]
    )

    print(interaction.output_text)

    for step in interaction.steps:
        if step.type == "model_output":
            for content_block in step.content:
                if content_block.type == "text" and content_block.annotations:
                    print("\nCitations:")
                    for annotation in content_block.annotations:
                        if annotation.type == "url_citation":
                            print(f"  - [{annotation.title}]({annotation.url})")

### JavaScript

    async function main() {
      const interaction = await ai.interactions.create({
        model: "gemini-3-flash-preview",
        input: "Who won the euro 2024?",
        tools: [{ type: "google_search" }]
      });

      console.log(interaction.output_text);

      for (const step of interaction.steps) {
        if (step.type === 'model_output') {
          for (const contentBlock of step.content) {
            if (contentBlock.type === 'text' && contentBlock.annotations) {
              console.log("\nCitations:");
              for (const annotation of contentBlock.annotations) {
                if (annotation.type === 'url_citation') {
                  console.log(`  - [${annotation.title}](${annotation.url})`);
                }
              }
            }
          }
        }
      }
    }

    main();

### REST

    curl -X POST \
      "https://generativelanguage.googleapis.com/v1beta/interactions" \
      -H "Api-Revision: 2026-05-20" \
      -H "x-goog-api-key: $GEMINI_API_KEY" \
      -H "Content-Type: application/json" \
      -d '{
        "model": "gemini-3-flash-preview",
        "input": "Who won the euro 2024?",
        "tools": [{"type": "google_search"}]
      }'

The Gemini API also supports other built-in tools:

- **[Code execution](https://ai.google.dev/gemini-api/docs/interactions/code-execution)**: Lets the model write and run Python code to solve complex math problems.
- **[URL context](https://ai.google.dev/gemini-api/docs/interactions/url-context)**: Lets you ground responses in specific web page URLs you provide.
- **[File search](https://ai.google.dev/gemini-api/docs/interactions/file-search)**: Lets you upload files and ground responses in their content using semantic search.
- **[Google Maps](https://ai.google.dev/gemini-api/docs/interactions/maps-grounding)**: Lets you ground responses in location data and search for places, directions, and maps.
- **[Computer use](https://ai.google.dev/gemini-api/docs/interactions/computer-use)**: Lets the model interact with a virtual computer screen, keyboard, and mouse to perform tasks.

## Call custom functions

Use **[function calling](https://ai.google.dev/gemini-api/docs/interactions/function-calling)**
to connect models to your custom tools and APIs. The model determines when to
call your function and returns a `function_call` step with the arguments for
your application to execute.

This example declares a mock temperature function and checks if the model
wants to call it.

### Python

    import json

    weather_function = {
        "type": "function",
        "name": "get_current_temperature",
        "description": "Gets the current temperature for a given location.",
        "parameters": {
            "type": "object",
            "properties": {
                "location": {
                    "type": "string",
                    "description": "The city name, e.g. San Francisco",
                },
            },
            "required": ["location"],
        },
    }

    interaction = client.interactions.create(
        model="gemini-3-flash-preview",
        input="What's the temperature in London?",
        tools=[weather_function],
    )

    fc_step = None
    for step in interaction.steps:
        if step.type == "function_call":
            fc_step = step
            break

    if fc_step:
        print(f"Model requested function: {fc_step.name} with args {fc_step.arguments}")

        mock_result = {"temperature": "15C", "condition": "Cloudy"}

        final_interaction = client.interactions.create(
            model="gemini-3-flash-preview",
            input=[
                {
                    "type": "function_result",
                    "name": fc_step.name,
                    "call_id": fc_step.id,
                    "result": [{"type": "text", "text": json.dumps(mock_result)}],
                }
            ],
            tools=[weather_function],
            previous_interaction_id=interaction.id,
        )
        print("Final Response:", final_interaction.output_text)

### JavaScript

    async function main() {
      const weatherFunction = {
        type: 'function',
        name: 'get_current_temperature',
        description: 'Gets the current temperature for a given location.',
        parameters: {
          type: 'object',
          properties: {
            location: {
              type: 'string',
              description: 'The city name, e.g. San Francisco',
            },
          },
          required: ['location'],
        },
      };

      const interaction = await ai.interactions.create({
        model: 'gemini-3-flash-preview',
        input: "What's the temperature in London?",
        tools: [weatherFunction],
      });

      const fcStep = interaction.steps.find(s => s.type === 'function_call');
      if (fcStep) {
        console.log(`Model requested function: ${fcStep.name}`);

        const mockResult = { temperature: "15C", condition: "Cloudy" };

        const finalInteraction = await ai.interactions.create({
          model: 'gemini-3-flash-preview',
          input: [{
            type: 'function_result',
            name: fcStep.name,
            call_id: fcStep.id,
            result: [{ type: 'text', text: JSON.stringify(mockResult) }]
          }],
          tools: [weatherFunction],
          previous_interaction_id: interaction.id,
        });

        console.log("Final Response:", finalInteraction.output_text);
      }
    }

    main();

### REST

    curl -X POST \
      "https://generativelanguage.googleapis.com/v1beta/interactions" \
      -H "Api-Revision: 2026-05-20" \
      -H "x-goog-api-key: $GEMINI_API_KEY" \
      -H "Content-Type: application/json" \
      -d '{
        "model": "gemini-3-flash-preview",
        "input": "What'\''s the temperature in London?",
        "tools": [{
          "type": "function",
          "name": "get_current_temperature",
          "description": "Gets the current temperature for a given location.",
          "parameters": {
            "type": "object",
            "properties": {
              "location": {"type": "string", "description": "The city name"}
            },
            "required": ["location"]
          }
        }]
      }'

## What's next

Now that you've got started with the Gemini API, explore the following
guides to build more advanced applications:

- [Text generation](https://ai.google.dev/gemini-api/docs/interactions/text-generation)
- [Image generation](https://ai.google.dev/gemini-api/docs/interactions/image-generation)
- [Image understanding](https://ai.google.dev/gemini-api/docs/interactions/image-understanding)
- [Thinking](https://ai.google.dev/gemini-api/docs/interactions/thinking)
- [Function calling](https://ai.google.dev/gemini-api/docs/interactions/function-calling)
- [Grounding with Google Search](https://ai.google.dev/gemini-api/docs/interactions/google-search)
- [Long context](https://ai.google.dev/gemini-api/docs/long-context)
- [Embeddings](https://ai.google.dev/gemini-api/docs/embeddings)