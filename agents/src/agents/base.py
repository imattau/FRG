"""
Base agent: ReAct loop with ollama tool calling.

Each agent runs an observe-reason-act loop. Tools are defined as
JSON schemas that ollama uses for function calling. Agent state
(observation history, action log) is tracked for context.
"""

from __future__ import annotations

import json
import time
from abc import ABC, abstractmethod
from dataclasses import dataclass, field
from typing import Any, Callable, Optional

import ollama


@dataclass
class Tool:
    name: str
    description: str
    parameters: dict
    handler: Callable[..., Any]


@dataclass
class Observation:
    timestamp: float
    content: str


@dataclass
class Action:
    timestamp: float
    tool: str
    args: dict
    result: Any


@dataclass
class AgentState:
    observations: list[Observation] = field(default_factory=list)
    actions: list[Action] = field(default_factory=list)
    memory: dict = field(default_factory=dict)


class Agent(ABC):
    def __init__(
        self,
        name: str,
        model: str = "llama3.2",
        host: Optional[str] = None,
        max_iterations: int = 50,
        world: Any = None,
    ):
        self.name = name
        self.model = model
        self.host = host
        self.max_iterations = max_iterations
        self.state = AgentState()
        self.tools: dict[str, Tool] = {}
        self._running = False
        self.world = world
        self._alert_index: int = 0

    def register_tool(self, tool: Tool) -> None:
        self.tools[tool.name] = tool

    @abstractmethod
    def system_prompt(self) -> str:
        ...

    def _build_messages(self) -> list[dict]:
        messages: list[dict] = [
            {"role": "system", "content": self.system_prompt()},
        ]

        if self.world:
            messages.append({
                "role": "system",
                "content": (
                    "SHARED WORLD CONTEXT — Other agents are active:\n"
                    + self.world.active_agents_summary()
                    + "\n\nRecent world events:\n"
                    + self.world.recent_events_summary(12)
                ),
            })

        history = []
        for obs in self.state.observations[-15:]:
            history.append({
                "role": "user",
                "content": f"[OBSERVATION at t={obs.timestamp:.0f}] {obs.content}",
            })
        for act in self.state.actions[-15:]:
            history.append({
                "role": "assistant",
                "content": f"Used tool '{act.tool}' with args {json.dumps(act.args)} → result: {json.dumps(act.result)}",
            })
        messages.extend(history)

        if self.state.observations:
            messages.append({
                "role": "user",
                "content": (
                    f"Latest observation: {self.state.observations[-1].content}\n\n"
                    "What action should you take next? Respond with a tool call, "
                    "or if you believe your objective is complete, say DONE and explain why."
                ),
            })
        else:
            messages.append({
                "role": "user",
                "content": "Begin your task. What is your first action?",
            })

        return messages

    def _tool_schemas(self) -> list[dict]:
        schemas = []
        for tool in self.tools.values():
            schemas.append({
                "type": "function",
                "function": {
                    "name": tool.name,
                    "description": tool.description,
                    "parameters": {
                        "type": "object",
                        "properties": tool.parameters,
                        "required": list(tool.parameters.keys()),
                    },
                },
            })
        return schemas

    def observe(self, content: str) -> None:
        self.state.observations.append(Observation(
            timestamp=time.time(),
            content=content,
        ))

    def _execute_action(self, tool_name: str, args: dict) -> Any:
        tool = self.tools.get(tool_name)
        if not tool:
            return f"Unknown tool: {tool_name}"

        try:
            result = tool.handler(**args)
        except Exception as e:
            result = f"Error: {e}"

        self.state.actions.append(Action(
            timestamp=time.time(),
            tool=tool_name,
            args=args,
            result=result,
        ))

        if self.world:
            if isinstance(result, dict):
                self.world.broadcast(self.name, tool_name, result)
            else:
                self.world.broadcast(self.name, tool_name, {"result": str(result)})

        return result

    def on_world_event(self, event: Any) -> None:
        pass

    def _check_world_alerts(self) -> None:
        if not self.world:
            return
        new_alerts, self._alert_index = self.world.get_new_alerts(self._alert_index)
        for alert in new_alerts:
            if alert["agent"] != self.name:
                msg = f"ALERT from {alert['agent']}: {alert['message']}"
                self.observe(msg)
                self.on_world_event(alert)

    def step(self) -> Optional[str]:
        self._check_world_alerts()

        messages = self._build_messages()
        schemas = self._tool_schemas()

        kwargs = {
            "model": self.model,
            "messages": messages,
        }
        if self.host:
            kwargs["host"] = self.host
        if schemas:
            kwargs["tools"] = schemas

        response = ollama.chat(**kwargs)

        content = response.get("message", {}).get("content", "").strip()
        tool_calls = response.get("message", {}).get("tool_calls", [])

        if tool_calls:
            for tc in tool_calls:
                func = tc.get("function", {})
                name = func.get("name", "")
                args = func.get("arguments", {})
                if isinstance(args, str):
                    try:
                        args = json.loads(args)
                    except json.JSONDecodeError:
                        args = {}
                result = self._execute_action(name, args)

            new_msg = f"Tool '{name}' returned: {json.dumps(result)}"
            self.observe(new_msg)
            return None

        if "DONE" in content.upper() or "COMPLETE" in content.upper():
            self.state.memory["completion_reason"] = content
            if self.world:
                self.world.broadcast(self.name, "done", {"reason": content})
                self.world.set_agent_status(self.name, "complete")
            return "DONE"

        self.observe(content)
        return None

    def run(self, initial_observation: str = "") -> str:
        self._running = True
        self.observe(initial_observation or "Begin your assigned task.")

        for _ in range(self.max_iterations):
            status = self.step()
            if status == "DONE":
                break
            time.sleep(0.5)

        self._running = False
        return self.state.memory.get("completion_reason", "Max iterations reached")

    def get_summary(self) -> dict:
        return {
            "name": self.name,
            "observations": len(self.state.observations),
            "actions_taken": len(self.state.actions),
            "completion_reason": self.state.memory.get("completion_reason", ""),
        }
