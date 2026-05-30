# FRG — Fractal Renormalisation Group Ledger

A distributed ledger protocol that organises high-throughput transactions into multi-resolution K-ary state trees, with real-time variance and macroeconomic telemetry embedded in cryptographic roots.

---

## Protocol Overview

FRG processes up to **65,536 transactions per block**, organising them into a K=4 Renormalisation Group (RG) state tree. Each internal node carries aggregated volume and variance statistics and a derived economic signature, embedding macroeconomic telemetry directly in the state root. The result: any node can verify the full economic state of the network from a single 32-byte hash.

### Core Constants

| Constant | Value |
|---|---|
| Branching factor K | 4 |
| Max transactions/block (T_MAX) | 65,536 |
| Fixed-point denominator (SCALE) | 10^18 |
| Hash function | SHA2-256-SINGLE |
| Signing scheme | Ed25519 (2-of-2 for transfers) |
| Max tx payload | 70,000 bytes |

---

## Architecture

```
core/
  errors/    — protocol error codes (ERR_001–ERR_020)
  hash/      — SHA2-256, domain prefixes, UINT256_MAX
  keys/      — Ed25519 keypair generation, signing, verification
  tx/        — transaction serialisation, 2-of-2 signing, nonce, miss evidence
  node/      — RGNode serialisation, coarse-graining, signature derivation
  tree/      — K-ary state tree construction, empty block anchor
  ledger/    — balance store (bbolt), Transfer (nonce-enforced), Burn, Seed, Move
  staking/   — Bond, Unbond, Finalize, Slash (equivocation), RecordMiss (liveness)
  gas/       — EIP-1559 base fee, fee accrual, validator/staker distribution
  mint/      — staking-ratio-driven block rewards, split distribution
  leader/    — deterministic proposer election, skip rotation
  p2p/       — libp2p node, GossipSub tx/block gossip, Kademlia peer discovery
validator/   — stateless block state root validation
client/      — offline tx queue, gRPC transport
```

---

## Transaction Types

**TRANSFER** (`Type=1`) — value transfer between accounts. Requires 2-of-2 Ed25519 signatures (sender + receiver). Strictly sequential nonce enforcement.

**MISS_EVIDENCE** (`Type=2`) — records a validator liveness miss on-chain. Submitted by the next validator in the skip rotation. Single signature (reporter only). Committed to state root — independently verifiable by any node.

---

## Economic Model

### Staking
- Minimum bond: 1,000 quanta
- Unbonding lockup: 1,000 blocks
- Equivocation: full bond slashed, validator removed
- Liveness: 10% bond slashed after 5 cumulative misses; validator remains active

### Gas
- EIP-1559 style: base fee adjusts ±12.5% per block (target: 32,768 txs/block)
- Minimum base fee: 1 quanta
- Distribution: 70% to validators (equal split), 30% to stakers (proportional to bond)
- Pull model: fees accumulate per-validator, claimed explicitly

### Mint
- Target staking ratio: 50% of supply
- Maximum annual emission: 10% of supply
- At 50% staked: zero emission
- Initial supply: 400,000,000 FRG
- Blocks per year: 5,256,000

---

## Consensus (in progress)

| Component | Status |
|---|---|
| Miss evidence transaction | Spec + plan complete |
| Leader election (hash-based, skip rotation) | Spec + plan complete |
| Liveness penalties (5-miss threshold, 10% slash) | Spec + plan complete |
| P2P networking (libp2p, GossipSub) | Spec + plan complete |
| BFT voting / finality | Pending |
| State machine | Pending |
| Genesis | Pending |

Leader election: `proposer = sortedValidators[H(prevStateRoot ∥ blockHeight) mod n]`

---

## P2P Network

- **Transport:** TCP + Noise + yamux
- **Discovery:** Kademlia DHT (`frg/kad/v1`), mDNS (testnet)
- **Gossip:** GossipSub on two topics:
  - `frg/tx/v1` — transaction gossip (sig validated before forwarding)
  - `frg/block/v1` — block header announcements (proposer sig verified before forwarding)
- **Consensus votes:** direct gRPC between validators (time-critical, not gossiped)

---

## Getting Started

### Prerequisites
- Go 1.25.7+

### Build

```bash
go build ./...
```

### Run

```bash
./frg-node
```

On first launch the node now bootstraps local defaults in the current directory:
- `frg.key` for the node keypair
- `genesis.json` for the bootstrap validator set
- `frg.db` for the local BoltDB state
- gRPC admin API on `127.0.0.1:50051`

Use the gRPC API from the `client` package or any gRPC client that speaks the `frg.FRG` service in `proto/frg.proto`.

If you want a minimal local setup for the web client or direct gRPC submits, start the node in gRPC-only mode:

```bash
./frg-node -grpc-only
```

That skips P2P/blockloop startup and brings up the admin API immediately on `127.0.0.1:50051`.

### Web Client

```bash
./frg-web
```

By default it opens on `http://127.0.0.1:8080` and points at `127.0.0.1:50051`.
Use the page to submit raw hex-encoded transactions, submit batches, stream block headers, and poll live node status from any FRG gRPC server.

### Test

```bash
go test ./...
```

### Benchmarks

```bash
go test ./test/e2e/... -bench=. -benchmem
```

---

## Error Codes

| Code | Label | Condition |
|---|---|---|
| ERR_001 | ARITHMETIC_OVERFLOW | Result > UINT256_MAX |
| ERR_002 | INVALID_CHILD_ARITY | child_count violates spec |
| ERR_003 | SCALE_DOMAIN_FAULT | Λ ≠ K^n |
| ERR_004 | HASH_BOUNDARY_MISMATCH | Hash not exactly 32 bytes |
| ERR_005 | MASK_OUT_OF_BOUNDS | padding_mask ≥ 2^K |
| ERR_006 | PADDING_SUBSTITUTION_FRAUD | Masked child ≠ canonical NULL |
| ERR_007 | SIGNATURE_MISREPRESENTATION | Recomputed sig ≠ declared |
| ERR_008 | NAMESPACE_ESCAPE_FAULT | Cross-block node injection |
| ERR_009 | CANONICAL_ENCODING_DISTORTION | Invalid length-prefixed encoding |
| ERR_010 | DOS_SIZE_EXCEEDED | Block > T_MAX or tx > 70KB |
| ERR_011 | ROOT_MISMATCH | Computed root ≠ declared root |
| ERR_012 | INVALID_SIGNATURE | Ed25519 verification failed |
| ERR_013 | INSUFFICIENT_FUNDS | Balance < transfer value |
| ERR_014 | ALREADY_BONDED | Validator already has active bond |
| ERR_015 | NOT_BONDED | Validator has no active bond |
| ERR_016 | UNBONDING_PENDING | Unbonding already in progress |
| ERR_017 | BOND_BELOW_MINIMUM | Bond < 1,000 quanta |
| ERR_018 | SEQUENCE_FAULT | tx.Nonce ≠ lastNonce + 1 |
| ERR_019 | INVALID_TX_TYPE | Unknown Type byte |
| ERR_020 | EMPTY_VALIDATOR_SET | Validator set is empty |

---

## Repository Layout

```
core/           — protocol packages (no main, no HTTP)
validator/      — stateless block validator
client/         — node client with offline queue
test/e2e/       — integration and benchmark tests
docs/
  superpowers/
    specs/      — design documents
    plans/      — implementation plans
```

---

## Spec

Protocol spec is embedded in `CLAUDE.md`. Version: **v1.0.0** (frozen).
