package leader

import (
	"bytes"
	"encoding/binary"
	"sort"

	rgerrors "github.com/imattau/frg/core/errors"
	"github.com/imattau/frg/core/hash"
)

// ElectedProposer returns the public key of the block proposer for the given height.
// prevStateRoot is the 32-byte state root of the previous block.
// blockHeight is the current block height.
// validators is the BONDED validator set (unsorted; sorted internally).
// Returns ERR_020 if validators is empty.
func ElectedProposer(prevStateRoot [32]byte, blockHeight uint64, validators [][32]byte) ([32]byte, error) {
	return SkipProposer(prevStateRoot, blockHeight, validators, 0)
}

// SkipProposer returns the proposer after skipCount misses.
// skipCount=0 returns the same result as ElectedProposer.
// skipCount=1 is the first skip (original proposer absent).
// Returns ERR_020 if validators is empty.
func SkipProposer(prevStateRoot [32]byte, blockHeight uint64, validators [][32]byte, skipCount uint32) ([32]byte, error) {
	if len(validators) == 0 {
		return [32]byte{}, rgerrors.New(rgerrors.ErrEmptyValidatorSet, "validator set is empty")
	}

	sorted := make([][32]byte, len(validators))
	copy(sorted, validators)
	sort.Slice(sorted, func(i, j int) bool {
		return bytes.Compare(sorted[i][:], sorted[j][:]) < 0
	})

	seed := computeSeed(prevStateRoot, blockHeight)
	base := binary.BigEndian.Uint64(seed[:8])
	n := uint64(len(sorted))
	idx := (base%n + uint64(skipCount)) % n

	return sorted[idx], nil
}

func computeSeed(prevStateRoot [32]byte, blockHeight uint64) [32]byte {
	var heightBytes [8]byte
	binary.BigEndian.PutUint64(heightBytes[:], blockHeight)

	input := make([]byte, 32+8)
	copy(input[:32], prevStateRoot[:])
	copy(input[32:], heightBytes[:])
	return hash.Hash(input)
}
