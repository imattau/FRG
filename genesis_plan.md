# FRG Genesis + Testnet Node Implementation Plan

## Phase 1: Genesis Logic

- [x] **Task 1: core/genesis — Load and types**
    - [x] Step 1: Write failing tests for Load
    - [x] Step 2: Run to confirm failure
    - [x] Step 3: Implement genesis.go with Load
    - [x] Step 4: Run tests — expect PASS
- [x] **Task 2: core/genesis — Apply function**
    - [x] Step 1: Write failing tests for Apply (Fresh DB, Idempotent)
    - [x] Step 2: Run to confirm failure
    - [x] Step 3: Implement Apply in genesis.go
    - [x] Step 4: Run tests — expect PASS

## Phase 2: Node Binary

- [x] **Task 3: cmd/frg-node — Skeleton and Config**
    - [x] Step 1: Define Config structure and load function
    - [x] Step 2: Implement keypair loading/generation
- [x] **Task 4: cmd/frg-node — Subsystem wiring**
    - [x] Step 1: Wire all subsystems (ledger, staking, sm, genesis, p2p, blockloop, consensus)
    - [x] Step 2: Implement signal handling for graceful shutdown
- [x] **Task 5: Full suite and verification**
    - [x] Step 1: Run all tests
    - [x] Step 2: Manual verification of node startup
