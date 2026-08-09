"""
Node interaction via frg-cli subprocess.

All gRPC queries go through the frg-cli binary, which handles the
frg-json codec correctly. Parse the text output for structured data.
"""

from __future__ import annotations

import re
import subprocess
from dataclasses import dataclass
from pathlib import Path
from typing import Optional


def _find_frg_cli() -> str:
    for path in [
        "./frg-cli",
        "../frg-cli",
        str(Path(__file__).parent.parent.parent.parent / "frg-cli"),
    ]:
        if os.path.isfile(path) and os.access(path, os.X_OK):
            return os.path.abspath(path)
    return "frg-cli"

import os

_FRG_CLI = _find_frg_cli()


def _run_cli(args: list[str], timeout: int = 10) -> subprocess.CompletedProcess:
    return subprocess.run(
        [_FRG_CLI] + args,
        capture_output=True, text=True, timeout=timeout,
    )


@dataclass
class NodeStatus:
    height: int
    state_root: str
    peer_count: int
    mempool_len: int
    validator_count: int
    consensus_round: int
    consensus_phase: str
    grpc_only: bool


def get_status(grpc_addr: str = "127.0.0.1:50051") -> NodeStatus:
    result = _run_cli(["status", "--addr", grpc_addr])
    if result.returncode != 0:
        raise RuntimeError(f"status query failed: {result.stderr.strip()}")

    out = result.stdout
    return NodeStatus(
        height=_parse_int(out, r"height:\s+(\d+)"),
        state_root=_parse_str(out, r"state_root:\s+([0-9a-fA-F]+)"),
        peer_count=_parse_int(out, r"peers:\s+(\d+)"),
        mempool_len=_parse_int(out, r"mempool:\s+(\d+)"),
        validator_count=_parse_int(out, r"validators:\s+(\d+)"),
        consensus_round=_parse_int(out, r"consensus_round:\s+(\d+)"),
        consensus_phase=_parse_str(out, r"consensus_phase:\s+(\S+)"),
        grpc_only="grpc_only:         true" in out,
    )


@dataclass
class AccountInfo:
    pubkey: str
    balance: str
    nonce: int


def get_account(pubkey_hex: str, grpc_addr: str = "127.0.0.1:50051") -> AccountInfo:
    result = _run_cli(["balance", pubkey_hex, "--addr", grpc_addr])
    if result.returncode != 0:
        raise RuntimeError(f"balance query failed: {result.stderr.strip()}")

    out = result.stdout
    return AccountInfo(
        pubkey=_parse_str(out, r"pubkey:\s+([0-9a-fA-F]+)"),
        balance=_parse_str(out, r"balance:\s+([0-9]+)"),
        nonce=_parse_int(out, r"nonce:\s+(\d+)"),
    )


def submit_tx(tx_hex: str, grpc_addr: str = "127.0.0.1:50051") -> tuple[bool, str]:
    result = _run_cli(["submit", "--tx", tx_hex, "--addr", grpc_addr])
    if result.returncode != 0:
        err = result.stderr.strip()
        if "rejected:" in err:
            return False, err.split("rejected:", 1)[1].strip()
        return False, err
    return True, ""


def get_all_statuses(info) -> list[dict]:
    statuses = []
    for node in info.nodes:
        try:
            s = get_status(node["grpc_addr"])
            statuses.append({
                "node": node["index"],
                "height": s.height,
                "state_root": s.state_root,
                "peers": s.peer_count,
                "mempool": s.mempool_len,
                "phase": s.consensus_phase,
            })
        except Exception as e:
            statuses.append({
                "node": node["index"],
                "error": str(e),
            })
    return statuses


def wait_for_height(
    grpc_addr: str, target: int, timeout: float = 60.0
) -> Optional[NodeStatus]:
    import time
    deadline = time.time() + timeout
    while time.time() < deadline:
        try:
            s = get_status(grpc_addr)
            if s.height >= target:
                return s
        except Exception:
            pass
        time.sleep(1.0)
    return None


def _parse_int(text: str, pattern: str, default: int = 0) -> int:
    m = re.search(pattern, text)
    return int(m.group(1)) if m else default


def _parse_str(text: str, pattern: str, default: str = "") -> str:
    m = re.search(pattern, text)
    return m.group(1) if m else default
