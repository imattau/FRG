"""
Live test: runs the collaborative agent swarm against a real devnet.

All agents run concurrently, sharing a SharedWorld event bus.
They react to each other: Monitor watches as Traffic flows, Contracts deploy,
Attacks hit, and Faults occur — all at the same time, like a real chain.
"""

import sys
import os
import asyncio
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent))
sys.path.insert(0, str(Path(__file__).parent.parent))

from src.swarm import SwarmConfig, run_default_swarm
from src.tools.devnet_tools import DevnetInfo, DevnetConfig


def make_live_devnet() -> DevnetInfo:
    cfg = DevnetConfig(validators=3, base_grpc_port=50051, stress_accounts=5)
    info = DevnetInfo(config=cfg, data_dir="/tmp/frg-devnet-test")
    info.nodes = [
        {"index": i, "grpc_port": 50051 + i,
         "grpc_addr": f"127.0.0.1:{50051 + i}",
         "p2p_port": 17777 + i}
        for i in range(3)
    ]
    stress_file = "/tmp/frg-devnet-test/stress_accounts.json"
    if os.path.isfile(stress_file):
        info.stress_accounts_file = stress_file
    return info


async def main():
    model = sys.argv[1] if len(sys.argv) > 1 else "qwen3:1.7b"
    iters = int(sys.argv[2]) if len(sys.argv) > 2 else 12

    info = make_live_devnet()
    print(f"Collaborative Swarm — {len(info.nodes)} nodes running\n")

    config = SwarmConfig(
        model=model,
        max_iterations=iters,
        cooldown_between_phases=1.0,
    )

    print("Timeline:")
    print("  t=0s   Monitor starts watching chain health")
    print("  t=3s   TrafficGenerator starts sending transfers")
    print("  t=5s   ContractTester starts deploying/calling contracts")
    print("  t=25s  Adversarial starts attacking (while traffic + contracts run)")
    print("  t=40s  FaultInjector kills nodes (chain under load)")
    print("  t=55s  Analyst takes final measurements\n")

    swarm, report = await run_default_swarm(info, config)

    print(f"\n{'='*60}")
    print(report)
    print(f"{'='*60}")

    with open("/tmp/frg-swarm-report.txt", "w") as f:
        f.write(report)
    print("\nReport saved to /tmp/frg-swarm-report.txt")

    return swarm, report


if __name__ == "__main__":
    asyncio.run(main())
