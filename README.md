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
| Token denomination | 1 FRG = 10^18 quanta |
| Hash function | SHA2-256-SINGLE |
| Signing scheme | Ed25519 sender signatures |
| Max tx payload | 70,000 bytes |

---

## Architecture

```
core/
  errors/    — protocol error codes (ERR_001–ERR_027)
  hash/      — SHA2-256, domain prefixes, UINT256_MAX
  keys/      — Ed25519 keypair generation, signing, verification
  tx/        — transaction serialisation, sender signing, nonce, miss evidence
  node/      — RGNode serialisation, coarse-graining, signature derivation
  tree/      — K-ary state tree construction, retained RG queries, proofs
  ledger/    — balance store (bbolt), Transfer (nonce-enforced), Burn, Seed, Move
  staking/   — Bond, Unbond, Finalize, Slash (equivocation), RecordMiss (liveness)
  gas/       — EIP-1559 base fee, fee accrual, validator/staker distribution
  mint/      — staking-ratio-driven block rewards, split distribution
  contract/  — deterministic WASM runtime, contract state, deploy/call execution
  genesis/   — genesis allocation, validator bootstrap, total supply setup
  leader/    — deterministic proposer election, skip rotation
  consensus/ — proposer/vote state machine, attestations, catch-up validation
  blockloop/ — mempool, block proposal batching, committed block distribution
  p2p/       — libp2p node, GossipSub tx/block gossip, Kademlia peer discovery
validator/   — stateless block state root validation
client/      — offline tx queue, gRPC transport
wallet/      — Go wallet SDK and faucet helper
```

---

## Transaction Types

**TRANSFER** (`Type=1`) — value transfer between accounts. Requires a sender Ed25519 signature. Strictly sequential nonce enforcement.

**MISS_EVIDENCE** (`Type=2`) — records a validator liveness miss on-chain. Submitted by the next validator in the skip rotation. Single signature (reporter only). Committed to state root — independently verifiable by any node.

**CONTRACT_DEPLOY** (`Type=3`) — deploys deterministic WASM bytecode and derives the contract address from the sender pubkey and nonce.

**CONTRACT_CALL** (`Type=4`) — calls a deployed WASM contract. The current dispatcher selects the exported function from the first four calldata bytes. Contracts can call the `frg.bn254_pairing_check(ptr,len)` host precompile with repeated 192-byte `(G1.Marshal || G2.Marshal)` pairs; it returns `1` when the product of pairings equals one, `0` for a valid false check, and `-1` for malformed input or insufficient gas.

Contract compute gas uses a fixed consensus conversion of Wasmtime fuel to protocol gas. See [Protocol Gas Calibration](docs/protocol-gas.md).

**BOND** (`Type=5`) — locks the sender's stake in escrow and activates the sender pubkey as a validator once the minimum bond is met.

**UNBOND** (`Type=6`) — starts the validator unbonding lockup and removes the validator from the active proposer set.

**FINALIZE_UNBOND** (`Type=7`) — releases escrowed stake after the unbonding lockup has elapsed.

**CLAIM_REWARDS** (`Type=8`) — claims validator reward balances into the validator account.

**EQUIVOCATION_EVIDENCE** (`Type=9`) — submits two conflicting signed consensus votes and slashes the equivocating validator escrow.

---

## Structural Telemetry

FRG now exposes the RG information it derives while committing blocks:

- transaction counts, total value, mean value, and variance
- transaction type counts
- per-level RG signature histograms
- contract density, including touched contract-state RG nodes for newly committed blocks
- volatile and stagnant region indexes

The node persists a compact exact telemetry summary for each newly committed block. Older pre-telemetry blocks are reconstructed from stored transactions; if they contain contract deploys or calls, query responses warn that historical contract-state RG nodes are unavailable for that block.

Telemetry is available through the node gRPC API (`GetBlockTelemetry`), the Go wallet SDK (`BlockTelemetry`), and the MCP tool `frg_get_block_telemetry`.

## Economic Model

### Denominations

FRG balances are stored on-chain as unsigned integer quanta. One FRG is
`1,000,000,000,000,000,000` quanta, and one quantum is the smallest transferable
unit. User-facing tools (`frg-cli --amount`, `frg-wallet` JSON `amount`/`value`,
and `frg-mcp` `amount`/`value`/policy limits) accept FRG decimal strings such as
`1`, `1.5`, or `0.000000000000000001`. Raw quanta are still available through
explicit `*_quanta` fields and `frg-cli --amount-quanta`.

### Staking
- Minimum bond: 1,000 FRG (`1,000,000,000,000,000,000,000` quanta)
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
- Per-block mint rewards are split across bonded validators and credited to claimable validator reward accounts.

---

## Consensus And State Machine

