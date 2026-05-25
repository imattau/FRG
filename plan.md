# FRG Consensus Implementation Plan

## Phase 1: Core Types and Serialization

- [x] **Task 1: Vote, BlockProposal, and AttestationSet types**
    - [x] Step 1: Write the failing test in `core/consensus/vote_test.go`
    - [x] Step 2: Run test — expect FAIL
    - [x] Step 3: Create `core/consensus/vote.go` with types
    - [x] Step 4: Run tests — expect PASS
    - [ ] Step 5: Commit (skipped)
- [x] **Task 2: Serialisation and Verification**
    - [x] Step 1: Write the failing test for serialization and verification in `core/consensus/vote_test.go`
    - [x] Step 2: Run test — expect FAIL
    - [x] Step 3: Add serialization and verification to `core/consensus/vote.go`
    - [x] Step 4: Run tests — expect PASS
    - [ ] Step 5: Commit (skipped)
- [x] **Task 3: Quorum calculation**
    - [x] Step 1: Write the failing test in `core/consensus/vote_test.go`
    - [x] Step 2: Run test — expect FAIL
    - [x] Step 3: Add `QuorumReached` to `core/consensus/vote.go`
    - [x] Step 4: Run tests — expect PASS
    - [ ] Step 5: Commit (skipped)

## Phase 2: Engine and State Machine
...
- [x] **Task 4: Round state machine and Engine**
    - [x] Step 1: Write the failing test in `core/consensus/consensus_test.go`
    - [x] Step 2: Run test — expect FAIL
    - [x] Step 3: Create `core/consensus/consensus.go`
    - [x] Step 4: Run tests — expect PASS
    - [x] Step 5: Compile check
    - [ ] Step 6: Commit (skipped)
- [x] **Task 5: Deterministic tests for timeout and duplicate vote**
    - [x] Step 1: Add timeout and duplicate vote tests to `core/consensus/consensus_test.go`
    - [x] Step 2: Run all consensus tests
    - [ ] Step 3: Commit (skipped)
- [x] **Task 6: Property tests**
    - [x] Step 1: Write property tests in `core/consensus/property_test.go`
    - [x] Step 2: Run property tests
    - [ ] Step 3: Commit (skipped)

## Phase 3: Finalization

- [x] **Task 7: Full suite and push**
    - [x] Step 1: Run all tests
    - [ ] Step 2: Push (skipped)

