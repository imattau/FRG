"""
Transaction serialization, signing, and submission.

Replicates the Go serialization format exactly:
  TX_V1\x00(6) Type(1) Len_Sender(2) Sender Len_Rcvr(2) Receiver
  Value(32) Nonce(8) MissedHeight(8) MissedProposer(32) SkipIndex(4)
  [contract extension]
  SenderPubKey(32) ReceiverPubKey(32) SenderSig(64) ReceiverSig(64)
"""

from __future__ import annotations

import hashlib
import struct
from typing import Optional

from nacl.signing import SigningKey
from nacl.encoding import RawEncoder


DOMAIN_TX = b"TX_V1\x00"

TX_TYPE_TRANSFER = 1
TX_TYPE_MISS_EVIDENCE = 2
TX_TYPE_CONTRACT_DEPLOY = 3
TX_TYPE_CONTRACT_CALL = 4

MAX_TX_BYTES = 70000


def _sha256(data: bytes) -> bytes:
    return hashlib.sha256(data).digest()


def _sign_ed25519(seed_32: bytes, message: bytes) -> bytes:
    sk = SigningKey(seed_32)
    signed = sk.sign(message)
    return signed.signature


def _build_unsigned(
    tx_type: int,
    sender: str,
    receiver: str,
    value: int,
    nonce: int,
    wasm_bytes: Optional[bytes] = None,
    call_data: Optional[bytes] = None,
) -> bytes:
    sender_utf8 = sender.encode("utf-8")
    receiver_utf8 = receiver.encode("utf-8")

    value_bytes = value.to_bytes(32, "big")

    buf = bytearray()
    buf.extend(DOMAIN_TX)
    buf.append(tx_type)
    buf.extend(struct.pack(">H", len(sender_utf8)))
    buf.extend(sender_utf8)
    buf.extend(struct.pack(">H", len(receiver_utf8)))
    buf.extend(receiver_utf8)
    buf.extend(value_bytes)
    buf.extend(struct.pack(">Q", nonce))
    buf.extend(b"\x00" * 8)   # MissedHeight
    buf.extend(b"\x00" * 32)  # MissedProposer
    buf.extend(b"\x00" * 4)   # SkipIndex

    if tx_type == TX_TYPE_CONTRACT_DEPLOY and wasm_bytes:
        buf.extend(struct.pack(">I", len(wasm_bytes)))
        buf.extend(wasm_bytes)
    elif tx_type == TX_TYPE_CONTRACT_CALL and call_data:
        buf.extend(struct.pack(">H", len(call_data)))
        buf.extend(call_data)

    return bytes(buf)


def serialize_transfer(
    sender: str,
    receiver: str,
    value: int,
    nonce: int,
    sender_pubkey: bytes,
    receiver_pubkey: bytes,
    sender_seed: bytes,
    receiver_seed: bytes,
) -> bytes:
    sender_pubkey = _pad_or_truncate(sender_pubkey, 32)
    receiver_pubkey = _pad_or_truncate(receiver_pubkey, 32)

    unsigned = _build_unsigned(TX_TYPE_TRANSFER, sender, receiver, value, nonce)
    msg_hash = _sha256(unsigned)

    sender_sig = _sign_ed25519(sender_seed[:32], msg_hash)
    receiver_sig = _sign_ed25519(receiver_seed[:32], msg_hash)

    full = bytearray()
    full.extend(unsigned)
    full.extend(sender_pubkey)
    full.extend(receiver_pubkey)
    full.extend(sender_sig)
    full.extend(receiver_sig)
    return bytes(full)


def serialize_contract_deploy(
    sender: str,
    nonce: int,
    sender_pubkey: bytes,
    sender_seed: bytes,
    wasm_bytes: bytes,
    value: int = 0,
) -> bytes:
    sender_pubkey = _pad_or_truncate(sender_pubkey, 32)
    unsigned = _build_unsigned(
        TX_TYPE_CONTRACT_DEPLOY,
        sender, "", value, nonce,
        wasm_bytes=wasm_bytes,
    )
    msg_hash = _sha256(unsigned)
    sender_sig = _sign_ed25519(sender_seed[:32], msg_hash)

    empty_pubkey = b"\x00" * 32
    full = bytearray()
    full.extend(unsigned)
    full.extend(sender_pubkey)
    full.extend(empty_pubkey)  # receiver pubkey (unused)
    full.extend(sender_sig)
    full.extend(b"\x00" * 64)  # receiver sig (unused)
    return bytes(full)


def serialize_contract_call(
    sender: str,
    nonce: int,
    sender_pubkey: bytes,
    sender_seed: bytes,
    contract_addr: bytes,
    call_data: bytes,
    value: int = 0,
) -> bytes:
    sender_pubkey = _pad_or_truncate(sender_pubkey, 32)
    contract_addr = _pad_or_truncate(contract_addr, 32)

    unsigned = _build_unsigned(
        TX_TYPE_CONTRACT_CALL,
        sender, "", value, nonce,
        call_data=call_data,
    )
    msg_hash = _sha256(unsigned)
    sender_sig = _sign_ed25519(sender_seed[:32], msg_hash)

    full = bytearray()
    full.extend(unsigned)
    full.extend(sender_pubkey)
    full.extend(contract_addr)  # receiver pubkey = contract address
    full.extend(sender_sig)
    full.extend(b"\x00" * 64)  # receiver sig (unused)
    return bytes(full)


def txid(tx_bytes: bytes) -> bytes:
    unsigned_end = len(tx_bytes) - 192  # strip 4×32 + 2×64 trailing bytes
    unsigned = tx_bytes[:unsigned_end]
    return _sha256(unsigned)


def _pad_or_truncate(data: bytes, length: int) -> bytes:
    if len(data) > length:
        return data[:length]
    if len(data) < length:
        return data + b"\x00" * (length - len(data))
    return data