| Component | Status |
|---|---|
| Genesis allocation and validator bootstrap | Implemented |
| State machine with atomic block commits | Implemented |
| Leader election (hash-based, skip rotation) | Implemented |
| P2P networking (libp2p, GossipSub, Kademlia, mDNS) | Implemented |
| Mempool and block proposal loop | Implemented |
| BFT voting / finality with attestations | Implemented |
| Miss evidence transaction | Implemented |
| Liveness penalties (5-miss threshold, 10% slash) | Implemented |
| Catch-up validation from peers | Implemented |

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

Use the gRPC API from the `client` package or another client that implements the `frg.FRG` service in `proto/frg.proto`. Read-only methods are declared in the proto, including status, account, contract state, validator list, mempool, and block telemetry. Transaction submission takes opaque FRG signed transaction bytes; signed transaction construction is handled by the Go wallet/transaction code and is not specified in the proto.

If you want a minimal local setup for the web client or direct gRPC submits, start the node in gRPC-only mode:

```bash
./frg-node -grpc-only
```

That skips P2P/blockloop startup and brings up the admin API immediately on `127.0.0.1:50051`.

For a real local consensus node, use the first-run initializer:

```bash
go build -o frg-node ./cmd/frg-node
./frg-node init-first-network --data-dir frg-first --chain-id frg-devnet-1
cd frg-first
../frg-node --config config.toml
```

### Web Client

```bash
./frg-web
```

By default it opens on `http://127.0.0.1:8080` and points at `127.0.0.1:50051`.
Use the page to submit raw hex-encoded transactions, submit batches, stream block headers, and poll live node status from any FRG gRPC server.

### Wallet SDK and Local API

Build `frg-wallet` for a local developer wallet API:

```bash
go build -o frg-wallet ./cmd/frg-wallet
./frg-wallet --create-key --node 127.0.0.1:50051 --listen 127.0.0.1:8090
```

It exposes local HTTP endpoints for pubkey, account/balance, transfers, bonding, contract deploy/call/state queries, faucet requests, node status, and validators. The reusable Go package is available at `github.com/imattau/frg/wallet`. See [docs/wallet-api.md](docs/wallet-api.md).

### Token Distribution

New users and validators obtain FRG from genesis allocations, a funded treasury account, another holder, a configured faucet, or protocol mint rewards after validators are bonded and blocks are produced. Wallet and MCP tools do not mint tokens; they only request faucet funding or sign transactions using tokens already available to their account.

### AI Agent MCP

Build `frg-mcp` to let AI agents inspect FRG state and, with an explicit local policy, transact autonomously:

```bash
go build -o frg-mcp ./cmd/frg-mcp
./frg-mcp --create-key --key frg-agent.key --node 127.0.0.1:50051
```

The MCP exposes read tools for status, accounts, validators, mempool, contract state, block telemetry, operator health/readiness, faucet requests, and the standard agent work-contract convention. Policy-gated autonomous tools can transfer, bond, unbond, finalize unbonding, claim rewards, deploy contracts, call contracts, and invoke standard work-contract actions. See [docs/mcp.md](docs/mcp.md).

### Validator Docker Quickstart

For a containerized validator setup with mounted genesis/key data and environment-based config generation, see [docs/operator-quickstart.md](docs/operator-quickstart.md).

### Test

```bash
go test ./...
```

### Benchmarks

```bash
go test ./benchmarks/... -bench=. -benchmem
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
| ERR_021 | BLOCK_HEIGHT_SEQUENCE_FAULT | Block height or parent state root is invalid |
| ERR_022 | CONTRACT_BYTECODE_TOO_LARGE | WASM bytecode exceeds protocol limits |
| ERR_023 | CONTRACT_OUT_OF_GAS | Contract execution exceeds available gas |
| ERR_024 | CONTRACT_TRAP | Contract execution trapped |
| ERR_025 | CONTRACT_NON_DETERMINISTIC | Contract uses disallowed imports or nondeterministic behavior |
| ERR_026 | CONTRACT_NOT_FOUND | Contract address has no deployed bytecode |
| ERR_027 | CONTRACT_STATE_INVALID | Contract state key/value is invalid |

---

## Repository Layout

```
core/           — protocol packages (no main, no HTTP)
validator/      — stateless block validator
client/         — node client with offline queue
wallet/         — Go wallet SDK
cmd/            — node, CLI, devnet, faucet, wallet API, MCP, web, stress tools
docker/         — container entrypoint and first-run setup helpers
agents/         — optional LLM-driven devnet test swarm
test/e2e/       — integration and benchmark tests
docs/
  mcp.md
  operator-quickstart.md
  wallet-api.md
  production.md
```

---

## Spec

Protocol spec is embedded in `CLAUDE.md`. Version: **v1.0.0** (frozen).
