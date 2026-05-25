package validator

import (
	rgerrors "github.com/imattau/frg/core/errors"
	"github.com/imattau/frg/core/tree"
)

// Validate recomputes the state root for b and verifies it matches claimedRoot.
// Returns the recomputed root and nil on success.
// Returns [32]byte{} and *RGError on any signature fault (ERR_012),
// pipeline fault (ERR_001-ERR_010)
// or root mismatch (ERR_011).
func Validate(b *tree.Block, claimedRoot [32]byte) ([32]byte, error) {
	for _, tx := range b.Txs {
		if err := tx.VerifySigs(); err != nil {
			return [32]byte{}, err
		}
	}
	computed, err := b.BuildRoot()
	if err != nil {
		return [32]byte{}, err
	}
	if computed != claimedRoot {
		return [32]byte{}, rgerrors.Newf(rgerrors.ErrRootMismatch, "recomputed %x, claimed %x", computed, claimedRoot)
	}
	return computed, nil
}
