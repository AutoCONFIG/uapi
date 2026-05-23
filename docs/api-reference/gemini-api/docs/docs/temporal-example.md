# Durable AI agent with Gemini and Temporal

This tutorial walks you through building a
[ReAct-style](https://arxiv.org/abs/2210.03629) agentic loop that uses the
Gemini API for reasoning and [Temporal](https://temporal.io/) for durability.
The complete source code for this tutorial is available on
[GitHub](https://github.com/temporal-community/durable-react-agent-gemini).

The agent can call tools, like looking up weather alerts or geolocating an IP
address, and will loop until it has enough information to respond.

What makes this different from a typical agent demo is **durability**. Every LLM
call, every tool invocation, and every step of the agentic loop is persisted by
Temporal. If the process crashes, the network drops, or an API times out,
Temporal automatically retries and resumes from the last completed step. No
conversation history is lost, and no tool calls are incorrectly repeated.

## Architecture

The architecture consists of three parts:

- **Workflow:** The agentic loop that orchestrates the execution logic.
- **Activities:** Individual units of work (LLM calls, tool calls) that Temporal makes durable.
- **Worker:** The process that executes the workflows and activities.

In this example, you will place all three of these pieces in a single file
(`durable_agent_worker.py`). In a real-world implementation, you would separate
them to allow for various deployment and scalability advantages. You will place
the code that supplies a prompt to the agent in a second file
(`start_workflow.py`).

## Prerequisites

To complete this guide, you'll need:

- A Gemini API key. You can create one for free in [Google AI Studio](https://aistudio.google.com/apikey).
- [Python](https://www.python.org/downloads/) version 3.10 or later.
- The [Temporal CLI](https://docs.temporal.io/cli) for running a local development server.

## Setup

Before you begin, ensure you have a
[Temporal development server](https://docs.temporal.io/cli#start-dev-server)
running locally:

    temporal server start-dev

Next, install the required dependencies:

    pip install temporalio google-genai httpx pydantic python-dotenv

Create a `.env` file in your project directory with your Gemini API key. You
can get an API key from
[Google AI Studio](https://aistudio.google.com/apikey).

    echo "GOOGLE_API_KEY=your-api-key-here" > .env

> [!NOTE]
> **Note:** Only the worker process needs the API key. The client script does not require it.

## Implementation

The rest of this tutorial walks through `durable_agent_worker.py` from top to
bottom, building up the agent piece by piece. Create the file and follow along.

### Imports and sandbox setup

Start with the imports that must be defined up-front. The
`workflow.unsafe.imports_passed_through()` block tells Temporal's workflow
sandbox to let certain modules pass through without restriction. This is
necessary because several libraries (notably `httpx`, which subclasses
`urllib.request.Request`) use patterns the sandbox would otherwise block.

    from temporalio import workflow

    with workflow.unsafe.imports_passed_through():
        import pydantic_core  # noqa: F401
        import annotated_types  # noqa: F401

        import httpx
        from pydantic import BaseModel, Field
        from google import genai
        from google.genai import types

### System instructions

Next, define the agent's personality. The system instructions tell the model how
to behave. This agent is instructed to respond in haikus when no tools are
needed.

    SYSTEM_INSTRUCTIONS = """
    You are a helpful agent that can use tools to help the user.
    You will be given an input from the user and a list of tools to use.
    You may or may not need to use the tools to satisfy the user ask.
    If no tools are needed, respond in haikus.
    """

### Tool definitions

Now define the tools the agent can use. Each tool is an async function with a
descriptive docstring. Tools that take parameters use a Pydantic model as their
single argument. This is a Temporal best practice that keeps activity signatures
stable as you add optional fields over time.

    import json

    NWS_API_BASE = "https://api.weather.gov"
    USER_AGENT = "weather-app/1.0"


    class GetWeatherAlertsRequest(BaseModel):
        """Request model for getting weather alerts."""

        state: str = Field(description="Two-letter US state code (e.g. CA, NY)")


    async def get_weather_alerts(request: GetWeatherAlertsRequest) -> str:
        """Get weather alerts for a US state.

        Args:
            request: The request object containing:
                - state: Two-letter US state code (e.g. CA, NY)
        """
        headers = {"User-Agent": USER_AGENT, "Accept": "application/geo+json"}
        url = f"{NWS_API_BASE}/alerts/active/area/{request.state}"

        async with httpx.AsyncClient() as client:
            response = await client.get(url, headers=headers, timeout=5.0)
            response.raise_for_status()
            return json.dumps(response.json())

Next, define tools for IP address geolocation:

    class GetLocationRequest(BaseModel):
        """Request model for getting location info from an IP address."""

        ipaddress: str = Field(description="An IP address")


    async def get_ip_address() -> str:
        """Get the public IP address of the current machine."""
        async with httpx.AsyncClient() as client:
            response = await client.get("https://icanhazip.com")
            response.raise_for_status()
            return response.text.strip()


    async def get_location_info(request: GetLocationRequest) -> str:
        """Get the location information for an IP address including city, state, and country.

        Args:
            request: The request object containing:
                - ipaddress: An IP address to look up
        """
        async with httpx.AsyncClient() as client:
            response = await client.get(f"http://ip-api.com/json/{request.ipaddress}")
            response.raise_for_status()
            result = response.json()
            return f"{result['city']}, {result['regionName']}, {result['country']}"

> [!IMPORTANT]
> **Important:** The Google GenAI SDK's `FunctionDeclaration.from_callable_with_api_option()` (used in the next section) extracts the function description from the docstring but does **not** extract parameter descriptions from Pydantic `Field(description=...)`. Always put parameter descriptions in the docstring's `Args` section.

### Tool registry

Next, create a registry that maps tool names to handler functions. The
`get_tools()` function generates Gemini-compatible `FunctionDeclaration` objects
from the callables using `FunctionDeclaration.from_callable_with_api_option()`.

    from typing import Any, Awaitable, Callable

    ToolHandler = Callable[..., Awaitable[Any]]


    def get_handler(tool_name: str) -> ToolHandler:
        """Get the handler function for a given tool name."""
        if tool_name == "get_location_info":
            return get_location_info
        if tool_name == "get_ip_address":
            return get_ip_address
        if tool_name == "get_weather_alerts":
            return get_weather_alerts
        raise ValueError(f"Unknown tool name: {tool_name}")


    def get_tools() -> types.Tool:
        """Get the Tool object containing all available function declarations.

        Uses FunctionDeclaration.from_callable_with_api_option() from the Google GenAI SDK
        to generate tool definitions from the handler functions.
        """
        return types.Tool(
            function_declarations=[
                types.FunctionDeclaration.from_callable_with_api_option(
                    callable=get_weather_alerts, api_option="GEMINI_API"
                ),
                types.FunctionDeclaration.from_callable_with_api_option(
                    callable=get_location_info, api_option="GEMINI_API"
                ),
                types.FunctionDeclaration.from_callable_with_api_option(
                    callable=get_ip_address, api_option="GEMINI_API"
                ),
            ]
        )

### LLM activity

Now define the activity that calls the Gemini API. The `GeminiChatRequest` and
`GeminiChatResponse` dataclasses define the contract.

You will disable automatic function calling so that the LLM invocation and the
tool invocation are handled as separate tasks, bringing more durability to your
agent. You will also disable the SDK's built-in retries (`attempts=1`) since
Temporal handles retries durably.

    import os
    from dataclasses import dataclass

    from temporalio import activity

    @dataclass
    class GeminiChatRequest:
        """Request parameters for a Gemini chat completion."""

        model: str
        system_instruction: str
        contents: list[types.Content]
        tools: list[types.Tool]


    @dataclass
    class GeminiChatResponse:
        """Response from a Gemini chat completion."""

        text: str | None
        function_calls: list[dict[str, Any]]
        raw_parts: list[types.Part]


    @activity.defn
    async def generate_content(request: GeminiChatRequest) -> GeminiChatResponse:
        """Execute a Gemini chat completion with tool support."""
        api_key = os.environ.get("GOOGLE_API_KEY")
        if not api_key:
            raise ValueError("GOOGLE_API_KEY environment variable is not set")
        client = genai.Client(
            api_key=api_key,
            http_options=types.HttpOptions(
                retry_options=types.HttpRetryOptions(attempts=1),
            ),
        )

        config = types.GenerateContentConfig(
            system_instruction=request.system_instruction,
            tools=request.tools,
            automatic_function_calling=types.AutomaticFunctionCallingConfig(disable=True),
        )

        response = await client.aio.models.generate_content(
            model=request.model,
            contents=request.contents,
            config=config,
        )

        function_calls = []
        raw_parts = []
        text_parts = []

        if response.candidates and response.candidates[0].content:
            for part in response.candidates[0].content.parts:
                raw_parts.append(part)
                if part.function_call:
                    function_calls.append(
                        {
                            "name": part.function_call.name,
                            "args": dict(part.function_call.args) if part.function_call.args else {},
                        }
                    )
                elif part.text:
                    text_parts.append(part.text)

        text = "".join(text_parts) if text_parts and not function_calls else None

        return GeminiChatResponse(
            text=text,
            function_calls=function_calls,
            raw_parts=raw_parts,
        )

### Dynamic tool activity

Next, define the activity that executes tools. This uses Temporal's dynamic
activity feature: the tool handler (a callable) is obtained from the tool
registry via the `get_handler` function. This allows for different agents to be
defined simply by supplying a different set of tools and system instructions; the
workflow implementing the agentic loop requires no changes.

The activity inspects the handler's signature to determine how to pass
arguments. If the handler expects a Pydantic model, it handles the nested output
format that Gemini produces (for example, `{"request": {"state": "CA"}}` instead
of a flat `{"state": "CA"}`).

    import inspect
    from collections.abc import Sequence

    from temporalio.common import RawValue

    @activity.defn(dynamic=True)
    async def dynamic_tool_activity(args: Sequence[RawValue]) -> dict:
        """Execute a tool dynamically based on the activity name."""
        tool_name = activity.info().activity_type
        tool_args = activity.payload_converter().from_payload(args[0].payload, dict)
        activity.logger.info(f"Running dynamic tool '{tool_name}' with args: {tool_args}")

        handler = get_handler(tool_name)

        if not inspect.iscoroutinefunction(handler):
            raise TypeError("Tool handler must be async (awaitable).")

        sig = inspect.signature(handler)
        params = list(sig.parameters.values())

        if len(params) == 0:
            result = await handler()
        else:
            param = params[0]
            param_name = param.name
            ann = param.annotation

            if isinstance(ann, type) and issubclass(ann, BaseModel):
                nested_args = tool_args.get(param_name, tool_args)
                result = await handler(ann(**nested_args))
            else:
                result = await handler(**tool_args)

        activity.logger.info(f"Tool '{tool_name}' result: {result}")
        return result

### The agentic loop workflow

Now you have all the pieces to finish building the agent. The `AgentWorkflow`
class implements a workflow containing the agent loop. Within that loop, the LLM
is invoked via activity (making it durable), the output is inspected, and if a
tool has been chosen by the LLM, it is invoked via the `dynamic_tool_activity`.

In this simple ReAct style agent, once the LLM chooses not to use a tool, the
loop is considered complete and the final LLM result is returned.

    from datetime import timedelta

    @workflow.defn
    class AgentWorkflow:
        """Agentic loop workflow that uses Gemini for LLM calls and executes tools."""

        @workflow.run
        async def run(self, input: str) -> str:
            contents: list[types.Content] = [
                types.Content(role="user", parts=[types.Part.from_text(text=input)])
            ]

            tools = [get_tools()]

            while True:
                result = await workflow.execute_activity(
                    generate_content,
                    GeminiChatRequest(
                        model="gemini-3.5-flash",
                        system_instruction=SYSTEM_INSTRUCTIONS,
                        contents=contents,
                        tools=tools,
                    ),
                    start_to_close_timeout=timedelta(seconds=60),
                )

                if result.function_calls:
                    # Sending the complete raw_parts here ensures Gemini 3 thought
                    # signatures are propagated correctly.
                    contents.append(types.Content(role="model", parts=result.raw_parts))

                    for function_call in result.function_calls:
                        tool_result = await self._handle_function_call(function_call)

                        contents.append(
                            types.Content(
                                role="user",
                                parts=[
                                    types.Part.from_function_response(
                                        name=function_call["name"],
                                        response={"result": tool_result},
                                    )
                                ],
                            )
                        )
                else:
                    return result.text
                # Leave this in place. You will un-comment it during a durability
                # test later on.
                # await asyncio.sleep(10)

        async def _handle_function_call(self, function_call: dict) -> str:
            """Execute a tool via dynamic activity and return the result."""
            tool_name = function_call["name"]
            tool_args = function_call.get("args", {})

            result = await workflow.execute_activity(
                tool_name,
                tool_args,
                start_to_close_timeout=timedelta(seconds=30),
            )

            return result

The agentic loop is fully durable. If the agent worker crashes after several
iterations through the loop, Temporal will pick up exactly where it left off
without a need to re-invoke already executed LLM invocations or tool calls.

### Worker startup

Finally, wire everything together. While the code implements the necessary
business logic in a manner that makes it appear to be running in a single
process, the use of Temporal makes it an event-driven system (specifically,
event-sourced) where the communication between the workflow and activities
happens via messaging provided by Temporal.

The Temporal worker connects to the Temporal service and acts as a scheduler for
the workflow and activity tasks. The worker registers the workflow and both
activities, then starts listening for tasks.

    import asyncio
    from concurrent.futures import ThreadPoolExecutor

    from dotenv import load_dotenv
    from temporalio.client import Client
    from temporalio.contrib.pydantic import pydantic_data_converter
    from temporalio.envconfig import ClientConfig
    from temporalio.worker import Worker

    async def main():
        config = ClientConfig.load_client_connect_config()
        config.setdefault("target_host", "localhost:7233")
        client = await Client.connect(
            **config,
            data_converter=pydantic_data_converter,
        )

        worker = Worker(
            client,
            task_queue="gemini-agent-python-task-queue",
            workflows=[
                AgentWorkflow,
            ],
            activities=[
                generate_content,
                dynamic_tool_activity,
            ],
            activity_executor=ThreadPoolExecutor(max_workers=10),
        )
        await worker.run()


    if __name__ == "__main__":
        load_dotenv()
        asyncio.run(main())

## The client script

Create the client script (`start_workflow.py`). It submits a query and waits for
the result. Notice it connects to the same task queue referenced in the agent
worker---the `start_workflow` script dispatches a workflow task with the user
prompt to that task queue, initiating the execution of the agent.

    import asyncio
    import sys
    import uuid

    from temporalio.client import Client
    from temporalio.contrib.pydantic import pydantic_data_converter


    async def main():
        client = await Client.connect(
            "localhost:7233",
            data_converter=pydantic_data_converter,
        )

        query = sys.argv[1] if len(sys.argv) > 1 else "Tell me about recursion"

        result = await client.execute_workflow(
            "AgentWorkflow",
            query,
            id=f"gemini-agent-id-{uuid.uuid4()}",
            task_queue="gemini-agent-python-task-queue",
        )
        print(f"\nResult:\n{result}")


    if __name__ == "__main__":
        asyncio.run(main())

## Run the agent

If you haven't already, start the Temporal development server:

    temporal server start-dev

In a new terminal window, start the agent worker:

    python -m durable_agent_worker

In a third terminal window, submit a query to your agent:

    python -m start_workflow "are there any weather alerts for where I am?"

Notice the output in the terminal of the `durable_agent_worker` that shows the
actions that happen in each iteration of the agentic loop. The LLM is able to
satisfy the user request by invoking a series of tools at its disposal. You can
see the steps that were executed via the Temporal UI at
`http://localhost:8233/namespaces/default/workflows`.

Try a few different prompts to see the agent reason and call tools:

    python -m start_workflow "are there any weather alerts for New York?"
    python -m start_workflow "where am I?"
    python -m start_workflow "what is my ip address?"
    python -m start_workflow "tell me a joke"

The last prompt doesn't require any tools, so the agent responds in a haiku
based on the `SYSTEM_INSTRUCTIONS`.

## Test durability (Optional)

Building on Temporal ensures your agent survives failures seamlessly. You can
test this using two distinct experiments.

### Simulating a network outage

In this test, you'll temporarily disable your computer's internet connection,
submit a workflow, watch Temporal automatically retry, and then restore the
network to see it recover.

1. Disconnect your machine from the internet (for example, turn off your Wi-Fi).
2. Submit a workflow:

       python -m start_workflow "tell me a joke"

3. Check the Temporal UI (`http://localhost:8233`). You will see the LLM
   activity failing and Temporal automatically managing the retries in the
   background.

4. Reconnect to the internet.

5. The next automated retry will successfully reach the Gemini API, and your
   terminal will print the final result.

### Surviving a worker crash

In this test, you kill the worker mid-execution and restart it. Temporal replays
the workflow history (event sourcing) and resumes from the last completed
activity---already-completed LLM invocations and tool calls are not repeated.

1. To give yourself time to kill the worker, open `durable_agent_worker.py` and temporarily uncomment `await asyncio.sleep(10)` inside the `AgentWorkflow` `run` loop.
2. Restart the worker:

       python -m durable_agent_worker

3. Submit a query that triggers several tools:

       python -m start_workflow "are there any weather alerts where I am?"

4. Kill the worker process any time before completion (`Ctrl-C` in the worker
   terminal, or using `kill %1` if running in the background).

5. Restart the worker:

       python -m durable_agent_worker

Temporal replays the workflow history. The LLM calls and tool invocations that
already completed are **not** re-executed---their results are instantly replayed
from history (the event log). The workflow finishes successfully.

## Further resources

- [Temporal documentation](https://docs.temporal.io/)
- [Temporal Python SDK](https://docs.temporal.io/develop/python)
- [Google GenAI SDK](https://googleapis.github.io/python-genai/)
- [Source code for this tutorial](https://github.com/temporal-community/durable-react-agent-gemini)