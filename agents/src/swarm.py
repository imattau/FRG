"""
Swarm orchestrator: manages agent lifecycle, shared state, and coordination.

The swarm spawns specialized agents, collects their results, and ensures
they don't interfere with each other.
"""

from __future__ import annotations

import asyncio
import time
from dataclasses import dataclass, field
from typing import Optional, Type

from .agents.base import Agent
from .agents.network_monitor import NetworkMonitor
from .agents.traffic_generator import TrafficGenerator
from .agents.contract_tester import ContractTester
from .agents.fault_injector import FaultInjector
from .agents.adversarial import AdversarialAgent
from .agents.analyst import Analyst
from .tools.analysis_tools import TestReport
from .world import SharedWorld


AGENT_REGISTRY: dict[str, Type[Agent]] = {
    "monitor": NetworkMonitor,
    "traffic": TrafficGenerator,
    "contract": ContractTester,
    "fault": FaultInjector,
    "adversarial": AdversarialAgent,
    "analyst": Analyst,
}


@dataclass
class SwarmConfig:
    model: str = "llama3.2"
    host: Optional[str] = None
    max_iterations: int = 30
    cooldown_between_phases: float = 5.0


@dataclass
class AgentResult:
    name: str
    agent_type: str
    summary: dict
    start_time: float
    end_time: float
    success: bool = True


@dataclass
class SwarmState:
    results: list[AgentResult] = field(default_factory=list)
    errors: list[str] = field(default_factory=list)
    active_agents: dict[str, Agent] = field(default_factory=dict)


