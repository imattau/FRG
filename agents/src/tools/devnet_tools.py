"""
Devnet lifecycle management: generation, deployment, teardown.

Uses the frg-devnet binary (Go) to generate devnet configs and
docker compose to manage the cluster.
"""

from __future__ import annotations

import json
import os
import subprocess
import tempfile
from dataclasses import dataclass, field
from pathlib import Path
from typing import Optional


@dataclass
class DevnetConfig:
    validators: int = 7
    chain_id: str = "frg-devnet-1"
    bond: str = "1000"
    balance: str = "10000"
    base_p2p_port: int = 17777
    base_grpc_port: int = 50051
    stress_accounts: int = 50
    faucet_balance: str = "1000000"
    data_dir: Optional[str] = None


@dataclass
class DevnetInfo:
    config: DevnetConfig
    data_dir: str
    nodes: list[dict] = field(default_factory=list)
    faucet_pubkey: str = ""
    stress_accounts_file: str = ""


def find_devnet_binary() -> str:
    for path in [
        "./frg-devnet",
        "../frg-devnet",
        str(Path(__file__).parent.parent.parent.parent / "frg-devnet"),
    ]:
        if os.path.isfile(path) and os.access(path, os.X_OK):
            return os.path.abspath(path)
    return "frg-devnet"


def generate_devnet(config: Optional[DevnetConfig] = None) -> DevnetInfo:
    if config is None:
        config = DevnetConfig()

    data_dir = config.data_dir or tempfile.mkdtemp(prefix="frg-devnet-")
    binary = find_devnet_binary()

    cmd = [
        binary,
        "--validators", str(config.validators),
        "--chain-id", config.chain_id,
        "--bond", config.bond,
        "--balance", config.balance,
        "--base-p2p-port", str(config.base_p2p_port),
        "--base-grpc-port", str(config.base_grpc_port),
        "--stress-accounts", str(config.stress_accounts),
        "--faucet-balance", config.faucet_balance,
        "--output-dir", data_dir,
    ]

    result = subprocess.run(cmd, capture_output=True, text=True)
    if result.returncode != 0:
        raise RuntimeError(f"frg-devnet failed: {result.stderr}")

    info = DevnetInfo(config=config, data_dir=data_dir)
    info.nodes = [
        {
            "index": i,
            "p2p_port": config.base_p2p_port + i,
            "grpc_port": config.base_grpc_port + i,
            "grpc_addr": f"127.0.0.1:{config.base_grpc_port + i}",
        }
        for i in range(config.validators)
    ]

    for line in result.stdout.splitlines():
        if "faucet" in line and "balance=" in line:
            parts = line.strip().split()
            if len(parts) >= 2:
                info.faucet_pubkey = parts[1]

    stress_file = os.path.join(data_dir, "stress_accounts.json")
    if os.path.isfile(stress_file):
        info.stress_accounts_file = stress_file

    return info


def deploy_devnet(info: DevnetInfo) -> bool:
    compose_file = os.path.join(info.data_dir, "docker-compose.yml")
    if not os.path.isfile(compose_file):
        raise FileNotFoundError(f"No docker-compose.yml in {info.data_dir}")
    result = subprocess.run(
        ["docker", "compose", "-f", compose_file, "up", "-d"],
        capture_output=True, text=True,
    )
    return result.returncode == 0


def teardown_devnet(info: DevnetInfo) -> bool:
    compose_file = os.path.join(info.data_dir, "docker-compose.yml")
    if not os.path.isfile(compose_file):
        return False
    result = subprocess.run(
        ["docker", "compose", "-f", compose_file, "down", "-v"],
        capture_output=True, text=True,
    )
    return True


def restart_node(info: DevnetInfo, node_index: int) -> bool:
    name = f"frg-node-{node_index}"
    result = subprocess.run(
        ["docker", "restart", name],
        capture_output=True, text=True,
    )
    return result.returncode == 0


def stop_node(info: DevnetInfo, node_index: int) -> bool:
    name = f"frg-node-{node_index}"
    result = subprocess.run(
        ["docker", "stop", name],
        capture_output=True, text=True,
    )
    return result.returncode == 0


def start_node(info: DevnetInfo, node_index: int) -> bool:
    name = f"frg-node-{node_index}"
    result = subprocess.run(
        ["docker", "start", name],
        capture_output=True, text=True,
    )
    return result.returncode == 0


def load_stress_accounts(info: DevnetInfo) -> list[dict]:
    if not info.stress_accounts_file:
        return []
    with open(info.stress_accounts_file) as f:
        return json.load(f)


def get_node_logs(node_index: int, tail: int = 50) -> str:
    name = f"frg-node-{node_index}"
    result = subprocess.run(
        ["docker", "logs", "--tail", str(tail), name],
        capture_output=True, text=True,
    )
    return result.stdout
