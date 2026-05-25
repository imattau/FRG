# FRG Multi-Node Integration Test Implementation Plan

## Phase 1: Test Setup and Node Stack

- [x] **Task 1: Define `nodeStack` and setup helper**
  - [x] Step 1: Create `test/e2e/multinode_test.go`
  - [x] Step 2: Define `nodeStack` struct to hold node subsystems (`Keypair`, `DB`, `Ledger`, `Staking`, `StateMachine`, `P2P`, `BlockLoop`, `ConsensusEngine`).
  - [x] Step 3: Implement `newNodeStack(t *testing.T, listenAddr string) *nodeStack` to initialize all components.

## Phase 2: Test Execution Flow

- [x] **Task 2: Implement `TestMultiNodeConsensus`**
  - [x] Step 1: Skip if `testing.Short()`
  - [x] Step 2: Initialize 4 `nodeStack` instances.
  - [x] Step 3: Seed balances (9000) and bond validators (1000) for all 4 nodes at height 0 to satisfy the 2/3+ quorum.
  - [x] Step 4: Full mesh connect all nodes over P2P.
  - [x] Step 5: Start all blockloops and consensus engines.
  - [x] Step 6: Create and enqueue a transfer transaction into node 0's mempool.
  - [x] Step 7: Poll every 500ms for up to 30s, checking if all nodes reach `CurrentHeight() >= 1`.
  - [x] Step 8: Assert that all nodes have the identical `CurrentStateRoot()` at height 1.
  - [x] Step 9: Gracefully stop all engines, blockloops, and p2p nodes.

## Phase 3: Final Verification

- [x] **Task 3: Run and verify the multi-node test**
  - [x] Step 1: Run `go test ./test/e2e/... -run TestMultiNodeConsensus -v`
  - [x] Step 2: Ensure it completes successfully and consensus is reached across the 4 nodes.