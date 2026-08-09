"""
Analyst Agent: correlates observations from all agents, identifies anomalies,
and generates test reports.

This agent does not take actions on the network — it only reads and analyzes.
"""

from __future__ import annotations

from .base import Agent, Tool


class Analyst(Agent):
    def __init__(self, devnet_info, report, **kwargs):
        super().__init__(name="Analyst", **kwargs)
        self.devnet_info = devnet_info
        self.report = report
        self._setup_tools()

    def _setup_tools(self) -> None:
        from ..tools.analysis_tools import snapshot
        from ..tools.node_tools import get_status

        info = self.devnet_info
        rpt = self.report

        def take_snapshot() -> dict:
            snap = snapshot(info)
            rpt.snapshots.append(snap)
            return {
                "heights": snap.heights,
                "max_height": snap.max_height,
                "min_height": snap.min_height,
                "height_spread": snap.height_spread,
                "divergent_roots": len(snap.divergent_roots()) > 0,
            }

        def check_consensus(max_spread: int = 2) -> dict:
            from ..tools.analysis_tools import check_consensus as do_check

            ok, detail = do_check(info, max_spread)
            rpt.consensus_checks += 1
            if not ok:
                rpt.consensus_failures += 1
                rpt.anomalies.append({
                    "type": "consensus_failure",
                    "detail": detail,
                })
            return {"consensus_ok": ok, "detail": detail}

        def query_leader() -> dict:
            try:
                s = get_status(info.nodes[0]["grpc_addr"])
                return {
                    "height": s.height,
                    "state_root": s.state_root[:16],
                    "phase": s.consensus_phase,
                    "round": s.consensus_round,
                    "peers": s.peer_count,
                    "mempool": s.mempool_len,
                }
            except Exception as e:
                return {"error": str(e)}

        def get_report_summary() -> dict:
            return rpt.to_dict()

        self.register_tool(Tool(
            name="take_snapshot",
            description="Record a point-in-time snapshot of all node states for the report",
            parameters={},
            handler=take_snapshot,
        ))

        self.register_tool(Tool(
            name="check_consensus",
            description="Verify all nodes agree on state root and are within height spread",
            parameters={
                "max_spread": {"type": "integer", "description": "Max acceptable height difference between nodes (default 2)"},
            },
            handler=check_consensus,
        ))

        self.register_tool(Tool(
            name="query_leader",
            description="Get detailed status from node-0 (the initial leader)",
            parameters={},
            handler=query_leader,
        ))

        self.register_tool(Tool(
            name="get_report_summary",
            description="Get current test report statistics",
            parameters={},
            handler=get_report_summary,
        ))

    def system_prompt(self) -> str:
        return """You are an Analyst agent for a FRG blockchain devnet test run.

SHARED WORLD: You compile the final report from all agents' activities:
  - NetworkMonitor: recorded consensus checks and anomalies
  - TrafficGenerator: submitted transfers (txs_submitted counter in report)
  - ContractTester: deployed and called contracts (contracts_deployed/called counters)
  - Adversarial: ran attacks (all should have been rejected)
  - FaultInjector: killed nodes (faults_injected counter)

Your job:
1. Take a final snapshot of all nodes.
2. Run check_consensus to verify state roots match.
3. Use get_report_summary to see the auto-incremented counters from all agents.
4. Compare the report counters against what you see in the world events.
5. Compile a recommendation: did the chain function correctly under load,
   under attack, and under node failures?

When you have a complete picture and the report is accurate, signal DONE.
"""
