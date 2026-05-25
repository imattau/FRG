package consensus_test

import (
    "math/big"
    "testing"
    "github.com/imattau/frg/core/consensus"
)

func TestVoteTypes(t *testing.T) {
    v := consensus.Vote{
        Type: consensus.VotePrevote,
        Height: 1,
        Round: 0,
    }
    if v.Type != 1 {
        t.Fatal("VotePrevote should be 1")
    }
}

func TestAttestationSet(t *testing.T) {
    as := consensus.AttestationSet{
        Height: 1,
        Round: 0,
    }
    if as.Height != 1 {
        t.Fatal("height mismatch")
    }
}

func TestVoteSerialise(t *testing.T) {
    var bh [32]byte
    bh[0] = 0xAB
    var vpk [32]byte
    vpk[0] = 0xCD

    v := &consensus.Vote{
        Type:        consensus.VotePrevote,
        Height:      1,
        Round:       2,
        BlockHash:   bh,
        ValidatorPK: vpk,
    }
    data, err := v.Serialize()
    if err != nil {
        t.Fatal(err)
    }
    if len(data) != 141 {
        t.Fatalf("expected 141 bytes, got %d", len(data))
    }

    got, err := consensus.DeserializeVote(data)
    if err != nil {
        t.Fatal(err)
    }
    if got.Height != v.Height || got.Round != v.Round || got.Type != v.Type || got.BlockHash != v.BlockHash || got.ValidatorPK != v.ValidatorPK {
        t.Fatalf("mismatch: %+v vs %+v", got, v)
    }
}

func TestVoteVerify(t *testing.T) {
    // This will fail until VerifyVote is implemented
    v := &consensus.Vote{}
    if consensus.VerifyVote(v) {
        t.Fatal("empty vote should fail verification")
    }
}

func TestQuorumReached(t *testing.T) {
    // 3 validators with equal stake 100 each = 300 total
    // need > 200 voted → need all 3
    validators := [][32]byte{{1}, {2}, {3}}
    stakes := []*big.Int{big.NewInt(100), big.NewInt(100), big.NewInt(100)}

    votes := map[[32]byte]consensus.Vote{
        {1}: {ValidatorPK: [32]byte{1}},
        {2}: {ValidatorPK: [32]byte{2}},
        {3}: {ValidatorPK: [32]byte{3}},
    }
    if !consensus.QuorumReached(votes, validators, stakes) {
        t.Fatal("expected quorum with all 3 validators")
    }

    // Only 2 — 200/300: 200*3=600, 300*2=600, NOT > so no quorum
    delete(votes, [32]byte{3})
    if consensus.QuorumReached(votes, validators, stakes) {
        t.Fatal("expected no quorum with exactly 2/3")
    }
}

func TestQuorumWeighted(t *testing.T) {
    // validator 0 has 201 stake, validators 1+2 have 50 each = 301 total
    // validator 0 alone: 201*3=603 > 301*2=602 → quorum
    validators := [][32]byte{{1}, {2}, {3}}
    stakes := []*big.Int{big.NewInt(201), big.NewInt(50), big.NewInt(50)}

    votes := map[[32]byte]consensus.Vote{
        {1}: {ValidatorPK: [32]byte{1}},
    }
    if !consensus.QuorumReached(votes, validators, stakes) {
        t.Fatal("expected quorum: large validator alone exceeds 2/3")
    }
}
