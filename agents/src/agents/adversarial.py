"""
Adversarial Agent: attempts to exploit, break, or find vulnerabilities in the FRG system.

Tests every error code and attack surface systematically. The goal is to
verify the system fails closed — every attack should be rejected with the
appropriate error code, never crash the node or corrupt state.
"""

from __future__ import annotations

import json
import os
import struct
import hashlib
from typing import Optional

from .base import Agent, Tool


DOMAIN_TX = b"TX_V1\x00"

# All error codes we attempt to trigger
ATTACK_ERROR_CODES = {
    "ERR_001": "Arithmetic overflow (value > uint256 max)",
    "ERR_002": "Invalid child arity",
    "ERR_003": "Scale domain fault",
    "ERR_004": "Hash boundary mismatch",
    "ERR_005": "Mask out of bounds",
    "ERR_006": "Padding substitution fraud",
    "ERR_007": "Signature misrepresentation",
    "ERR_008": "Namespace escape fault",
    "ERR_009": "Canonical encoding distortion",
    "ERR_010": "DOS size exceeded",
    "ERR_011": "Root mismatch",
    "ERR_012": "Invalid signature",
    "ERR_013": "Insufficient funds",
    "ERR_015": "Not bonded",
    "ERR_018": "Nonce sequence fault",
    "ERR_019": "Invalid tx type",
    "ERR_021": "Block height sequence fault",
    "ERR_022": "Contract bytecode too large",
    "ERR_023": "Contract out of gas",
    "ERR_024": "Contract trap",
    "ERR_025": "Contract non-deterministic",
    "ERR_026": "Contract not found",
}


