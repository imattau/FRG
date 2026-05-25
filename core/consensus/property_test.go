package consensus_test

import (
    "math/big"
    "testing"

    "github.com/imattau/frg/core/consensus"
    "github.com/imattau/frg/core/keys"
    "pgregory.net/rapid"
)

func TestPropQuorumMonotone(t *testing.T) {
    rapid.Check(t, func(rt *rapid.T) {
        n := rapid.IntRange(3, 10).Draw(rt, "n")
        validators := make([][32]byte, n)
        stakes := make([]*big.Int, n)
        for i := range validators {
            validators[i][0] = byte(i + 1)
            stakes[i] = big.NewInt(int64(rapid.IntRange(1, 100).Draw(rt, "stake")))
        }

        // Build votes incrementally — quorum once reached must stay reached
        votes := make(map[[32]byte]consensus.Vote)
        var blockHash [32]byte
        blockHash[0] = 0x42
        reachedAt := -1
        for i := range validators {
            votes[validators[i]] = consensus.Vote{ValidatorPK: validators[i], BlockHash: blockHash}
            if consensus.QuorumReached(votes, validators, stakes) {
                if reachedAt == -1 {
                    reachedAt = i
                }
            } else if reachedAt != -1 {
                rt.Fatalf("quorum was lost after being reached at index %d", reachedAt)
            }
        }
    })
}

func TestPropVoteSerialiseInverse(t *testing.T) {
    rapid.Check(t, func(rt *rapid.T) {
        kp, err := keys.GenerateKeypair()
        if err != nil {
            rt.Fatal(err)
        }
        var bh [32]byte
        bh[0] = byte(rapid.IntRange(0, 255).Draw(rt, "bh"))
        vType := consensus.VoteType(rapid.IntRange(1, 2).Draw(rt, "type"))
        height := uint64(rapid.IntRange(1, 1000000).Draw(rt, "height"))
        round := uint32(rapid.IntRange(0, 100).Draw(rt, "round"))

        v := &consensus.Vote{
            Type: vType, Height: height, Round: round,
            BlockHash: bh, ValidatorPK: kp.PublicKey,
        }
        body := consensus.VoteSignBytes(v)
        sig, _ := kp.Sign(body)
        v.Sig = sig

        data, err := v.Serialize()
        if err != nil {
            rt.Fatal(err)
        }
        got, err := consensus.DeserializeVote(data)
        if err != nil {
            rt.Fatal(err)
        }
        if got.Height != height || got.Round != round || got.Type != vType || got.BlockHash != bh {
            rt.Fatalf("round-trip mismatch: got %+v", got)
        }
        if !consensus.VerifyVote(got) {
            rt.Fatal("deserialized vote failed verification")
        }
    })
}