class Swarm:
    def __init__(
        self,
        devnet_info,
        config: Optional[SwarmConfig] = None,
    ):
        self.devnet_info = devnet_info
        self.config = config or SwarmConfig()
        self.report = TestReport()
        self.state = SwarmState()
        self.world = SharedWorld(report=self.report)

    def _make_agent(self, agent_type: str) -> Agent:
        agent_cls = AGENT_REGISTRY[agent_type]
        if agent_type == "analyst":
            return agent_cls(
                self.devnet_info,
                self.report,
                model=self.config.model,
                host=self.config.host,
                max_iterations=self.config.max_iterations,
                world=self.world,
            )
        return agent_cls(
            self.devnet_info,
            model=self.config.model,
            host=self.config.host,
            max_iterations=self.config.max_iterations,
            world=self.world,
        )

    async def run_agent(self, agent: Agent, initial_observation: str = "") -> AgentResult:
        loop = asyncio.get_event_loop()
        start = time.time()

        try:
            reason = await loop.run_in_executor(
                None, agent.run, initial_observation
            )
            result = AgentResult(
                name=agent.name,
                agent_type=agent.name,
                summary=agent.get_summary(),
                start_time=start,
                end_time=time.time(),
                success="DONE" in reason.upper() or "COMPLETE" in reason.upper(),
            )
        except Exception as e:
            result = AgentResult(
                name=agent.name,
                agent_type=agent.name,
                summary={"error": str(e)},
                start_time=start,
                end_time=time.time(),
                success=False,
            )

        self.state.results.append(result)
        return result

    async def run_phase(
        self,
        agent_types: list[str],
        observations: Optional[dict[str, str]] = None,
        parallel: bool = False,
    ) -> list[AgentResult]:
        if observations is None:
            observations = {}

        if parallel:
            agents = [
                (at, self._make_agent(at))
                for at in agent_types
            ]
            tasks = [
                self.run_agent(agent, observations.get(at, ""))
                for at, agent in agents
            ]
            results = await asyncio.gather(*tasks, return_exceptions=True)
            return [
                r if not isinstance(r, Exception)
                else AgentResult(
                    name="error", agent_type="error",
                    summary={"error": str(r)},
                    start_time=0, end_time=0, success=False,
                )
                for r in results
            ]
        else:
            results = []
            for at in agent_types:
                agent = self._make_agent(at)
                obs = observations.get(at, "")
                result = await self.run_agent(agent, obs)
                results.append(result)
                await asyncio.sleep(self.config.cooldown_between_phases)
            return results

    def collect_and_report(self) -> str:
        from .tools.analysis_tools import check_consensus

        self.report.finalize()

        ok, detail = check_consensus(self.devnet_info)
        if not ok:
            self.report.consensus_failures += 1
            self.report.anomalies.append({
                "type": "final_consensus_check",
                "detail": detail,
            })

        report_text = self.report.summary_text()
        report_text += "\n\nWorld Events: " + str(len(self.world.events)) + " total\n"
        report_text += f"  Last events:\n{self.world.recent_events_summary(5)}\n"

        report_text += "\n\nAgent Results:\n"
        for r in self.state.results:
            report_text += (
                f"  {r.agent_type}: "
                f"{'OK' if r.success else 'FAIL'} "
                f"({r.summary.get('actions_taken', 0)} actions)\n"
            )
        if self.state.errors:
            report_text += "\nErrors:\n"
            for e in self.state.errors:
                report_text += f"  - {e}\n"

        return report_text

    async def run_scenario(
        self,
        duration: float,
        schedule: list[tuple[str, float, str]],
    ) -> dict[str, AgentResult]:
        """Run agents on a timeline, all sharing the SharedWorld.

        schedule: list of (agent_type, start_delay_seconds, initial_observation)
        Agents run concurrently in thread pool, coordinated via SharedWorld.
        """
        spawned: dict[str, Agent] = {}
        loop = asyncio.get_event_loop()

        async def spawn_delayed(agent_type: str, delay: float, observation: str):
            await asyncio.sleep(delay)
            agent = self._make_agent(agent_type)
            self.world.set_agent_status(agent.name, f"started at t+{delay:.0f}s")
            print(f"  [{agent.name}] spawned at t+{delay:.0f}s: {observation[:60]}...")
            loop.run_in_executor(None, agent.run, observation)
            spawned[agent_type] = agent

        await asyncio.gather(*[
            spawn_delayed(at, delay, obs)
            for at, delay, obs in schedule
        ])

        remaining = max(duration - max(d for _, d, _ in schedule), 5.0)
        print(f"\n  All {len(spawned)} agents spawned. Running for {remaining:.0f}s...\n")
        await asyncio.sleep(remaining)

        results = {}
        for at, agent in spawned.items():
            agent._running = False
            summary = agent.get_summary()
            results[at] = AgentResult(
                name=agent.name,
                agent_type=at,
                summary=summary,
                start_time=0,
                end_time=time.time(),
                success=True,
            )
            self.state.results.append(results[at])

        return results
        from .tools.analysis_tools import check_consensus

        self.report.finalize()

        ok, detail = check_consensus(self.devnet_info)
        if not ok:
            self.report.consensus_failures += 1
            self.report.anomalies.append({
                "type": "final_consensus_check",
                "detail": detail,
            })

        report_text = self.report.summary_text()
        report_text += "\n\nAgent Results:\n"
        for r in self.state.results:
            report_text += (
                f"  {r.agent_type}: "
                f"{'OK' if r.success else 'FAIL'} "
                f"({r.summary.get('actions_taken', 0)} actions)\n"
            )
        if self.state.errors:
            report_text += "\nErrors:\n"
            for e in self.state.errors:
                report_text += f"  - {e}\n"

        return report_text


async def run_default_swarm(devnet_info, config: Optional[SwarmConfig] = None) -> tuple[Swarm, str]:
    swarm = Swarm(devnet_info, config)

    schedule = [
        ("monitor",     0,   "Watch the network from genesis. Verify all nodes produce blocks, state roots match, consensus holds."),
        ("traffic",     3,   "Generate steady transfer load. Send batch_transfer(count=10, amount=1) every few iterations. This is a real chain being used."),
        ("contract",    5,   "Alongside traffic: list workloads, deploy 'trivial', call it, then deploy 'arithmetic'. Check consensus after each."),
        ("adversarial", 25,  "The chain is busy with traffic and contracts. Now attack: invalid_signature, wrong_signer, double_spend, invalid_tx_type. Verify all rejected, no crashes."),
        ("fault",       40,  "Kill node-2 for 8 seconds while traffic continues. Check recovery after. Then kill node-1 briefly."),
        ("analyst",     55,  "Take final snapshot. Check consensus. Use report counters from SharedWorld to compile findings. Signal DONE."),
    ]

    await swarm.run_scenario(duration=75, schedule=schedule)
    report = swarm.collect_and_report()
    return swarm, report
