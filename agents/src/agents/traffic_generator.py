"""
Traffic Generator Agent: creates and submits transactions to the devnet.

Generates various traffic patterns: steady load, bursts, targeted transfers,
mass spam. Uses pre-funded stress accounts from the devnet.
"""

from __future__ import annotations

import json
import os
import time
from typing import Optional

from .base import Agent, Tool


class TrafficGenerator(Agent):
    def __init__(self, devnet_info, **kwargs):
        super().__init__(name="TrafficGenerator", **kwargs)
        self.devnet_info = devnet_info
        self._accounts: list[dict] = []
        self._account_index: int = 0
        self._load_accounts()
        self._setup_tools()

    def _load_accounts(self) -> None:
        path = self.devnet_info.stress_accounts_file
        if path and os.path.isfile(path):
            with open(path) as f:
                self._accounts = json.load(f)

    def _setup_tools(self) -> None:
        from ..tools.transaction_tools import serialize_transfer
        from ..tools.node_tools import submit_tx

        info = self.devnet_info

        def send_transfer(
            from_index: int,
            to_index: int,
            amount: int,
            nonce: int,
        ) -> dict:
            if not self._accounts:
                return {"error": "No stress accounts loaded"}

            sender = self._accounts[from_index % len(self._accounts)]
            receiver = self._accounts[to_index % len(self._accounts)]

            sender_pubkey = bytes.fromhex(sender["pubkey"])
            receiver_pubkey = bytes.fromhex(receiver["pubkey"])
            sender_seed = bytes.fromhex(sender["seed"])
            receiver_seed = bytes.fromhex(receiver["seed"])

            tx_bytes = serialize_transfer(
                sender=f"stress-{from_index}",
                receiver=f"stress-{to_index}",
                value=amount,
                nonce=nonce,
                sender_pubkey=sender_pubkey,
                receiver_pubkey=receiver_pubkey,
                sender_seed=sender_seed,
                receiver_seed=receiver_seed,
            )
            tx_hex = tx_bytes.hex()
            addr = info.nodes[0]["grpc_addr"]
            ok, err = submit_tx(tx_hex, addr)
            return {"ok": ok, "error": err, "from": from_index, "to": to_index, "amount": amount}

        def get_account_nonce(account_index: int) -> dict:
            from ..tools.node_tools import get_account

            if not self._accounts:
                return {"error": "No stress accounts loaded"}
            acct = self._accounts[account_index % len(self._accounts)]
            try:
                acc = get_account(acct["pubkey"], info.nodes[0]["grpc_addr"])
                return {"index": account_index, "nonce": acc.nonce, "balance": acc.balance}
            except Exception as e:
                return {"index": account_index, "error": str(e)}

        self.register_tool(Tool(
            name="send_transfer",
            description="Send a transfer transaction between stress accounts",
            parameters={
                "from_index": {"type": "integer", "description": "Index of sender stress account (0-based)"},
                "to_index": {"type": "integer", "description": "Index of receiver stress account (0-based)"},
                "amount": {"type": "integer", "description": "Transfer amount in base units"},
                "nonce": {"type": "integer", "description": "Transaction nonce (must be account_nonce + 1)"},
            },
            handler=send_transfer,
        ))

        self.register_tool(Tool(
            name="get_account_nonce",
            description="Get current nonce and balance for a stress account",
            parameters={
                "account_index": {"type": "integer", "description": "Index of stress account (0-based)"},
            },
            handler=get_account_nonce,
        ))

        self.register_tool(Tool(
            name="batch_transfer",
            description="Send many transfers in quick succession to generate load",
            parameters={
                "count": {"type": "integer", "description": "Number of transfers to send"},
                "amount": {"type": "integer", "description": "Amount per transfer"},
            },
            handler=lambda count, amount: self._batch_send(count, amount),
        ))

        self.register_tool(Tool(
            name="poll_node_status",
            description="Check node status to see if transactions are being processed",
            parameters={
                "node_index": {"type": "integer", "description": "Node to query (0-based, default 0)"},
            },
            handler=lambda node_index=0: self._node_status(node_index),
        ))

    def _node_status(self, node_index: int) -> dict:
        from ..tools.node_tools import get_status
        try:
            addr = self.devnet_info.nodes[node_index]["grpc_addr"]
            s = get_status(addr)
            return {"height": s.height, "mempool": s.mempool_len, "state_root": s.state_root[:16]}
        except Exception as e:
            return {"error": str(e)}

    def _batch_send(self, count: int, amount: int) -> dict:
        if not self._accounts:
            return {"error": "No stress accounts loaded"}

        from ..tools.transaction_tools import serialize_transfer
        from ..tools.node_tools import submit_tx

        info = self.devnet_info
        addr = info.nodes[0]["grpc_addr"]
        submitted = 0
        failed = 0

        for i in range(min(count, 200)):
            try:
                si = (self._account_index + i) % len(self._accounts)
                ri = (si + 1) % len(self._accounts)

                sender = self._accounts[si]
                receiver = self._accounts[ri]
                tx_bytes = serialize_transfer(
                    sender=f"stress-{si}",
                    receiver=f"stress-{ri}",
                    value=amount,
                    nonce=i + 1,
                    sender_pubkey=bytes.fromhex(sender["pubkey"]),
                    receiver_pubkey=bytes.fromhex(receiver["pubkey"]),
                    sender_seed=bytes.fromhex(sender["seed"]),
                    receiver_seed=bytes.fromhex(receiver["seed"]),
                )
                ok, _ = submit_tx(tx_bytes.hex(), addr)
                if ok:
                    submitted += 1
                else:
                    failed += 1
            except Exception:
                failed += 1

        self._account_index += count
        return {"submitted": submitted, "failed": failed}

    def system_prompt(self) -> str:
        accts = len(self._accounts)
        return f"""You are a Traffic Generator agent for a FRG blockchain devnet.

You have access to {accts} pre-funded stress accounts.
SHARED WORLD: Other agents are also running alongside you:
  - NetworkMonitor: watching consensus health
  - ContractTester: deploying and calling contracts (you may see their contract addresses)
  - Adversarial: launching attacks (make sure your transfers still work)
  - FaultInjector: killing nodes (verify your tx batches get through despite failures)

Your job: generate realistic transaction traffic and observe how the network handles it.

Available patterns:
- Steady load: send transfers at a constant rate
- Burst: send many transfers at once, then pause
- Stress test: push the network to see throughput limits

Use batch_transfer or send_transfer to submit txs, then poll_node_status to
see if they're being included in blocks. Monitor mempool size and block height
progress. When you've run sufficient traffic and confirmed it's being processed, signal DONE.

If you see the Adversarial agent attacking, check that your transfers still get through.
If you see the FaultInjector kill a node, send a batch and verify it still works. This is a living chain — keep the traffic flowing.
"""  # noqa: E501
