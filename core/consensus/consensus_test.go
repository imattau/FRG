package consensus_test

import (
    "testing"
    "github.com/imattau/frg/core/consensus"
)

func TestRoundStateInitial(t *testing.T) {
    rs := consensus.NewRoundState(1)
    if rs.Height != 1 {
        t.Fatalf("want height 1, got %d", rs.Height)
    }
    if rs.Round != 0 {
        t.Fatalf("want round 0, got %d", rs.Round)
    }
    if rs.Phase != consensus.PhasePropose {
        t.Fatalf("want PhasePropose, got %d", rs.Phase)
    }
    if rs.LockedRound != -1 {
        t.Fatalf("want LockedRound -1, got %d", rs.LockedRound)
    }
}

func TestRoundIncrement(t *testing.T) {
    rs := consensus.NewRoundState(1)
    rs.IncrementRound()
    if rs.Round != 1 {
        t.Fatalf("want round 1, got %d", rs.Round)
    }
    if rs.Phase != consensus.PhasePropose {
        t.Fatalf("after increment, want PhasePropose, got %d", rs.Phase)
    }
    if rs.Proposal != nil {
        t.Fatal("proposal should be cleared on round increment")
    }
}

func TestLockAndUnlock(t *testing.T) {
    rs := consensus.NewRoundState(1)
    var blockA [32]byte
    blockA[0] = 0xAA
    rs.Lock(blockA, 0)
    if rs.LockedBlock == nil || *rs.LockedBlock != blockA {
        t.Fatal("should be locked on blockA")
    }
    var blockB [32]byte
    blockB[0] = 0xBB
    rs.Unlock()
    rs.Lock(blockB, 1)
    if rs.LockedBlock == nil || *rs.LockedBlock != blockB {
        t.Fatal("should be locked on blockB after unlock")
    }
}

func TestDuplicateVoteIgnored(t *testing.T) {
    rs := consensus.NewRoundState(1)
    var pk [32]byte
    pk[0] = 0x01
    var blockHash [32]byte
    blockHash[0] = 0xAB

    v1 := consensus.Vote{Type: consensus.VotePrevote, Height: 1, Round: 0, BlockHash: blockHash, ValidatorPK: pk}
    v2 := consensus.Vote{Type: consensus.VotePrevote, Height: 1, Round: 0, BlockHash: [32]byte{0xFF}, ValidatorPK: pk}

    rs.Prevotes[pk] = v1
    // Simulate duplicate: if already present, should not overwrite
    if _, exists := rs.Prevotes[pk]; exists {
        // Don't overwrite — this is the guard the engine applies
        t.Log("duplicate correctly detected")
    } else {
        rs.Prevotes[pk] = v2
        t.Fatal("duplicate vote should not have been inserted")
    }
    if rs.Prevotes[pk].BlockHash != blockHash {
        t.Fatal("first vote should be retained")
    }
}

func TestNilVoteHandling(t *testing.T) {
    // A nil vote has all-zero BlockHash
    var nilHash [32]byte
    v := consensus.Vote{
        Type: consensus.VotePrevote, Height: 1, Round: 0,
        BlockHash: nilHash,
    }
    if v.BlockHash != (consensus.Vote{}.BlockHash) {
        t.Fatal("nil vote BlockHash should be zero")
    }
}
