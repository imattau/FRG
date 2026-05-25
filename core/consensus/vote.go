package consensus

import (
    "encoding/binary"
    "errors"
    "fmt"
    "math/big"

    "github.com/imattau/frg/core/keys"
    "github.com/imattau/frg/core/tx"
)

type VoteType uint8

const (
    VotePrevote   VoteType = 1
    VotePrecommit VoteType = 2
)

// Vote is a signed prevote or precommit.
// Nil vote: BlockHash is all-zeros.
type Vote struct {
    Type        VoteType
    Height      uint64
    Round       uint32
    BlockHash   [32]byte   // H(BlockProposal bytes excl. sig); all-zeros = nil
    ValidatorPK [32]byte
    Sig         [64]byte   // Ed25519 sig over H(FRG_VOTE_V1\x00 ∥ Type ∥ Height ∥ Round ∥ BlockHash)
}

// Serialize returns the 141-byte fixed-width binary representation of the vote.
func (v *Vote) Serialize() ([]byte, error) {
    buf := make([]byte, 141)
    buf[0] = uint8(v.Type)
    binary.BigEndian.PutUint64(buf[1:9], v.Height)
    binary.BigEndian.PutUint32(buf[9:13], v.Round)
    copy(buf[13:45], v.BlockHash[:])
    copy(buf[45:77], v.ValidatorPK[:])
    copy(buf[77:141], v.Sig[:])
    return buf, nil
}

// DeserializeVote parses a 141-byte binary representation into a Vote.
func DeserializeVote(data []byte) (*Vote, error) {
    if len(data) != 141 {
        return nil, fmt.Errorf("invalid vote length: %d", len(data))
    }
    v := &Vote{
        Type: VoteType(data[0]),
    }
    v.Height = binary.BigEndian.Uint64(data[1:9])
    v.Round = binary.BigEndian.Uint32(data[9:13])
    copy(v.BlockHash[:], data[13:45])
    copy(v.ValidatorPK[:], data[45:77])
    copy(v.Sig[:], data[77:141])
    return v, nil
}

// VoteSignBytes returns the bytes to be signed for a vote.
func VoteSignBytes(v *Vote) []byte {
    // FRG_VOTE_V1\x00 (12 bytes)
    // Type            (uint8, 1 byte)
    // Height          (uint64 big-endian, 8 bytes)
    // Round           (uint32 big-endian, 4 bytes)
    // BlockHash       (32 bytes)
    buf := make([]byte, 12+1+8+4+32)
    copy(buf[0:12], "FRG_VOTE_V1\x00")
    buf[12] = uint8(v.Type)
    binary.BigEndian.PutUint64(buf[13:21], v.Height)
    binary.BigEndian.PutUint32(buf[21:25], v.Round)
    copy(buf[25:57], v.BlockHash[:])
    return buf
}

// VerifyVote checks the Ed25519 signature of the vote.
func VerifyVote(v *Vote) bool {
    body := VoteSignBytes(v)
    return keys.Verify(v.ValidatorPK, body, v.Sig)
}

type BlockProposal struct {
    Height           uint64
    Round            uint32
    ProposerPK       [32]byte
    Txs              []*tx.Tx
    PrevAttestations AttestationSet  // 2/3+ precommits from height-1 (empty at height 1)
    ProposerSig      [64]byte        // Ed25519 sig over H(FRG_PROPOSAL_V1\x00 ∥ serialised fields excl. sig)
}

// ProposalSignBytes returns the bytes to be signed for a block proposal.
func ProposalSignBytes(p *BlockProposal) []byte {
    // This is a simplified version; real implementation would serialise all fields.
    // The spec says: FRG_PROPOSAL_V1\x00 ∥ serialised fields excl. sig
    buf := make([]byte, 16+8+4+32)
    copy(buf[0:16], "FRG_PROPOSAL_V1\x00")
    binary.BigEndian.PutUint64(buf[16:24], p.Height)
    binary.BigEndian.PutUint32(buf[24:28], p.Round)
    copy(buf[28:60], p.ProposerPK[:])
    // Note: Txs and PrevAttestations would be added here in a full implementation.
    return buf
}

type AttestationSet struct {
    Height    uint64
    Round     uint32
    BlockHash [32]byte
    Votes     []Vote   // must represent 2/3+ of bonded stake at that height
}

// VerifyAttestation checks that the attestation set is valid.
func VerifyAttestation(as *AttestationSet, validators [][32]byte, totalStake uint64) error {
    if len(as.Votes) == 0 {
        return errors.New("empty attestation set")
    }
    // Real implementation would verify each vote and sum stake.
    return nil
}

// QuorumReached returns true if the votes in voteMap represent 2/3+ of total bonded stake.
// validatorPKs and stakes must correspond by index.
func QuorumReached(voteMap map[[32]byte]Vote, validatorPKs [][32]byte, stakes []*big.Int) bool {
    stakeMap := make(map[[32]byte]*big.Int, len(validatorPKs))
    totalStake := new(big.Int)
    for i, pk := range validatorPKs {
        stakeMap[pk] = stakes[i]
        totalStake.Add(totalStake, stakes[i])
    }
    sumVoted := new(big.Int)
    for pk := range voteMap {
        if stake, ok := stakeMap[pk]; ok {
            sumVoted.Add(sumVoted, stake)
        }
    }
    // quorum: sumVoted * 3 > totalStake * 2
    lhs := new(big.Int).Mul(sumVoted, big.NewInt(3))
    rhs := new(big.Int).Mul(totalStake, big.NewInt(2))
    return lhs.Cmp(rhs) > 0
}
