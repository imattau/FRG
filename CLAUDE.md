# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

This repository implements the **Fractal Renormalisation Group (RG) Ledger Protocol v1.0.0** — a distributed ledger system that organises high-throughput transactions into multi-resolution K-ary state trees, with real-time variance and macroeconomic telemetry embedded in cryptographic roots.

The spec is frozen at v1.0.0. Any implementation must match the spec exactly; there is no room for interpretation on consensus-critical fields.

## Core Protocol Constants

These are fixed for this network profile and must never be treated as configurable:

| Constant | Value | Derivation |
|---|---|---|
| `K` (branching factor) | `4` | fixed |
| `n_max` (max exponent) | `8` | fixed |
| `LAMBDA_MAX` | `65536` | `4^8` |
| `T_MAX` (txs/block) | `65536` | `= LAMBDA_MAX` |
| `SCALE` | `10^18` | fixed-point denominator |
| Max tx payload bytes | `70000` | entire serialised `Tx_Bytes` |
| Max tree depth | `9` | `n_max + 1` |

Valid scale values are `Λ = K^n` for `n ∈ {0,1,...,8}`: `1, 4, 16, 64, 256, 1024, 4096, 16384, 65536`.

## Cryptographic Primitives

- Hash: **SHA2-256-SINGLE** (one pass, standard SHA-256). Symbol: `H(x)`.
- All integers: **unsigned, big-endian** unless explicitly stated otherwise.
- Strings: **NFC-normalised → UTF-8** before hashing.
- All integer division: **floor toward zero** (`⌊x/y⌋`).

### Domain Prefixes (must prepend before hashing)

| Domain | Bytes |
|---|---|
| `TX_V1\x00` | `0x54585F563100` (6 bytes) |
| `RG_NODE_V1\x00` | `0x52475F4E4F44455F563100` (11 bytes) |
| `NULL_PAD_V1\x00` | `0x4E554C4C5F5041445F563100` (12 bytes) |
| `EMPTY_BLOCK_V1\x00` | `0x454D5054595F424C4F434B5F563100` (15 bytes) |

## Serialisation Formats

### Transaction (`Tx_Bytes`)
```
[TX_V1\x00 — 6 bytes]
[Len_Sender — uint16, 2 bytes]
[Sender — UTF-8 NFC, variable]
[Len_Rcvr — uint16, 2 bytes]
[Receiver — UTF-8 NFC, variable]
[Value — uint256, 32 bytes]  ← fixed-point, multiply by 10^18
[Nonce — uint64, 8 bytes]
```
`txid = H(Tx_Bytes)`

### RGNode (`Node_Bytes`)
```
[RG_NODE_V1\x00 — 11 bytes]
[Scale Λ — uint32, 4 bytes]
[Total Volume — uint256, 32 bytes]
[True Variance — uint256, 32 bytes]
[Signature — uint8, 1 byte]   ← enum: 1=ATOMIC, 2=NULL_PAD, 3=STAGNANT_STATE, 4=LAMINAR_FLOW, 5=VOLATILE_SHOCK
[Child Count — uint16, 2 bytes]
[Child Roots — M × 32 bytes]
```
`node_root = H(Node_Bytes)`

## Block Processing Pipeline

Bottom-up, deterministic:

1. **Atomic Ingestion** — wrap each `txid` into an atomic node (`Λ=1`, `child_count=1`).
2. **K-ary Chunking** — split layer into chunks of `K=4`. Pad trailing short chunk with canonical `NULL_Λ` nodes until length = 4. Set `padding_mask` bits for appended positions.
3. **Coarse-Graining** — call `coarse_grain()` on each chunk → one parent node at `Λ×K`. Collect parents into new layer. Repeat steps 2–3 until one root remains.

### Padding Mask
- `0 ≤ padding_mask < 2^K` (i.e. `< 16` for `K=4`).
- Bit `i` = LSB-indexed: `is_null[i] = (padding_mask >> i) & 1`.
- If `is_null[i] == 1`, child `i` **must** equal the canonical `NULL_Λ` root for that scale.

### Child Count Rules
- Atomic node (`Λ=1`): `child_count = 1`, child = `txid`.
- Macro node (`Λ>1`): `child_count = 4`.
- Empty block node: `child_count = 1`.

### Signature Recomputation (Mandatory — ERR_007 if mismatch)
The validator must independently derive the signature from raw volume/variance/count fields. The node's declared byte is untrusted.

Derivation rules (applied in order):
1. **NULL_PAD** — all children are canonical `NULL_Λ` roots, Volume=0, Variance=0, Count=0
2. **ATOMIC** — Scale=1, child is a txid (not the empty block sentinel)
3. **STAGNANT_STATE** — Volume=0 and Variance=0 (no active txs in subtree)
4. **LAMINAR_FLOW** — Variance=0 (uniform values)
5. **VOLATILE_SHOCK** — CV² > 4: `Variance > 4 × (Volume / Count)²` (high relative dispersion)
6. **LAMINAR_FLOW** — default

### Volume & Variance
- **Volume** = sum of all active (non-null) transaction values at the leaf level, propagated up.
- **True Variance** = population variance (no Bessel correction) over active leaf values.

## Empty Block Anchor

When a block has zero transactions, the state root is the normative constant derived as:
```
child_root = H(EMPTY_BLOCK_V1\x00)
node_bytes = RG_NODE_V1\x00 || scale=1 || volume=0 || variance=0 || sig=3 || child_count=1 || child_root
empty_root = H(node_bytes)
```
Serialised node bytes (hex):
```
0x52475F4E4F44455F56310000000001000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000030001bc954f91cf645624ebfcfa3c28987ec84a7e9373ab12d7c49c3c1b12b2b1b1b1
```

## Namespace & Cross-Block Isolation

A node is uniquely identified by `(h, Λ, idx)` where `h` = block height. Block height is **not** mixed into node serialisation — it is an out-of-band routing parameter. Any cross-block node injection is `ERR_008 NAMESPACE_ESCAPE_FAULT`.

## Error Codes (Fail Closed)

| Code | Label | Condition |
|---|---|---|
| ERR_001 | ARITHMETIC_OVERFLOW | Result > UINT256_MAX or < 0 |
| ERR_002 | INVALID_CHILD_ARITY | child_count violates Section 3.4 |
| ERR_003 | SCALE_DOMAIN_FAULT | Λ ≠ K^n or Λ > 65536 |
| ERR_004 | HASH_BOUNDARY_MISMATCH | Any hash not exactly 32 bytes |
| ERR_005 | MASK_OUT_OF_BOUNDS | padding_mask ≥ 2^K |
| ERR_006 | PADDING_SUBSTITUTION_FRAUD | Masked child ≠ canonical NULL_Λ |
| ERR_007 | SIGNATURE_MISREPRESENTATION | Recomputed signature ≠ declared |
| ERR_008 | NAMESPACE_ESCAPE_FAULT | Cross-block node injection |
| ERR_009 | CANONICAL_ENCODING_DISTORTION | Input doesn't match length-prefixed spec |
| ERR_010 | DOS_SIZE_EXCEEDED | Block tx count > T_MAX or tx payload > 70KB |

On any fault: fail closed, discard all uncommitted state, report error code, disconnect peer.

## Critical Ordering Rules

- Transaction order within a block is **consensus-critical** and set by the proposer. It must not be altered.
- Child roots within a node **must preserve proposer order**. Lexicographic sorting of child roots is **strictly prohibited** (would mutate the state_root hash).
