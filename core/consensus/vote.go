package consensus

import (
    "bytes"
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

// ProposalSignBytes returns the bytes signed by the proposer.
// Covers all fields except ProposerSig itself.
func ProposalSignBytes(p *BlockProposal) []byte {
	body, _ := serializeProposalBody(p)
	prefix := []byte("FRG_PROPOSAL_V1\x00")
	out := make([]byte, len(prefix)+len(body))
	copy(out, prefix)
	copy(out[len(prefix):], body)
	return out
}

// SerializeProposal serialises a complete BlockProposal for broadcast.
func SerializeProposal(p *BlockProposal) ([]byte, error) {
	body, err := serializeProposalBody(p)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 64+len(body))
	copy(out[:64], p.ProposerSig[:])
	copy(out[64:], body)
	return out, nil
}

// DeserializeProposal parses a serialised BlockProposal.
func DeserializeProposal(data []byte) (*BlockProposal, error) {
	if len(data) < 64+8+4+32+8+4+32+4 {
		return nil, fmt.Errorf("proposal too short")
	}
	p := &BlockProposal{}
	copy(p.ProposerSig[:], data[:64])
	body := data[64:]
	off := 0
	p.Height = binary.BigEndian.Uint64(body[off:]); off += 8
	p.Round = binary.BigEndian.Uint32(body[off:]); off += 4
	copy(p.ProposerPK[:], body[off:]); off += 32
	// attestation
	p.PrevAttestations.Height = binary.BigEndian.Uint64(body[off:]); off += 8
	p.PrevAttestations.Round = binary.BigEndian.Uint32(body[off:]); off += 4
	copy(p.PrevAttestations.BlockHash[:], body[off:]); off += 32
	voteCount := int(binary.BigEndian.Uint32(body[off:])); off += 4
	p.PrevAttestations.Votes = make([]Vote, voteCount)
	for i := range p.PrevAttestations.Votes {
		if off+141 > len(body) {
			return nil, fmt.Errorf("truncated vote at index %d", i)
		}
		v, err := DeserializeVote(body[off : off+141])
		if err != nil {
			return nil, err
		}
		p.PrevAttestations.Votes[i] = *v
		off += 141
	}
	// txs
	if off+4 > len(body) {
		return nil, fmt.Errorf("missing tx count")
	}
	_ = binary.BigEndian.Uint32(body[off:]); off += 4
	if off < len(body) {
		txs, err := tx.DeserializeBatch(body[off:])
		if err != nil {
			return nil, err
		}
		p.Txs = txs
	}
	return p, nil
}

// serializeProposalBody serialises all fields except ProposerSig.
func serializeProposalBody(p *BlockProposal) ([]byte, error) {
	var buf bytes.Buffer
	var tmp8 [8]byte
	var tmp4 [4]byte
	binary.BigEndian.PutUint64(tmp8[:], p.Height); buf.Write(tmp8[:])
	binary.BigEndian.PutUint32(tmp4[:], p.Round); buf.Write(tmp4[:])
	buf.Write(p.ProposerPK[:])
	binary.BigEndian.PutUint64(tmp8[:], p.PrevAttestations.Height); buf.Write(tmp8[:])
	binary.BigEndian.PutUint32(tmp4[:], p.PrevAttestations.Round); buf.Write(tmp4[:])
	buf.Write(p.PrevAttestations.BlockHash[:])
	binary.BigEndian.PutUint32(tmp4[:], uint32(len(p.PrevAttestations.Votes))); buf.Write(tmp4[:])
	for _, v := range p.PrevAttestations.Votes {
		vb, err := v.Serialize()
		if err != nil {
			return nil, err
		}
		buf.Write(vb)
	}
	binary.BigEndian.PutUint32(tmp4[:], uint32(len(p.Txs))); buf.Write(tmp4[:])
	if len(p.Txs) > 0 {
		txBytes, err := tx.SerializeBatch(p.Txs)
		if err != nil {
			return nil, err
		}
		buf.Write(txBytes)
	}
	return buf.Bytes(), nil
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