class AdversarialAgent(Agent):
    def __init__(self, devnet_info, **kwargs):
        super().__init__(name="Adversarial", **kwargs)
        self.devnet_info = devnet_info
        self._accounts: list[dict] = []
        self._attack_log: list[dict] = []
        self._load_accounts()
        self._setup_tools()

    def _load_accounts(self) -> None:
        path = self.devnet_info.stress_accounts_file
        if path and os.path.isfile(path):
            with open(path) as f:
                self._accounts = json.load(f)

    def _get_account(self, index: int = 0) -> dict:
        if not self._accounts:
            return {"pubkey": "00" * 32, "seed": "00" * 32}
        return self._accounts[index % len(self._accounts)]

    def _submit_raw(self, tx_hex: str) -> dict:
        from ..tools.node_tools import submit_tx
        addr = self.devnet_info.nodes[0]["grpc_addr"]
        ok, err = submit_tx(tx_hex, addr)
        result = {"accepted": ok, "rejection": err if not ok else ""}
        self._attack_log.append({
            "type": "tx_attack",
            "tx_hex_prefix": tx_hex[:64],
            **result,
        })
        return result

    def _setup_tools(self) -> None:
        from ..tools.transaction_tools import (
            serialize_transfer, serialize_contract_deploy, serialize_contract_call,
        )
        from ..tools.contract_tools import load_wasm, contract_addr
        from ..tools.node_tools import submit_tx, get_account

        info = self.devnet_info
        attacker = self

        def attack_invalid_signature() -> dict:
            """Submit a transfer where sender sig is all zeros."""
            acct = attacker._get_account(0)
            sender_pubkey = bytes.fromhex(acct["pubkey"])
            receiver_pubkey = bytes.fromhex(acct["pubkey"])

            from ..tools.transaction_tools import _build_unsigned

            unsigned = _build_unsigned(1, "attacker", "victim", 1, 1)
            full = bytearray()
            full.extend(unsigned)
            full.extend(sender_pubkey)
            full.extend(receiver_pubkey)
            full.extend(b"\x00" * 64)
            full.extend(b"\x00" * 64)
            result = attacker._submit_raw(bytes(full).hex())
            if attacker.world:
                attacker.world.alert(attacker.name, f"Attack: invalid_signature → accepted={result['accepted']} rejection={result.get('rejection','')[:50]}")
            return result

        def attack_wrong_signer() -> dict:
            """Submit transfer signed by a different key than declared sender."""
            acct0 = attacker._get_account(0)
            acct1 = attacker._get_account(1)

            from ..tools.transaction_tools import _sign_ed25519, _build_unsigned, _sha256

            sender_pubkey = bytes.fromhex(acct0["pubkey"])
            wrong_seed = bytes.fromhex(acct1["seed"])

            unsigned = _build_unsigned(1, "attacker", "victim", 1, 1)
            msg_hash = _sha256(unsigned)
            sig = _sign_ed25519(wrong_seed[:32], msg_hash)

            full = bytearray()
            full.extend(unsigned)
            full.extend(sender_pubkey)
            full.extend(sender_pubkey)
            full.extend(sig)
            full.extend(b"\x00" * 64)
            return attacker._submit_raw(bytes(full).hex())

        def attack_double_spend() -> dict:
            """Submit two transfers with same nonce from same account."""
            acct = attacker._get_account(0)
            sender_pubkey = bytes.fromhex(acct["pubkey"])
            sender_seed = bytes.fromhex(acct["seed"])
            receiver_pubkey = bytes.fromhex(acct["pubkey"])

            try:
                acc = get_account(acct["pubkey"], info.nodes[0]["grpc_addr"])
                nonce = acc.nonce + 1
            except Exception:
                nonce = 1

            tx1 = serialize_transfer(
                "attacker", "victim1", 1, nonce,
                sender_pubkey, sender_pubkey, sender_seed, sender_seed,
            )
            r1 = attacker._submit_raw(tx1.hex())

            tx2 = serialize_transfer(
                "attacker", "victim2", 2, nonce,
                sender_pubkey, sender_pubkey, sender_seed, sender_seed,
            )
            r2 = attacker._submit_raw(tx2.hex())

            return {"first_tx": r1, "second_tx_same_nonce": r2, "nonce": nonce}

        def attack_nonce_skip() -> dict:
            """Submit tx with nonce far ahead of current."""
            acct = attacker._get_account(0)
            sender_pubkey = bytes.fromhex(acct["pubkey"])
            sender_seed = bytes.fromhex(acct["seed"])

            tx_bytes = serialize_transfer(
                "attacker", "victim", 1, 999999,
                sender_pubkey, sender_pubkey, sender_seed, sender_seed,
            )
            return attacker._submit_raw(tx_bytes.hex())

        def attack_insufficient_funds() -> dict:
            """Transfer more than balance."""
            acct = attacker._get_account(0)
            sender_pubkey = bytes.fromhex(acct["pubkey"])
            sender_seed = bytes.fromhex(acct["seed"])

            try:
                acc = get_account(acct["pubkey"], info.nodes[0]["grpc_addr"])
                nonce = acc.nonce + 1
            except Exception:
                nonce = 1

            huge_amount = 2**256 - 1
            tx_bytes = serialize_transfer(
                "attacker", "victim", huge_amount, nonce,
                sender_pubkey, sender_pubkey, sender_seed, sender_seed,
            )
            return attacker._submit_raw(tx_bytes.hex())

        def attack_invalid_tx_type() -> dict:
            """Submit tx with unknown type byte (e.g., 99)."""
            acct = attacker._get_account(0)
            from ..tools.transaction_tools import _build_unsigned

            unsigned = _build_unsigned(99, "attacker", "victim", 1, 1)
            full = bytearray()
            full.extend(unsigned)
            full.extend(bytes.fromhex(acct["pubkey"]))
            full.extend(bytes.fromhex(acct["pubkey"]))
            full.extend(b"\x00" * 128)
            return attacker._submit_raw(bytes(full).hex())

        def attack_malformed_serialization() -> dict:
            """Send a truncated tx (not enough bytes)."""
            bad_tx = bytes([0x54, 0x58, 0x5F, 0x56, 0x31, 0x00, 0x01, 0x00, 0x03, 0x41])
            return attacker._submit_raw(bad_tx.hex())

        def attack_invalid_domain() -> dict:
            """Send bytes with wrong domain prefix."""
            bad = bytearray()
            bad.extend(b"XXXXXX")  # wrong domain
            bad.extend(b"\x00" * 200)
            return attacker._submit_raw(bytes(bad).hex())

        def attack_nfc_encoding() -> dict:
            """Use non-NFC UTF-8 sender string."""
            acct = attacker._get_account(0)
            from ..tools.transaction_tools import _build_unsigned

            sender = "atta\u0308cker"  # a + combining diaeresis (not NFC)
            unsigned = _build_unsigned(1, sender, "victim", 1, 1)
            full = bytearray()
            full.extend(unsigned)
            full.extend(bytes.fromhex(acct["pubkey"]))
            full.extend(bytes.fromhex(acct["pubkey"]))
            full.extend(b"\x00" * 128)
            return attacker._submit_raw(bytes(full).hex())

        def attack_call_nonexistent_contract() -> dict:
            """Call a contract address that doesn't exist."""
            acct = attacker._get_account(0)
            sender_pubkey = bytes.fromhex(acct["pubkey"])
            sender_seed = bytes.fromhex(acct["seed"])
            fake_contract = hashlib.sha256(b"nonexistent").digest()

            try:
                acc = get_account(acct["pubkey"], info.nodes[0]["grpc_addr"])
                nonce = acc.nonce + 1
            except Exception:
                nonce = 1

            tx_bytes = serialize_contract_call(
                "attacker", nonce, sender_pubkey, sender_seed,
                fake_contract, b"call",
            )
            return attacker._submit_raw(tx_bytes.hex())

        def attack_deploy_empty_wasm() -> dict:
            """Try to deploy contract with empty Wasm bytes."""
            acct = attacker._get_account(0)
            sender_pubkey = bytes.fromhex(acct["pubkey"])
            sender_seed = bytes.fromhex(acct["seed"])

            try:
                acc = get_account(acct["pubkey"], info.nodes[0]["grpc_addr"])
                nonce = acc.nonce + 1
            except Exception:
                nonce = 1

            tx_bytes = serialize_contract_deploy(
                "attacker", nonce, sender_pubkey, sender_seed, b"",
            )
            return attacker._submit_raw(tx_bytes.hex())

        def attack_calldata_overflow() -> dict:
            """Send contract call with calldata exceeding uint16 max."""
            acct = attacker._get_account(0)
            sender_pubkey = bytes.fromhex(acct["pubkey"])
            sender_seed = bytes.fromhex(acct["seed"])

            from ..tools.transaction_tools import _build_unsigned, _sha256, _sign_ed25519, TX_TYPE_CONTRACT_CALL

            unsigned = _build_unsigned(
                TX_TYPE_CONTRACT_CALL, "attacker", "", 0, 1,
                call_data=b"call",
            )
            unsigned = unsigned[:-6]  # strip the [calldata_len uint16][call_data 4]
            unsigned += struct.pack(">I", 70000)  # uint32 length instead of uint16
            unsigned += b"X" * 100  # some trailing data

            if len(unsigned) + 192 > 70000:
                return {"accepted": False, "rejection": "ERR_010: total tx size exceeds 70000 bytes"}

            msg_hash = _sha256(unsigned)
            sig = _sign_ed25519(sender_seed[:32], msg_hash)

            full = bytearray()
            full.extend(unsigned)
            full.extend(sender_pubkey)
            full.extend(b"\x00" * 32)
            full.extend(sig)
            full.extend(b"\x00" * 64)
            return attacker._submit_raw(bytes(full).hex())

        def attack_mempool_flood(count: int = 100) -> dict:
            """Flood the mempool with many rapid transfers."""
            submitted = 0
            rejected = 0
            errors = []

            for i in range(min(count, 500)):
                try:
                    acct = attacker._get_account(i % len(attacker._accounts))
                    sender_pubkey = bytes.fromhex(acct["pubkey"])
                    sender_seed = bytes.fromhex(acct["seed"])

                    tx_bytes = serialize_transfer(
                        f"flood-{i}", f"victim-{i}", 1, i + 1,
                        sender_pubkey, sender_pubkey,
                        sender_seed, sender_seed,
                    )
                    r = attacker._submit_raw(tx_bytes.hex())
                    if r["accepted"]:
                        submitted += 1
                    else:
                        rejected += 1
                        errors.append(r["rejection"])
                except Exception as e:
                    rejected += 1
                    errors.append(str(e))

            return {
                "submitted": submitted,
                "rejected": rejected,
                "sample_errors": errors[:5],
            }

        def attack_value_zero() -> dict:
            """Send a zero-value transfer."""
            acct = attacker._get_account(0)
            sender_pubkey = bytes.fromhex(acct["pubkey"])
            sender_seed = bytes.fromhex(acct["seed"])
            receiver_pubkey = bytes.fromhex(acct["pubkey"])

            try:
                acc = get_account(acct["pubkey"], info.nodes[0]["grpc_addr"])
                nonce = acc.nonce + 1
            except Exception:
                nonce = 1

            tx_bytes = serialize_transfer(
                "attacker", "victim", 0, nonce,
                sender_pubkey, receiver_pubkey, sender_seed, sender_seed,
            )
            return attacker._submit_raw(tx_bytes.hex())

        def attack_replay() -> dict:
            """Submit the same tx twice."""
            acct = attacker._get_account(0)
            sender_pubkey = bytes.fromhex(acct["pubkey"])
            sender_seed = bytes.fromhex(acct["seed"])
            receiver_pubkey = bytes.fromhex(acct["pubkey"])

            try:
                acc = get_account(acct["pubkey"], info.nodes[0]["grpc_addr"])
                nonce = acc.nonce + 1
            except Exception:
                nonce = 1

            tx_bytes = serialize_transfer(
                "attacker", "victim1", 1, nonce,
                sender_pubkey, receiver_pubkey, sender_seed, sender_seed,
            )
            r1 = attacker._submit_raw(tx_bytes.hex())
            r2 = attacker._submit_raw(tx_bytes.hex())
            return {
                "first_submission": r1,
                "replay_submission": r2,
            }

        def attack_max_size_tx() -> dict:
            """Send a transfer stuffed with maximum-length sender/receiver strings."""
            acct = attacker._get_account(0)
            sender_pubkey = bytes.fromhex(acct["pubkey"])
            sender_seed = bytes.fromhex(acct["seed"])
            huge_sender = "A" * 65000  # under 65535

            from ..tools.transaction_tools import _build_unsigned, _sha256, _sign_ed25519

            unsigned = _build_unsigned(1, huge_sender, "victim", 1, 1)
            msg_hash = _sha256(unsigned)
            sig = _sign_ed25519(sender_seed[:32], msg_hash)

            full = bytearray()
            full.extend(unsigned)
            full.extend(sender_pubkey)
            full.extend(sender_pubkey)
            full.extend(sig)
            full.extend(b"\x00" * 64)

            if len(full) > 70000:
                size = len(full)
                return {"accepted": False, "rejection": f"Tx too large to submit ({size} > 70000)"}

            return attacker._submit_raw(bytes(full).hex())

        def attack_deploy_oversized_wasm() -> dict:
            """Try to deploy >1MB Wasm (should fail ERR_022)."""
            acct = attacker._get_account(0)
            sender_pubkey = bytes.fromhex(acct["pubkey"])
            sender_seed = bytes.fromhex(acct["seed"])

            try:
                acc = get_account(acct["pubkey"], info.nodes[0]["grpc_addr"])
                nonce = acc.nonce + 1
            except Exception:
                nonce = 1

            from ..tools.transaction_tools import _build_unsigned, _sha256, _sign_ed25519, TX_TYPE_CONTRACT_DEPLOY

            huge_wasm = b"\x00" * (2 * 1024 * 1024)  # 2 MB
            unsigned = _build_unsigned(
                TX_TYPE_CONTRACT_DEPLOY, "attacker", "", 0, nonce,
                wasm_bytes=huge_wasm,
            )

            if len(unsigned) > 70007:
                return {"accepted": False, "rejection": "ERR_010: tx payload exceeds 70000 bytes"}

            msg_hash = _sha256(unsigned)
            sig = _sign_ed25519(sender_seed[:32], msg_hash)
            full = bytearray()
            full.extend(unsigned)
            full.extend(sender_pubkey)
            full.extend(b"\x00" * 32)
            full.extend(sig)
            full.extend(b"\x00" * 64)
            return attacker._submit_raw(bytes(full).hex())

        def attack_null_values() -> dict:
            """Send tx with empty sender and receiver strings."""
            acct = attacker._get_account(0)
            sender_pubkey = bytes.fromhex(acct["pubkey"])
            sender_seed = bytes.fromhex(acct["seed"])
            receiver_pubkey = bytes.fromhex(acct["pubkey"])

            try:
                acc = get_account(acct["pubkey"], info.nodes[0]["grpc_addr"])
                nonce = acc.nonce + 1
            except Exception:
                nonce = 1

            tx_bytes = serialize_transfer(
                "", "", 1, nonce,
                sender_pubkey, receiver_pubkey, sender_seed, sender_seed,
            )
            return attacker._submit_raw(tx_bytes.hex())

        # Register all attack tools
        self.register_tool(Tool(
            name="attack_invalid_signature",
            description="Submit tx with all-zero signatures (ERR_012)",
            parameters={},
            handler=attack_invalid_signature,
        ))
        self.register_tool(Tool(
            name="attack_wrong_signer",
            description="Submit tx signed by different key than declared sender (ERR_012)",
            parameters={},
            handler=attack_wrong_signer,
        ))
        self.register_tool(Tool(
            name="attack_double_spend",
            description="Submit two transfers with identical nonce from same account (ERR_018)",
            parameters={},
            handler=attack_double_spend,
        ))
        self.register_tool(Tool(
            name="attack_nonce_skip",
            description="Submit tx with nonce far ahead of current (ERR_018)",
            parameters={},
            handler=attack_nonce_skip,
        ))
        self.register_tool(Tool(
            name="attack_insufficient_funds",
            description="Transfer more than account balance (ERR_013)",
            parameters={},
            handler=attack_insufficient_funds,
        ))
        self.register_tool(Tool(
            name="attack_invalid_tx_type",
            description="Submit tx with unknown type byte 99 (ERR_019)",
            parameters={},
            handler=attack_invalid_tx_type,
        ))
        self.register_tool(Tool(
            name="attack_malformed_serialization",
            description="Send truncated/incomplete tx bytes (ERR_009)",
            parameters={},
            handler=attack_malformed_serialization,
        ))
        self.register_tool(Tool(
            name="attack_invalid_domain",
            description="Send tx bytes with wrong domain prefix (ERR_009)",
            parameters={},
            handler=attack_invalid_domain,
        ))
        self.register_tool(Tool(
            name="attack_nfc_encoding",
            description="Use non-NFC normalized UTF-8 strings (ERR_009)",
            parameters={},
            handler=attack_nfc_encoding,
        ))
        self.register_tool(Tool(
            name="attack_call_nonexistent_contract",
            description="Call a contract that was never deployed (ERR_026)",
            parameters={},
            handler=attack_call_nonexistent_contract,
        ))
        self.register_tool(Tool(
            name="attack_deploy_empty_wasm",
            description="Deploy contract with zero-length Wasm bytes",
            parameters={},
            handler=attack_deploy_empty_wasm,
        ))
        self.register_tool(Tool(
            name="attack_calldata_overflow",
            description="Send contract call with huge calldata (ERR_010)",
            parameters={},
            handler=attack_calldata_overflow,
        ))
        self.register_tool(Tool(
            name="attack_mempool_flood",
            description="Flood mempool with many rapid transfers",
            parameters={
                "count": {"type": "integer", "description": "Number of txs to submit (max 500)"},
            },
            handler=attack_mempool_flood,
        ))
        self.register_tool(Tool(
            name="attack_value_zero",
            description="Send a zero-value transfer to test edge cases",
            parameters={},
            handler=attack_value_zero,
        ))
        self.register_tool(Tool(
            name="attack_replay",
            description="Submit the same tx twice (replay attack)",
            parameters={},
            handler=attack_replay,
        ))
        self.register_tool(Tool(
            name="attack_max_size_tx",
            description="Send tx with maximum-length sender string to test size limits",
            parameters={},
            handler=attack_max_size_tx,
        ))
        self.register_tool(Tool(
            name="attack_deploy_oversized_wasm",
            description="Try to deploy >1MB Wasm bytecode (ERR_022)",
            parameters={},
            handler=attack_deploy_oversized_wasm,
        ))
        self.register_tool(Tool(
            name="attack_null_values",
            description="Send tx with empty sender and receiver strings",
            parameters={},
            handler=attack_null_values,
        ))
        self.register_tool(Tool(
            name="check_network_health",
            description="Verify the network still works after attacks (height progressing, consensus intact)",
            parameters={},
            handler=self._check_health,
        ))
        self.register_tool(Tool(
            name="get_attack_log",
            description="Review summary of all attacks attempted so far",
            parameters={},
            handler=lambda: {
                "total_attacks": len(self._attack_log),
                "recent": self._attack_log[-10:] if self._attack_log else [],
            },
        ))

    def _check_health(self) -> dict:
        from ..tools.node_tools import get_status
        results = []
        for nd in self.devnet_info.nodes:
            try:
                s = get_status(nd["grpc_addr"])
                results.append({
                    "node": nd["index"],
                    "height": s.height,
                    "state_root": s.state_root[:16],
                    "phase": s.consensus_phase,
                })
            except Exception as e:
                results.append({"node": nd["index"], "error": str(e)})
        roots = set(r.get("state_root") for r in results if "state_root" in r)
        return {
            "nodes_healthy": sum(1 for r in results if "height" in r),
            "nodes_total": len(results),
            "all_roots_equal": len(roots) <= 1,
            "details": results,
        }

    def system_prompt(self) -> str:
        return f"""You are an Adversarial (Red Team) agent for a FRG blockchain devnet.

SHARED WORLD: Other agents are active while you attack:
  - NetworkMonitor: watching for consensus failures you cause
  - TrafficGenerator: sending real transfers (attack while traffic flows)
  - ContractTester: deploying and calling contracts (attack under contract load)
  - FaultInjector: killing nodes

Your mission: actively try to break the system while the chain is under real
load from TrafficGenerator and ContractTester. Test every attack surface and
verify the system fails closed.

Attack categories:

1. SIGNATURE ATTACKS: attack_invalid_signature, attack_wrong_signer
2. NONCE ATTACKS: attack_double_spend, attack_nonce_skip
3. VALUE ATTACKS: attack_insufficient_funds, attack_value_zero
4. SERIALIZATION: attack_invalid_tx_type, attack_malformed_serialization, attack_invalid_domain
5. CONTRACT ATTACKS: attack_call_nonexistent_contract, attack_deploy_empty_wasm
6. LOAD ATTACKS: attack_mempool_flood, attack_max_size_tx, attack_replay

Strategy:
1. Run attacks one at a time, interleaving with check_network_health.
2. After each attack, verify the network stayed healthy.
3. End with mempool_flood to stress-test under load.
4. If TrafficGenerator is sending transfers, attack during that — verify
   attacks are rejected but legit transfers still go through.

When you've tested most surfaces and the network stays healthy, signal DONE.
"""
