"""
Analysis tools: state root comparison, consensus verification, metrics aggregation.
"""

from __future__ import annotations

import time
from dataclasses import dataclass, field
from typing import Optional

from .node_tools import get_status, get_all_statuses, NodeStatus


@dataclass
class NetworkSnapshot:
    timestamp: float
    nodes: list[dict]

    @property
    def heights(self) -> list[int]:
        return [n.get("height", 0) for n in self.nodes if "height" in n]

    @property
    def max_height(self) -> int:
        h = self.heights
        return max(h) if h else 0

    @property
    def min_height(self) -> int:
        h = self.heights
        return min(h) if h else 0

    @property
    def height_spread(self) -> int:
        return self.max_height - self.min_height

    def state_roots(self) -> list[tuple[int, str]]:
        return [(n.get("node", -1), n.get("state_root", ""))
                for n in self.nodes if "state_root" in n]

    def divergent_roots(self) -> list[dict]:
        roots: dict[str, list[int]] = {}
        for n in self.nodes:
            if "state_root" not in n:
                continue
            r = n["state_root"]
            roots.setdefault(r, []).append(n["node"])
        if len(roots) == 1:
            return []
        diverged = []
        for root, node_list in roots.items():
            diverged.append({"state_root": root, "nodes": node_list})
        return diverged


def snapshot(info) -> NetworkSnapshot:
    statuses = get_all_statuses(info)
    return NetworkSnapshot(timestamp=time.time(), nodes=statuses)


def check_consensus(info, max_spread: int = 2) -> tuple[bool, str]:
    snap = snapshot(info)
    if snap.divergent_roots():
        d = snap.divergent_roots()
        detail = "; ".join(
            f"root={r['state_root'][:16]}... nodes={r['nodes']}" for r in d
        )
        return False, f"State root divergence: {detail}"

    if snap.height_spread > max_spread:
        return False, (
            f"Height spread {snap.height_spread} > {max_spread} "
            f"(min={snap.min_height}, max={snap.max_height})"
        )

    return True, f"OK (height range {snap.min_height}-{snap.max_height})"


@dataclass
class TestReport:
    start_time: float = field(default_factory=time.time)
    end_time: float = 0
    txs_submitted: int = 0
    txs_failed: int = 0
    contracts_deployed: int = 0
    contracts_called: int = 0
    faults_injected: int = 0
    consensus_checks: int = 0
    consensus_failures: int = 0
    snapshots: list[NetworkSnapshot] = field(default_factory=list)
    anomalies: list[dict] = field(default_factory=list)

    def finalize(self):
        self.end_time = time.time()

    @property
    def duration_seconds(self) -> float:
        return self.end_time - self.start_time

    @property
    def tps(self) -> float:
        if self.duration_seconds <= 0:
            return 0
        return self.txs_submitted / self.duration_seconds

    def to_dict(self) -> dict:
        return {
            "duration_s": round(self.duration_seconds, 1),
            "txs_submitted": self.txs_submitted,
            "txs_failed": self.txs_failed,
            "contracts_deployed": self.contracts_deployed,
            "contracts_called": self.contracts_called,
            "faults_injected": self.faults_injected,
            "consensus_checks": self.consensus_checks,
            "consensus_failures": self.consensus_failures,
            "anomalies": self.anomalies,
            "final_tps": round(self.tps, 1),
        }

    def summary_text(self) -> str:
        d = self.to_dict()
        lines = [
            "=== Test Report ===",
            f"Duration:           {d['duration_s']}s",
            f"TXs submitted:      {d['txs_submitted']}",
            f"TXs failed:         {d['txs_failed']}",
            f"Contracts deployed:  {d['contracts_deployed']}",
            f"Contracts called:    {d['contracts_called']}",
            f"Faults injected:     {d['faults_injected']}",
            f"Consensus checks:    {d['consensus_checks']}",
            f"Consensus failures:  {d['consensus_failures']}",
            f"Final TPS:           {d['final_tps']}",
        ]
        if self.anomalies:
            lines.append(f"Anomalies:           {len(self.anomalies)}")
            for a in self.anomalies:
                lines.append(f"  - {a.get('type', '?')}: {a.get('detail', '')}")
        return "\n".join(lines)
