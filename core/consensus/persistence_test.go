package consensus

import (
	"testing"

	"github.com/imattau/frg/core/keys"
)

func TestRoundStateSnapshotRoundTrip(t *testing.T) {
	chainID := "snapshot-chain"
	kp := keys.NewKeypairFromSeed([32]byte{7})
	vote := Vote{Type: VotePrevote, Height: 4, Round: 2, BlockHash: [32]byte{1}, ValidatorPK: kp.PublicKey}
	sig, err := kp.Sign(VoteSignBytesForChain(&vote, chainID))
	if err != nil {
		t.Fatal(err)
	}
	vote.Sig = sig
	rs := NewRoundState(4)
	rs.Round = 2
	rs.Phase = PhasePrevote
	rs.Prevotes[kp.PublicKey] = vote
	locked := [32]byte{2}
	rs.LockedBlock = &locked
	rs.LockedRound = 1

	raw, err := encodeRoundState(rs)
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeRoundState(raw, chainID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Height != rs.Height || got.Round != rs.Round || got.Phase != rs.Phase || got.LockedBlock == nil || *got.LockedBlock != locked {
		t.Fatalf("round state mismatch: got %+v", got)
	}
	if got.Prevotes[kp.PublicKey].BlockHash != vote.BlockHash {
		t.Fatal("vote was not restored")
	}
}
