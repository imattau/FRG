package consensus

import (
	"bytes"
	"encoding/binary"
	"fmt"

	"github.com/imattau/frg/core/keys"
	"github.com/imattau/frg/core/statemachine"
)

const maxPersistedVotes = 4096

func encodeRoundState(rs *RoundState) ([]byte, error) {
	var buf bytes.Buffer
	writeU64 := func(v uint64) { _ = binary.Write(&buf, binary.BigEndian, v) }
	writeU32 := func(v uint32) { _ = binary.Write(&buf, binary.BigEndian, v) }
	writeU64(rs.Height)
	writeU32(rs.Round)
	buf.WriteByte(byte(rs.Phase))
	if rs.LockedBlock != nil {
		buf.WriteByte(1)
		buf.Write(rs.LockedBlock[:])
	} else {
		buf.WriteByte(0)
	}
	writeU32(uint32(rs.LockedRound))

	proposal, err := serializeOptionalProposal(rs.Proposal)
	if err != nil {
		return nil, err
	}
	writeU32(uint32(len(proposal)))
	buf.Write(proposal)

	for _, votes := range []map[[32]byte]Vote{rs.Prevotes, rs.Precommits} {
		if len(votes) > maxPersistedVotes {
			return nil, fmt.Errorf("too many votes to persist")
		}
		writeU32(uint32(len(votes)))
		for _, vote := range votes {
			data, err := vote.Serialize()
			if err != nil {
				return nil, err
			}
			buf.Write(data)
		}
	}
	attestation, err := serializeAttestation(&rs.LastAttestation)
	if err != nil {
		return nil, err
	}
	writeU32(uint32(len(attestation)))
	buf.Write(attestation)
	return buf.Bytes(), nil
}

func decodeRoundState(data []byte, chainID string) (*RoundState, error) {
	read := bytes.NewReader(data)
	readU64 := func() (uint64, error) { var v uint64; err := binary.Read(read, binary.BigEndian, &v); return v, err }
	readU32 := func() (uint32, error) { var v uint32; err := binary.Read(read, binary.BigEndian, &v); return v, err }
	height, err := readU64()
	if err != nil {
		return nil, fmt.Errorf("snapshot height: %w", err)
	}
	round, err := readU32()
	if err != nil {
		return nil, fmt.Errorf("snapshot round: %w", err)
	}
	phase, err := read.ReadByte()
	if err != nil || phase < uint8(PhasePropose) || phase > uint8(PhaseCommit) {
		return nil, fmt.Errorf("snapshot phase")
	}
	locked, err := read.ReadByte()
	if err != nil {
		return nil, fmt.Errorf("snapshot lock")
	}
	var lockedHash [32]byte
	if locked != 0 {
		if _, err := read.Read(lockedHash[:]); err != nil {
			return nil, fmt.Errorf("snapshot locked hash: %w", err)
		}
	}
	lockedRound, err := readU32()
	if err != nil {
		return nil, fmt.Errorf("snapshot locked round: %w", err)
	}
	proposalLen, err := readU32()
	if err != nil || proposalLen > uint32(read.Len()) {
		return nil, fmt.Errorf("snapshot proposal length")
	}
	proposalData := make([]byte, proposalLen)
	if _, err := read.Read(proposalData); err != nil {
		return nil, fmt.Errorf("snapshot proposal: %w", err)
	}
	proposal, err := deserializeOptionalProposal(proposalData, chainID)
	if err != nil {
		return nil, err
	}
	rs := NewRoundState(height)
	rs.Round = round
	rs.Phase = Phase(phase)
	if locked != 0 {
		rs.LockedBlock = &lockedHash
		rs.LockedRound = int32(lockedRound)
	}
	rs.Proposal = proposal
	for i := 0; i < 2; i++ {
		count, err := readU32()
		if err != nil || count > maxPersistedVotes || uint64(count)*141 > uint64(read.Len()) {
			return nil, fmt.Errorf("snapshot vote count")
		}
		for j := uint32(0); j < count; j++ {
			data := make([]byte, 141)
			if _, err := read.Read(data); err != nil {
				return nil, fmt.Errorf("snapshot vote: %w", err)
			}
			vote, err := DeserializeVote(data)
			if err != nil {
				return nil, err
			}
			if !VerifyVoteForChain(vote, chainID) {
				return nil, fmt.Errorf("snapshot vote signature")
			}
			if i == 0 {
				rs.Prevotes[vote.ValidatorPK] = *vote
			} else {
				rs.Precommits[vote.ValidatorPK] = *vote
			}
		}
	}
	if read.Len() > 0 {
		attestationLen, err := readU32()
		if err != nil || attestationLen > uint32(read.Len()) {
			return nil, fmt.Errorf("snapshot attestation length")
		}
		attestationData := make([]byte, attestationLen)
		if attestationLen > 0 {
			if _, err := read.Read(attestationData); err != nil {
				return nil, fmt.Errorf("snapshot attestation: %w", err)
			}
		}
		attestation, err := deserializeAttestation(attestationData, chainID)
		if err != nil {
			return nil, err
		}
		rs.LastAttestation = *attestation
	}
	if read.Len() != 0 {
		return nil, fmt.Errorf("snapshot trailing bytes")
	}
	return rs, nil
}

