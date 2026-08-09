"""
Contract Tester Agent: deploys and calls smart contracts on the devnet.

Uses pre-built Wasm workloads (trivial, arithmetic, hashing, memory,
state_read, state_write, heavy) to test the contract subsystem end-to-end.
"""

from __future__ import annotations

import json
import os
from typing import Optional

from .base import Agent, Tool


CONTRACT_DESCRIPTIONS = {
    "trivial": "No-op contract. Exports init and call, both empty. Minimum viable contract.",
    "arithmetic": "100,000 iterations of complex arithmetic. Heavy CPU burner, tests fuel metering.",
    "hashing": "10,000 log calls. Tests Wasm-to-host boundary crossing overhead.",
    "memory": "Writes all 64KB of linear memory 20 times. Tests memory bandwidth.",
    "state_read": "100 state_get calls. Tests contract state read I/O performance.",
    "state_write": "100 state_set calls. Tests contract state write I/O performance.",
    "heavy": "Combination: 10K arithmetic + 100 state writes + 100 state reads + 1K logs. Max stress.",
}


class ContractTester(Agent):
    def __init__(self, devnet_info, **kwargs):
        super().__init__(name="ContractTester", **kwargs)
        self.devnet_info = devnet_info
        self._accounts: list[dict] = []
        self._account_index: int = 0
        self._deployed: dict[str, str] = {}  # workload_name -> contract_addr_hex
        self._load_accounts()
        self._setup_tools()

    def _load_accounts(self) -> None:
        path = self.devnet_info.stress_accounts_file
        if path and os.path.isfile(path):
            with open(path) as f:
                self._accounts = json.load(f)

    def _get_account(self) -> dict:
        if not self._accounts:
            return {"pubkey": "00" * 32, "seed": "00" * 32}
        return self._accounts[self._account_index % len(self._accounts)]

    def _setup_tools(self) -> None:
        from ..tools.contract_tools import (
            load_wasm, contract_addr, make_call_data, list_workloads,
        )
        from ..tools.transaction_tools import (
            serialize_contract_deploy, serialize_contract_call,
        )
        from ..tools.node_tools import submit_tx, get_account

        info = self.devnet_info
        tester = self

        def deploy_contract(workload_name: str, value: int = 0) -> dict:
            wasm = load_wasm(workload_name)
            acct = tester._get_account()
            sender_pubkey = bytes.fromhex(acct["pubkey"])
            sender_seed = bytes.fromhex(acct["seed"])
            nonce = 1

            try:
                acc = get_account(acct["pubkey"], info.nodes[0]["grpc_addr"])
                nonce = acc.nonce + 1
            except Exception:
                pass

            addr = contract_addr(sender_pubkey, nonce)
            addr_hex = addr.hex()

            tx_bytes = serialize_contract_deploy(
                sender=f"contract-deployer",
                nonce=nonce,
                sender_pubkey=sender_pubkey,
                sender_seed=sender_seed,
                wasm_bytes=wasm,
                value=value,
            )

            ok, err = submit_tx(tx_bytes.hex(), info.nodes[0]["grpc_addr"])

            if ok:
                tester._deployed[workload_name] = addr_hex
                tester._account_index += 1

            return {
                "ok": ok,
                "error": err,
                "workload": workload_name,
                "contract_addr": addr_hex,
                "wasm_size": len(wasm),
            }

        def call_contract(workload_name: str, func_name: str = "call", value: int = 0) -> dict:
            addr_hex = tester._deployed.get(workload_name)
            if not addr_hex:
                return {"error": f"No deployed contract for '{workload_name}'. Deploy first."}

            contract_bytes = bytes.fromhex(addr_hex)
            acct = tester._get_account()
            sender_pubkey = bytes.fromhex(acct["pubkey"])
            sender_seed = bytes.fromhex(acct["seed"])
            nonce = 1

            try:
                acc = get_account(acct["pubkey"], info.nodes[0]["grpc_addr"])
                nonce = acc.nonce + 1
            except Exception:
                pass

            cd = make_call_data(func_name)
            tx_bytes = serialize_contract_call(
                sender=f"contract-caller",
                nonce=nonce,
                sender_pubkey=sender_pubkey,
                sender_seed=sender_seed,
                contract_addr=contract_bytes,
                call_data=cd,
                value=value,
            )

            ok, err = submit_tx(tx_bytes.hex(), info.nodes[0]["grpc_addr"])
            return {
                "ok": ok,
                "error": err,
                "workload": workload_name,
                "contract_addr": addr_hex,
                "function": func_name,
            }

        def query_contract_balance(workload_name: str) -> dict:
            addr_hex = tester._deployed.get(workload_name)
            if not addr_hex:
                return {"error": f"No deployed contract for '{workload_name}'"}
            try:
                acc = get_account(addr_hex, info.nodes[0]["grpc_addr"])
                return {"contract": workload_name, "balance": acc.balance, "nonce": acc.nonce}
            except Exception as e:
                return {"error": str(e)}

        self.register_tool(Tool(
            name="list_available_workloads",
            description="List all Wasm contract workloads available for deployment with descriptions",
            parameters={},
            handler=lambda: list_workloads(),
        ))

        self.register_tool(Tool(
            name="deploy_contract",
            description="Deploy a Wasm contract workload to the devnet",
            parameters={
                "workload_name": {"type": "string", "description": f"One of: {', '.join(CONTRACT_DESCRIPTIONS.keys())}"},
                "value": {"type": "integer", "description": "Initial endowment (base units, 0 = no funding)"},
            },
            handler=deploy_contract,
        ))

        self.register_tool(Tool(
            name="call_contract",
            description="Call a deployed contract's 'call' function",
            parameters={
                "workload_name": {"type": "string", "description": "Name of the deployed workload"},
                "func_name": {"type": "string", "description": "Function name to call (default: 'call')"},
                "value": {"type": "integer", "description": "Value to send with the call"},
            },
            handler=call_contract,
        ))

        self.register_tool(Tool(
            name="query_contract_balance",
            description="Check the balance of a deployed contract account",
            parameters={
                "workload_name": {"type": "string", "description": "Name of the deployed workload"},
            },
            handler=query_contract_balance,
        ))

        self.register_tool(Tool(
            name="poll_node_status",
            description="Check node status to see if contract txs were processed",
            parameters={
                "node_index": {"type": "integer", "description": "Node index (0-based, default 0)"},
            },
            handler=lambda node_index=0: self._node_status(node_index),
        ))

    def _node_status(self, node_index: int) -> dict:
        from ..tools.node_tools import get_status
        try:
            addr = self.devnet_info.nodes[node_index]["grpc_addr"]
            s = get_status(addr)
            return {
                "height": s.height,
                "mempool": s.mempool_len,
                "state_root": s.state_root[:16],
                "consensus_phase": s.consensus_phase,
            }
        except Exception as e:
            return {"error": str(e)}

    def system_prompt(self) -> str:
        workloads_desc = "\n".join(
            f"  - {name}: {desc}" for name, desc in CONTRACT_DESCRIPTIONS.items()
        )
        return f"""You are a Contract Tester agent for a FRG blockchain devnet.

SHARED WORLD: Other agents are active alongside you:
  - NetworkMonitor: watching consensus (deploy/calls affect the state root)
  - TrafficGenerator: sending transfers (your contract calls run alongside normal traffic)
  - Adversarial: launching attacks (make sure your contracts still work under attack)
  - FaultInjector: killing nodes (verify contract txs get included despite node failures)

Your job: systematically test the smart contract subsystem by deploying and
calling Wasm contracts while other agents exercise the chain.

Available workload contracts:
{workloads_desc}

Testing strategy:
1. Start by deploying a 'trivial' contract (minimum viable, verifies deploy works).
2. Call the trivial contract to verify calls work.
3. Deploy an 'arithmetic' contract alongside traffic.
4. After each deploy/call, poll node status to ensure consensus holds.

This is a living chain — TrafficGenerator is also sending transfers. Your
contract deploys and calls interleave with normal traffic, just like a real
blockchain. When you've tested contracts and confirmed consensus, signal DONE.
"""  # noqa: E501
