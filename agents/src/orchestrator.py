"""
Orchestrator: main entry point for the agent swarm.

Handles CLI argument parsing, devnet setup/teardown, and running the swarm.
"""

from __future__ import annotations

import argparse
import asyncio
import os
import sys
import time

from .tools.devnet_tools import (
    DevnetConfig,
    generate_devnet,
    deploy_devnet,
    teardown_devnet,
)
from .swarm import SwarmConfig, run_default_swarm


def parse_args() -> argparse.Namespace:
    p = argparse.ArgumentParser(
        description="FRG Agent Swarm — LLM-powered devnet testing"
    )
    p.add_argument(
        "--model", default="llama3.2",
        help="Ollama model name (default: llama3.2)",
    )
    p.add_argument(
        "--ollama-host",
        help="Ollama host URL (default: http://localhost:11434)",
    )
    p.add_argument(
        "--validators", type=int, default=7,
        help="Number of validator nodes (default: 7)",
    )
    p.add_argument(
        "--stress-accounts", type=int, default=50,
        help="Number of pre-funded stress accounts (default: 50)",
    )
    p.add_argument(
        "--base-grpc-port", type=int, default=50051,
        help="Starting gRPC port (default: 50051)",
    )
    p.add_argument(
        "--max-iterations", type=int, default=30,
        help="Max agent loop iterations (default: 30)",
    )
    p.add_argument(
        "--data-dir",
        help="Existing devnet data directory (skips generation if provided)",
    )
    p.add_argument(
        "--keep-devnet", action="store_true",
        help="Don't tear down devnet after test run",
    )
    p.add_argument(
        "--no-deploy", action="store_true",
        help="Skip docker compose up (assume devnet already running)",
    )
    p.add_argument(
        "--frg-devnet-bin",
        help="Path to frg-devnet binary",
    )
    p.add_argument(
        "--phases",
        help="Comma-separated list of phases to run (default: all). Options: monitor,contract,traffic,fault,parallel,analysis",
    )
    return p.parse_args()


async def main_async() -> int:
    args = parse_args()

    if args.frg_devnet_bin:
        os.environ["FRG_DEVNET_BIN_OVERRIDE"] = args.frg_devnet_bin

    if args.data_dir and os.path.isdir(args.data_dir):
        print(f"Using existing devnet at {args.data_dir}")
        info = None
        from .tools.devnet_tools import DevnetInfo, DevnetConfig
        cfg = DevnetConfig(
            validators=args.validators,
            base_grpc_port=args.base_grpc_port,
            stress_accounts=args.stress_accounts,
        )
        info = DevnetInfo(config=cfg, data_dir=args.data_dir)
        info.nodes = [
            {
                "index": i,
                "p2p_port": 17777 + i,
                "grpc_port": args.base_grpc_port + i,
                "grpc_addr": f"127.0.0.1:{args.base_grpc_port + i}",
            }
            for i in range(args.validators)
        ]
        stress_file = os.path.join(args.data_dir, "stress_accounts.json")
        if os.path.isfile(stress_file):
            info.stress_accounts_file = stress_file
    else:
        print("Generating devnet configuration...")
        cfg = DevnetConfig(
            validators=args.validators,
            stress_accounts=args.stress_accounts,
            base_grpc_port=args.base_grpc_port,
        )
        if args.data_dir:
            cfg.data_dir = args.data_dir

        try:
            info = generate_devnet(cfg)
            print(f"Devnet generated: {len(info.nodes)} nodes in {info.data_dir}")
        except RuntimeError as e:
            print(f"Error generating devnet: {e}", file=sys.stderr)
            print("Build frg-devnet first: go build ./cmd/frg-devnet", file=sys.stderr)
            return 1

    if not args.no_deploy:
        print("Starting devnet cluster...")
        try:
            ok = deploy_devnet(info)
            if not ok:
                print("Warning: docker compose up may have issues, continuing...")
        except FileNotFoundError:
            print("No docker-compose.yml found. Run frg-devnet with --docker flag.", file=sys.stderr)
            if not args.keep_devnet:
                return 1

    print("Waiting for cluster to stabilize (15s)...")
    time.sleep(15)

    swarm_config = SwarmConfig(
        model=args.model,
        host=args.ollama_host,
        max_iterations=args.max_iterations,
    )

    try:
        swarm, report = await run_default_swarm(info, swarm_config)
        print("\n" + "=" * 60)
        print(report)
        print("=" * 60)

        report_path = os.path.join(info.data_dir, "test_report.txt")
        with open(report_path, "w") as f:
            f.write(report)
        print(f"\nReport saved to {report_path}")

    finally:
        if not args.keep_devnet:
            print("\nTearing down devnet...")
            teardown_devnet(info)

    return 0


def main() -> None:
    sys.exit(asyncio.run(main_async()))


if __name__ == "__main__":
    main()
