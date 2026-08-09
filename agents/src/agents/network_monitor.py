"""
Network Monitor Agent: observes consensus health, block production, and peer connectivity.

Periodically polls all nodes for status, checks state root agreement,
detects stalls, forks, and peer isolation.
"""

from __future__ import annotations

from .base import Agent, Tool


class NetworkMonitor(Agent):
    def __init__(self, devnet_info, **kwargs):
        super().__init__(name="NetworkMonitor", **kwargs)
        self.devnet_info = devnet_info
        self._setup_tools()

    def _setup_tools(self) -> None:
        from ..tools.node_tools import get_status, get_all_statuses
        from ..tools.devnet_tools import get_node_logs

        self.register_tool(Tool(
            name="poll_all_nodes",
            description="Query status (height, state_root, phase) from all devnet nodes",
            parameters={
                "reason": {"type": "string", "description": "Why you're polling"},
            },
            handler=lambda reason="": get_all_statuses(self.devnet_info),
        ))

        self.register_tool(Tool(
            name="poll_node",
            description="Query detailed status from a single node",
            parameters={
                "node_index": {"type": "integer", "description": "Node index (0-based)"},
                "reason": {"type": "string", "description": "Why you're querying this node"},
            },
            handler=lambda node_index, reason="": self._poll_one(node_index),
        ))

        self.register_tool(Tool(
            name="check_logs",
            description="Read recent logs from a node to investigate issues",
            parameters={
                "node_index": {"type": "integer", "description": "Node index (0-based)"},
            },
            handler=lambda node_index: get_node_logs(node_index, tail=30),
        ))

    def _poll_one(self, node_index: int) -> dict:
        try:
            addr = self.devnet_info.nodes[node_index]["grpc_addr"]
            s = get_status(addr)
            return {
                "node": node_index,
                "height": s.height,
                "state_root": s.state_root,
                "peers": s.peer_count,
                "mempool": s.mempool_len,
                "phase": s.consensus_phase,
                "round": s.consensus_round,
            }
        except Exception as e:
            return {"node": node_index, "error": str(e)}

    def system_prompt(self) -> str:
        n = len(self.devnet_info.nodes)
        return f"""You are a Network Monitor agent for a FRG blockchain devnet with {n} validator nodes.

SHARED WORLD: You are part of a multi-agent test swarm. Other agents running:
  - TrafficGenerator: sends transfers between accounts
  - ContractTester: deploys and calls Wasm smart contracts
  - Adversarial: attempts exploits and attacks
  - FaultInjector: kills and restarts validator nodes
  - Analyst: compiles the final test report

You will see events from these agents. React to them:
  - If Adversarial reports an attack, immediately poll all nodes to verify health
  - If FaultInjector kills a node, watch that node's recovery
  - If ContractTester deploys a contract, verify state roots still match
  - If TrafficGenerator submits a batch, check if mempool is being drained

Your job:
1. Poll all nodes regularly to track block height, state roots, and consensus phase.
2. Detect anomalies: state root divergence (fork), height stalls, nodes falling behind peers.
3. If you see an alert from another agent about an anomaly, investigate immediately.
4. When the network has been stable for several observations, signal DONE.

Normal behavior:
- All nodes should have the same state_root at each height.
- Height should increase steadily (block time ~500ms).
- Consensus phase should cycle through propose→prevote→precommit.
- Peer count should be at least {n-1} (all other validators).
"""

    def on_world_event(self, event: dict) -> None:
        agent = event.get("agent", "")
        msg = event.get("message", "")
        if agent == "Adversarial" and "attack" in msg.lower():
            self.observe("Adversarial agent reported attack results — I should verify network health immediately.")
        elif agent == "FaultInjector" and "kill" in msg.lower():
            self.observe("FaultInjector killed a node — I need to watch recovery.")
        elif agent == "ContractTester" and "deploy" in msg.lower():
            self.observe("Contract deployed — I should verify state roots across nodes.")
