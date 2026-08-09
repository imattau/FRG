"""
Fault Injector Agent: introduces controlled failures and observes recovery.

Actions: kill nodes, restart nodes, rapid kill/restart cycles,
and observing how consensus handles validator churn.
"""

from __future__ import annotations

import time
from .base import Agent, Tool


class FaultInjector(Agent):
    def __init__(self, devnet_info, **kwargs):
        super().__init__(name="FaultInjector", **kwargs)
        self.devnet_info = devnet_info
        self._faults_applied: int = 0
        self._setup_tools()

    def _setup_tools(self) -> None:
        from ..tools.devnet_tools import stop_node, start_node, restart_node
        from ..tools.node_tools import get_status

        info = self.devnet_info

        def kill_node(node_index: int, duration_seconds: int = 15) -> dict:
            ok = stop_node(info, node_index)
            if ok:
                self._faults_applied += 1
                if self.world:
                    self.world.alert(self.name, f"KILL: node-{node_index} stopped for {duration_seconds}s")
                time.sleep(duration_seconds)
                start_node(info, node_index)
                if self.world:
                    self.world.alert(self.name, f"RESTART: node-{node_index} restarted")
            return {
                "node": node_index,
                "killed": ok,
                "duration_s": duration_seconds,
                "restarted": ok,
            }

        def kill_multiple(indices: list[int], duration_seconds: int = 10) -> dict:
            results = []
            for i in indices:
                stop_node(info, i)
            self._faults_applied += len(indices)

            time.sleep(duration_seconds)

            for i in indices:
                start_node(info, i)
                results.append(f"node-{i} restarted")

            return {
                "killed": indices,
                "duration_s": duration_seconds,
                "results": results,
            }

        def check_recovery() -> dict:
            healthy = 0
            details = []
            for nd in info.nodes:
                try:
                    s = get_status(nd["grpc_addr"])
                    healthy += 1
                    details.append({
                        "node": nd["index"],
                        "height": s.height,
                        "phase": s.consensus_phase,
                        "peers": s.peer_count,
                    })
                except Exception as e:
                    details.append({
                        "node": nd["index"],
                        "error": str(e),
                    })
            return {"healthy_nodes": healthy, "total_nodes": len(info.nodes), "details": details}

        self.register_tool(Tool(
            name="kill_node",
            description="Stop a validator node for a duration, then restart it",
            parameters={
                "node_index": {"type": "integer", "description": "Node index to kill (0-based)"},
                "duration_seconds": {"type": "integer", "description": "How long to keep it down (default 15)"},
            },
            handler=kill_node,
        ))

        self.register_tool(Tool(
            name="kill_multiple",
            description="Stop multiple validator nodes simultaneously, then restart them",
            parameters={
                "indices": {"type": "array", "items": {"type": "integer"}, "description": "Node indices to kill"},
                "duration_seconds": {"type": "integer", "description": "How long to keep them down (default 10)"},
            },
            handler=kill_multiple,
        ))

        self.register_tool(Tool(
            name="check_recovery",
            description="Query all nodes to see if they've recovered after a fault",
            parameters={},
            handler=check_recovery,
        ))

        self.register_tool(Tool(
            name="restart_node",
            description="Restart a node without waiting (quick bounce)",
            parameters={
                "node_index": {"type": "integer", "description": "Node index to restart"},
            },
            handler=lambda node_index: {
                "node": node_index,
                "ok": restart_node(info, node_index),
            },
        ))

    def system_prompt(self) -> str:
        n = len(self.devnet_info.nodes)
        quorum = (2 * n) // 3 + 1
        return f"""You are a Fault Injector agent for a FRG blockchain devnet with {n} validators.

SHARED WORLD: Other agents are active while you inject faults:
  - NetworkMonitor: watching for consensus recovery after your kills
  - TrafficGenerator: sending transfers (their txs must still get through)
  - ContractTester: deploying contracts (their operations must survive node failures)
  - Adversarial: launching attacks

Your job: test the network's resilience while the chain is actively processing
traffic and contracts. The chain keeps running — you just take nodes away.

Safety rules:
- Never kill more than {n - quorum} nodes at once (need {quorum} for BFT quorum).
- Always wait for full recovery before injecting the next fault.
- Send alerts when you kill a node so NetworkMonitor knows to watch.

Test scenarios:
1. Kill one non-leader node (e.g., node-2) for 6 seconds → verify recovery
2. Kill a different node (node-1) for 5 seconds while traffic is flowing
3. Check recovery after each kill

The TrafficGenerator is sending transfers while you work. The NetworkMonitor
is watching. Make sure the chain stays alive through your fault injection.
When you've tested both kills and network recovered, signal DONE.
"""  # noqa: E501
