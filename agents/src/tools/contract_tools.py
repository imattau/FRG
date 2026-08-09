"""
Contract deployment, calling, and state interaction.

Handles contract address derivation, Wasm workload loading,
and construction of deploy/call transactions.
"""

from __future__ import annotations

import hashlib
import struct
from pathlib import Path
from typing import Optional


DOMAIN_CONTRACT_DEPLOY = b"CONTRACT_DEPLOY_V1\x00"
DOMAIN_CONTRACT_CALL = b"CONTRACT_CALL_V1\x00"

GAS_DEPLOY_BASE = 100
GAS_DEPLOY_PER_BYTE = 1
GAS_CALL_BASE = 10
GAS_STORAGE_READ = 1
GAS_STORAGE_WRITE = 50
GAS_LOG_BASE = 1
MAX_WASM_BYTES = 1 << 20  # 1 MB


def contract_addr(deployer_pubkey: bytes, nonce: int) -> bytes:
    data = bytearray()
    data.extend(DOMAIN_CONTRACT_DEPLOY)
    data.extend(deployer_pubkey[:32])
    data.extend(struct.pack(">Q", nonce))
    return hashlib.sha256(bytes(data)).digest()


def estimate_deploy_gas(wasm_len: int, base_fee: int = 1) -> int:
    return base_fee * (GAS_DEPLOY_BASE + wasm_len * GAS_DEPLOY_PER_BYTE)


def estimate_call_gas(base_fee: int = 1) -> int:
    return base_fee * GAS_CALL_BASE


WORKLOADS_DIR = Path(__file__).parent.parent.parent / "workloads"

WORKLOAD_NAMES = ["trivial", "arithmetic", "hashing", "memory",
                  "state_read", "state_write", "heavy"]


def load_wasm(name: str) -> bytes:
    if not name.endswith(".wasm"):
        name = name + ".wasm"
    path = WORKLOADS_DIR / name
    if not path.is_file():
        available = [p.stem for p in WORKLOADS_DIR.glob("*.wasm")]
        raise FileNotFoundError(
            f"Workload '{name}' not found. Available: {available}"
        )
    return path.read_bytes()


def list_workloads() -> list[dict]:
    workloads = []
    for p in sorted(WORKLOADS_DIR.glob("*.wasm")):
        size = p.stat().st_size
        workloads.append({
            "name": p.stem,
            "size_bytes": size,
            "deploy_gas": estimate_deploy_gas(size),
        })
    return workloads


def make_call_data(func_name: str = "call") -> bytes:
    if len(func_name) >= 4:
        return func_name[:4].encode("ascii")
    return func_name.encode("ascii").ljust(4, b"\x00")[:4]
