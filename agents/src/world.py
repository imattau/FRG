"""
SharedWorld: event bus and shared state for multi-agent coordination.

All agents read and write to the same world. Events are broadcast on every
tool execution. Report counters auto-increment based on known event types.
Inter-agent alerts allow reactive behavior.
"""

from __future__ import annotations

import threading
import time
from dataclasses import dataclass, field
from typing import Any, Optional

from .tools.analysis_tools import TestReport


@dataclass
class WorldEvent:
    timestamp: float
    agent: str
    event_type: str
    data: dict


@dataclass
class SharedWorld:
    report: TestReport = field(default_factory=TestReport)
    events: list[WorldEvent] = field(default_factory=list)
    alerts: list[dict] = field(default_factory=list)
    agent_status: dict[str, str] = field(default_factory=dict)
    lock: threading.Lock = field(default_factory=threading.Lock)

    def broadcast(self, agent: str, event_type: str, data: Optional[dict] = None) -> None:
        if data is None:
            data = {}
        event = WorldEvent(
            timestamp=time.time(),
            agent=agent,
            event_type=event_type,
            data=data,
        )
        with self.lock:
            self.events.append(event)
            self._update_counters(event_type, data)

    def _update_counters(self, event_type: str, data: dict) -> None:
        if event_type in ("send_transfer",):
            self.report.txs_submitted += 1
        elif event_type == "batch_transfer":
            self.report.txs_submitted += data.get("submitted", data.get("count", 0))
            self.report.txs_failed += data.get("failed", 0)
        elif event_type == "deploy_contract":
            if data.get("ok"):
                self.report.contracts_deployed += 1
        elif event_type == "call_contract":
            if data.get("ok"):
                self.report.contracts_called += 1
        elif event_type in ("kill_node",):
            if data.get("killed"):
                self.report.faults_injected += 1
        elif event_type == "kill_multiple":
            self.report.faults_injected += len(data.get("killed", []))
        elif event_type == "check_consensus":
            self.report.consensus_checks += 1
            if not data.get("consensus_ok", True):
                self.report.consensus_failures += 1

    def recent_events(self, n: int = 10, agent_filter: Optional[str] = None) -> list[WorldEvent]:
        with self.lock:
            events = list(self.events)
        if agent_filter:
            events = [e for e in events if e.agent == agent_filter]
        return events[-n:]

    def recent_events_summary(self, n: int = 15) -> str:
        events = self.recent_events(n)
        if not events:
            return "  (no events yet)"

        lines = []
        for e in events:
            ts = f"t={e.timestamp:.0f}"
            data_str = self._summarize_data(e.event_type, e.data)
            lines.append(f"  [{ts}] {e.agent}: {e.event_type} → {data_str}")
        return "\n".join(lines)

    def _summarize_data(self, event_type: str, data: dict) -> str:
        def _safe_heights(obj) -> str:
            items = []
            if isinstance(obj, list):
                items = obj
            elif isinstance(obj, dict):
                items = obj.get("details", obj.get("nodes", []))
                if isinstance(items, dict):
                    items = [items]
            if not items:
                return "[]"
            try:
                heights = [n.get("height", "?") if isinstance(n, dict) else "?" for n in items]
                return str(heights)
            except Exception:
                return "[...]"

        if event_type in ("poll_all_nodes", "check_recovery", "check_network_health",
                          "poll_node_status", "query_leader", "check_consensus"):
            return f"heights={_safe_heights(data)}"
        if event_type in ("get_account_nonce", "query_contract_balance"):
            flat = ", ".join(f"{k}={v}" for k, v in list(data.items())[:3])
            return flat[:60]
        if event_type == "deploy_contract":
            return f"ok={data.get('ok')} addr={data.get('contract_addr', '?')[:12]}..."
        if event_type == "call_contract":
            return f"ok={data.get('ok')} func={data.get('function', '?')}"
        if event_type == "batch_transfer":
            return f"submitted={data.get('submitted',0)} failed={data.get('failed',0)}"
        if event_type == "send_transfer":
            return f"ok={data.get('ok')} amount={data.get('amount', '?')}"
        if event_type == "kill_node":
            return f"node={data.get('node')} duration={data.get('duration_s')}s"
        if event_type == "kill_multiple":
            return f"killed={data.get('killed', [])}"
        if event_type == "take_snapshot":
            return f"heights={_safe_heights(data)}"
        if event_type == "get_report_summary":
            return f"txs={data.get('txs_submitted',0)} contracts={data.get('contracts_deployed',0)}"
        if event_type in ("attack_invalid_signature", "attack_wrong_signer",
                          "attack_double_spend", "attack_invalid_tx_type",
                          "attack_malformed_serialization", "attack_mempool_flood",
                          "attack_nonce_skip", "attack_insufficient_funds",
                          "attack_call_nonexistent_contract", "attack_deploy_empty_wasm",
                          "attack_calldata_overflow", "attack_invalid_domain",
                          "attack_nfc_encoding", "attack_value_zero", "attack_replay",
                          "attack_max_size_tx", "attack_deploy_oversized_wasm",
                          "attack_null_values"):
            return f"accepted={data.get('accepted')} rejection={str(data.get('rejection',''))[:40]}"
        if isinstance(data, dict) and data:
            flat = ", ".join(f"{k}={v}" for k, v in list(data.items())[:3])
            return str(flat)[:80]
        return str(data)[:60]

    def alert(self, agent: str, message: str) -> None:
        with self.lock:
            self.alerts.append({
                "timestamp": time.time(),
                "agent": agent,
                "message": message,
            })

    def get_new_alerts(self, since_index: int) -> tuple[list[dict], int]:
        with self.lock:
            new = self.alerts[since_index:]
            return list(new), len(self.alerts)

    def set_agent_status(self, agent: str, status: str) -> None:
        with self.lock:
            self.agent_status[agent] = status

    def active_agents_summary(self) -> str:
        with self.lock:
            items = list(self.agent_status.items())
        if not items:
            return "  (no agents registered)"
        return "\n".join(f"  - {name}: {status}" for name, status in items)
