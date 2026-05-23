# ReAct agent from scratch with Gemini and LangGraph

LangGraph is a framework for building stateful LLM applications, making it a
good choice for constructing ReAct (Reasoning and Acting) Agents.

ReAct agents combine LLM reasoning with action execution. They iteratively
think, use tools, and act on observations to achieve user goals, dynamically
adapting their approach. Introduced in ["ReAct: Synergizing Reasoning and Acting
in Language Models"](https://arxiv.org/abs/2210.03629) (2023), this pattern
tries to mirror human-like, flexible problem-solving over rigid workflows.

LangGraph offers a prebuilt ReAct agent ([`create_react_agent`](https://langchain-ai.github.io/langgraph/reference/prebuilt/#langgraph.prebuilt.chat_agent_executor.create_react_agent)),
that shines when you need more control and customization for your ReAct
implementations. This guide will show you a simplified version.

LangGraph models agents as graphs using three key components:

- `State`: Shared data structure (typically `TypedDict` or `Pydantic BaseModel`) representing the application's current snapshot.
- `Nodes`: Encodes logic of your agents. They receive the current State as input, perform some computation or side-effect, and return an updated State, such as LLM calls or tool calls.
- `Edges`: Define the next `Node` to execute based on the current `State`, allowing for conditional logic and fixed transitions.

If you don't have an API Key yet, you can get one from [Google AI
Studio](https://aistudio.google.com/app/apikey).

    pip install langgraph langchain-google-genai geopy requests

Set your API key in the environment variable `GEMINI_API_KEY`.

    import os

    # Read your API key from the environment variable or set it manually
    api_key = os.getenv("GEMINI_API_KEY")

To better understand how to implement a ReAct agent using LangGraph, this guide
will walk through a practical example. You will create an agent whose goal is to
use a tool to find the current weather for a specified location.

For this weather agent, the `State` will maintain the ongoing conversation
history (as a list of messages) and a counter (as an integer) for the number of
steps taken, for illustrative purposes.

LangGraph provides a helper function, `add_messages`, for updating state message
lists. It functions as a [reducer](https://langchain-ai.github.io/langgraph/concepts/low_level/#reducers),
taking the current list, plus the new messages, and returns a combined list. It
handles updates by message ID and defaults to an "append-only" behavior for new,
unseen messages.

> [!NOTE]
> **Note:** Since having a list of messages in the state is common, there exists a prebuilt state called `MessagesState` that you can use as a base class. For this example the messages will be listed explicitly.

    from typing import Annotated,Sequence, TypedDict

    from langchain_core.messages import BaseMessage
    from langgraph.graph.message import add_messages  # helper function to add messages to the state


    class AgentState(TypedDict):
        """The state of the agent."""
        messages: Annotated[Sequence[BaseMessage], add_messages]
        number_of_steps: int

Next, define your weather tool.

    from langchain_core.tools import tool
    from geopy.geocoders import Nominatim
    from pydantic import BaseModel, Field
    import requests

    geolocator = Nominatim(user_agent="weather-app")

    class SearchInput(BaseModel):
        location:str = Field(description="The city and state, e.g., San Francisco")
        date:str = Field(description="the forecasting date for when to get the weather format (yyyy-mm-dd)")

    @tool("get_weather_forecast", args_schema=SearchInput, return_direct=True)
    def get_weather_forecast(location: str, date: str):
        """Retrieves the weather using Open-Meteo API.

        Takes a given location (city) and a date (yyyy-mm-dd).

        Returns:
            A dict with the time and temperature for each hour.
        """
        # Note that Colab may experience rate limiting on this service. If this
        # happens, use a machine to which you have exclusive access.
        location = geolocator.geocode(location)
        if location:
            try:
                response = requests.get(f"https://api.open-meteo.com/v1/forecast?latitude={location.latitude}&longitude={location.longitude}&hourly=temperature_2m&start_date={date}&end_date={date}")
                data = response.json()
                return dict(zip(data["hourly"]["time"], data["hourly"]["temperature_2m"]))
            except Exception as e:
                return {"error": str(e)}
        else:
            return {"error": "Location not found"}

    tools = [get_weather_forecast]

Now initialize the model and bind the tools to the model.

    from datetime import datetime
    from langchain_google_genai import ChatGoogleGenerativeAI

    # Create LLM class
    llm = ChatGoogleGenerativeAI(
        model= "gemini-3.5-flash",
        temperature=1.0,
        max_retries=2,
        google_api_key=api_key,
    )

    # Bind tools to the model
    model = llm.bind_tools([get_weather_forecast])

    # Test the model with tools
    res=model.invoke(f"What is the weather in Berlin on {datetime.today()}?")

    print(res)

The last step before you can run your agent is to define your nodes and edges.
In this example, you have two nodes and one edge.

- `call_tool` node that executes your tool method. LangGraph has a prebuilt node for this called [ToolNode](https://langchain-ai.github.io/langgraph/how-tos/tool-calling/).
- `call_model` node that uses the `model_with_tools` to call the model.
- `should_continue` edge that decides whether to call the tool or the model.

The number of nodes and edges is not fixed. You can add as many nodes and edges
as you want to your graph. For example, you could add a node for adding
structured output or a self-verification/reflection node to check the model
output before calling the tool or the model.

    from langchain_core.messages import ToolMessage
    from langchain_core.runnables import RunnableConfig

    tools_by_name = {tool.name: tool for tool in tools}

    # Define our tool node
    def call_tool(state: AgentState):
        outputs = []
        # Iterate over the tool calls in the last message
        for tool_call in state["messages"][-1].tool_calls:
            # Get the tool by name
            tool_result = tools_by_name[tool_call["name"]].invoke(tool_call["args"])
            outputs.append(
                ToolMessage(
                    content=tool_result,
                    name=tool_call["name"],
                    tool_call_id=tool_call["id"],
                )
            )
        return {"messages": outputs}

    def call_model(
        state: AgentState,
        config: RunnableConfig,
    ):
        # Invoke the model with the system prompt and the messages
        response = model.invoke(state["messages"], config)
        # This returns a list, which combines with the existing messages state
        # using the add_messages reducer.
        return {"messages": [response]}


    # Define the conditional edge that determines whether to continue or not
    def should_continue(state: AgentState):
        messages = state["messages"]
        # If the last message is not a tool call, then finish
        if not messages[-1].tool_calls:
            return "end"
        # default to continue
        return "continue"

With all of the agent components ready, you can now assemble them.

    from langgraph.graph import StateGraph, END

    # Define a new graph with our state
    workflow = StateGraph(AgentState)

    # 1. Add the nodes
    workflow.add_node("llm", call_model)
    workflow.add_node("tools",  call_tool)
    # 2. Set the entrypoint as `agent`, this is the first node called
    workflow.set_entry_point("llm")
    # 3. Add a conditional edge after the `llm` node is called.
    workflow.add_conditional_edges(
        # Edge is used after the `llm` node is called.
        "llm",
        # The function that will determine which node is called next.
        should_continue,
        # Mapping for where to go next, keys are strings from the function return,
        # and the values are other nodes.
        # END is a special node marking that the graph is finish.
        {
            # If `tools`, then we call the tool node.
            "continue": "tools",
            # Otherwise we finish.
            "end": END,
        },
    )
    # 4. Add a normal edge after `tools` is called, `llm` node is called next.
    workflow.add_edge("tools", "llm")

    # Now we can compile and visualize our graph
    graph = workflow.compile()

You can visualize your graph using the `draw_mermaid_png` method.

    from IPython.display import Image, display

    display(Image(graph.get_graph().draw_mermaid_png()))

![png](https://ai.google.dev/static/gemini-api/docs/images/langgraph-react-agent_16_0.png)

Now run the agent.

    from datetime import datetime
    # Create our initial message dictionary
    inputs = {"messages": [("user", f"What is the weather in Berlin on {datetime.today()}?")]}

    # call our graph with streaming to see the steps
    for state in graph.stream(inputs, stream_mode="values"):
        last_message = state["messages"][-1]
        last_message.pretty_print()

You can now continue with your conversation, ask for the weather in another
city, or request a comparison.

    state["messages"].append(("user", "Would it be warmer in Munich?"))

    for state in graph.stream(state, stream_mode="values"):
        last_message = state["messages"][-1]
        last_message.pretty_print()