"""
Simulation mode: runs the collaborative agent swarm against mocked devnet responses.

Use when Docker/Go devnet isn't available. Tests that all agent reasoning
loops, tool calling, and swarm orchestration work end-to-end with real ollama.
"""

from __future__ import annotations

import sys
import time
import asyncio
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent.parent))

from src.tools.devnet_tools import DevnetInfo, DevnetConfig

MOCK_NODES = 7
MOCK_BASE_GRPC = 50051


def make_mock_devnet() -> DevnetInfo:
    cfg = DevnetConfig(
        validators=MOCK_NODES,
        base_grpc_port=MOCK_BASE_GRPC,
        stress_accounts=3,
    )
    info = DevnetInfo(config=cfg, data_dir="/tmp/mock-devnet")
    info.nodes = [
        {"index": i, "grpc_port": MOCK_BASE_GRPC + i,
         "grpc_addr": f"127.0.0.1:{MOCK_BASE_GRPC + i}",
         "p2p_port": 17777 + i}
        for i in range(MOCK_NODES)
    ]
    return info


_base_height = 42
_base_nonce = 3
_mempool_base = 5


def mock_get_status(grpc_addr="127.0.0.1:50051"):
    global _base_height, _mempool_base
    import src.tools.node_tools as nt
    _base_height += 1
    _mempool_base = max(0, _mempool_base - 1 + (_base_height % 3))
    root = "abcdef" + hex(_base_height)[2:].zfill(58)
    phases = ["propose", "prevote", "precommit"]
    return nt.NodeStatus(
        height=_base_height,
        state_root=root,
        peer_count=6,
        mempool_len=_mempool_base,
        validator_count=MOCK_NODES,
        consensus_round=0,
        consensus_phase=phases[_base_height % 3],
        grpc_only=False,
    )


def mock_get_account(pubkey_hex, grpc_addr="127.0.0.1:50051"):
    global _base_nonce
    import src.tools.node_tools as nt
    _base_nonce += 1
    return nt.AccountInfo(pubkey=pubkey_hex, balance="10000", nonce=_base_nonce)


def mock_submit_tx(tx_hex, grpc_addr="127.0.0.1:50051"):
    # For adversarial attacks, reject txs that look invalid
    if "fff" in tx_hex[:64] or len(tx_hex) < 100:
        return False, "ERR_009: canonical encoding distortion"
    if "0000000000000000" in tx_hex[-128:]:
        return False, "ERR_012: invalid signature"
    return True, ""


def patch_node_tools():
    import src.tools.node_tools as nt

    nt.get_status = mock_get_status
    nt.get_account = mock_get_account
    nt.submit_tx = mock_submit_tx
    nt.get_all_statuses = lambda info: [
        {"node": i, "height": _base_height + i % 2, "state_root": "abcdef" + "0" * 58,
         "peers": 6, "mempool": _mempool_base, "phase": "propose"}
        for i in range(MOCK_NODES)
    ]
    nt.wait_for_height = lambda grpc_addr, target, timeout=60.0: mock_get_status(grpc_addr)


def patch_devnet_tools():
    import src.tools.devnet_tools as dt

    dt.find_devnet_binary = lambda: "frg-devnet"
    dt.generate_devnet = lambda config=None: make_mock_devnet()
    dt.deploy_devnet = lambda info: True
    dt.teardown_devnet = lambda info: True
    dt.restart_node = lambda info, node_index: True
    dt.stop_node = lambda info, node_index: True
    dt.start_node = lambda info, node_index: True
    dt.load_stress_accounts = lambda info: [
        {"seed": "aa" * 32, "pubkey": "bb" * 32},
        {"seed": "cc" * 32, "pubkey": "dd" * 32},
        {"seed": "ee" * 32, "pubkey": "ff" * 32},
    ]
    dt.get_node_logs = lambda node_index, tail=50: (
        "[INFO] block produced height=42\n"
        "[INFO] consensus round complete\n"
        "[INFO] peer connected"
    )


def patch_contract_tools():
    import src.tools.contract_tools as ct

    ct.WORKLOADS_DIR = Path(__file__).parent.parent / "workloads"
    ct.list_workloads = lambda: [
        {"name": name, "size_bytes": 256, "deploy_gas": 356}
        for name in ct.WORKLOAD_NAMES
    ]


async def run_simulation(model: str = "qwen3:1.7b", max_iterations: int = 10):
    from src.swarm import Swarm, SwarmConfig

    patch_node_tools()
    patch_devnet_tools()
    patch_contract_tools()

    info = make_mock_devnet()
    print(f"Mock devnet: {len(info.nodes)} nodes, 3 stress accounts\n")

    config = SwarmConfig(
        model=model,
        host=None,
        max_iterations=max_iterations,
        cooldown_between_phases=0.5,
    )
    swarm = Swarm(info, config)

    schedule = [
        ("monitor",     0,   "Watch baseline state. All nodes producing blocks? Consensus healthy?"),
        ("traffic",     3,   "Send batch_transfer(count=10, amount=1). Verify processed."),
        ("contract",    5,   "List workloads, deploy 'trivial', call it. Deploy 'arithmetic'. Check consensus."),
        ("adversarial", 18,  "Run attacks: invalid_signature, wrong_signer, double_spend, invalid_tx_type. Check health after each."),
        ("fault",       30,  "Kill node-3 for 5s, check recovery. Kill node-5 briefly after."),
        ("analyst",     40,  "Take final snapshot. Check consensus. Get report. Compile findings."),
    ]

    print("Running collaborative scenario...\n")
    await swarm.run_scenario(duration=50, schedule=schedule)

    report = swarm.collect_and_report()
    print(f"\n{'='*60}")
    print(report)
    print(f"{'='*60}")

    return swarm, report


if __name__ == "__main__":
    model = sys.argv[1] if len(sys.argv) > 1 else "qwen3:1.7b"
    iters = int(sys.argv[2]) if len(sys.argv) > 2 else 8
    print(f"FRG Agent Swarm — Collaborative Scenario (Mock Mode)")
    print(f"Model: {model}, Max iterations: {iters}\n")
    asyncio.run(run_simulation(model=model, max_iterations=iters))
