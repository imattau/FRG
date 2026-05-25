# FRG Block Production Loop Implementation Plan

## Phase 1: Mempool and Skeleton

- [x] **Task 1: Skeleton + mempool internals**
    - [x] Step 1: Write the failing tests for enqueue, dedup, and cap
    - [x] Step 2: Run to confirm failure
    - [x] Step 3: Implement skeleton and mempool
    - [x] Step 4: Run tests — expect PASS

## Phase 2: Proposal Builder and Cleanup

- [x] **Task 2: Propose and OnCommit**
    - [x] Step 1: Write tests for `Propose` and `OnCommit`
    - [x] Step 2: Run tests — expect FAIL
    - [x] Step 3: Implement `Propose` and `OnCommit` fully
    - [x] Step 4: Run tests — expect PASS
- [x] **Task 3: Integration and P2P**
    - [x] Step 1: Add integration tests with mocked P2P
    - [x] Step 2: Run tests
- [x] **Task 4: Full suite and push**