func serializeAttestation(attestation *AttestationSet) ([]byte, error) {
	if attestation == nil || len(attestation.Votes) == 0 {
		return nil, nil
	}
	if len(attestation.Votes) > maxPersistedVotes {
		return nil, fmt.Errorf("too many attestation votes")
	}
	var buf bytes.Buffer
	_ = binary.Write(&buf, binary.BigEndian, attestation.Height)
	_ = binary.Write(&buf, binary.BigEndian, attestation.Round)
	buf.Write(attestation.BlockHash[:])
	_ = binary.Write(&buf, binary.BigEndian, uint32(len(attestation.Votes)))
	for _, vote := range attestation.Votes {
		data, err := vote.Serialize()
		if err != nil {
			return nil, err
		}
		buf.Write(data)
	}
	return buf.Bytes(), nil
}

func deserializeAttestation(data []byte, chainID string) (*AttestationSet, error) {
	if len(data) == 0 {
		return &AttestationSet{}, nil
	}
	read := bytes.NewReader(data)
	var height uint64
	var round uint32
	if err := binary.Read(read, binary.BigEndian, &height); err != nil {
		return nil, fmt.Errorf("attestation height: %w", err)
	}
	if err := binary.Read(read, binary.BigEndian, &round); err != nil {
		return nil, fmt.Errorf("attestation round: %w", err)
	}
	var blockHash [32]byte
	if _, err := read.Read(blockHash[:]); err != nil {
		return nil, fmt.Errorf("attestation block hash: %w", err)
	}
	var count uint32
	if err := binary.Read(read, binary.BigEndian, &count); err != nil || count > maxPersistedVotes || uint64(count)*141 > uint64(read.Len()) {
		return nil, fmt.Errorf("attestation vote count")
	}
	attestation := &AttestationSet{Height: height, Round: round, BlockHash: blockHash, Votes: make([]Vote, 0, count)}
	for i := uint32(0); i < count; i++ {
		data := make([]byte, 141)
		if _, err := read.Read(data); err != nil {
			return nil, fmt.Errorf("attestation vote: %w", err)
		}
		vote, err := DeserializeVote(data)
		if err != nil || !VerifyVoteForChain(vote, chainID) {
			return nil, fmt.Errorf("attestation vote signature")
		}
		if vote.Type != VotePrecommit || vote.Height != height || vote.Round != round || vote.BlockHash != blockHash {
			return nil, fmt.Errorf("attestation vote context")
		}
		attestation.Votes = append(attestation.Votes, *vote)
	}
	if read.Len() != 0 {
		return nil, fmt.Errorf("attestation trailing bytes")
	}
	return attestation, nil
}

func serializeOptionalProposal(p *BlockProposal) ([]byte, error) {
	if p == nil {
		return nil, nil
	}
	return SerializeProposal(p)
}

func deserializeOptionalProposal(data []byte, chainID string) (*BlockProposal, error) {
	if len(data) == 0 {
		return nil, nil
	}
	p, err := DeserializeProposal(data)
	if err != nil {
		return nil, err
	}
	if !keys.Verify(p.ProposerPK, ProposalSignBytesForChain(p, chainID), p.ProposerSig) {
		return nil, fmt.Errorf("snapshot proposal signature")
	}
	for _, t := range p.Txs {
		if err := t.VerifySigsForChain(chainID); err != nil {
			return nil, err
		}
	}
	return p, nil
}

func persistRoundState(sm *statemachine.StateMachine, rs *RoundState) {
	data, err := encodeRoundState(rs)
	if err == nil {
		_ = sm.SaveConsensusState(data)
	}
}
