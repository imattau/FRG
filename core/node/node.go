package node

import (
	"encoding/binary"
	"math/big"

	rgerrors "github.com/imattau/frg/core/errors"
	"github.com/imattau/frg/core/hash"
)

type Signature uint8

const (
	SigAtomic        Signature = 1
	SigNullPad       Signature = 2
	SigStagnantState Signature = 3
	SigLaminarFlow   Signature = 4
	SigVolatileShock Signature = 5
)

var (
	nullPadChildRoot    = hash.Hash([]byte(hash.DomainNullPad))
	emptyBlockChildRoot = hash.Hash([]byte(hash.DomainEmptyBlock))
)

type RGNode struct {
	Scale    uint32
	Volume   *big.Int
	Variance *big.Int
	Sig      Signature
	Children [][32]byte

	SumValues  *big.Int
	SumSquares *big.Int
	Count      uint64
}

func (n *RGNode) Serialize() ([]byte, error) {
	if !hash.ValidScale(n.Scale) {
		return nil, rgerrors.Newf(rgerrors.ErrScaleDomainFault, "invalid scale %d", n.Scale)
	}

	expectedChildren := 4
	if n.Scale == 1 {
		expectedChildren = 1
	}
	if len(n.Children) != expectedChildren {
		return nil, rgerrors.Newf(rgerrors.ErrInvalidChildArity, "expected %d children, got %d", expectedChildren, len(n.Children))
	}

	if n.Volume == nil || n.Volume.Sign() < 0 || n.Volume.Cmp(hash.UINT256_MAX) > 0 {
		return nil, rgerrors.New(rgerrors.ErrArithmeticOverflow, "volume out of uint256 range")
	}
	if n.Variance == nil || n.Variance.Sign() < 0 || n.Variance.Cmp(hash.UINT256_MAX) > 0 {
		return nil, rgerrors.New(rgerrors.ErrArithmeticOverflow, "variance out of uint256 range")
	}

	size := 11 + 4 + 32 + 32 + 1 + 2 + len(n.Children)*32
	buf := make([]byte, size)
	pos := 0

	pos += copy(buf[pos:], hash.DomainRGNode)

	binary.BigEndian.PutUint32(buf[pos:], n.Scale)
	pos += 4

	volumeBytes := n.Volume.Bytes()
	pos += 32 - len(volumeBytes)
	copy(buf[pos:], volumeBytes)
	pos += len(volumeBytes)

	varianceBytes := n.Variance.Bytes()
	pos += 32 - len(varianceBytes)
	copy(buf[pos:], varianceBytes)
	pos += len(varianceBytes)

	buf[pos] = byte(n.Sig)
	pos++

	binary.BigEndian.PutUint16(buf[pos:], uint16(len(n.Children)))
	pos += 2

	for _, child := range n.Children {
		copy(buf[pos:], child[:])
		pos += 32
	}

	return buf, nil
}

func (n *RGNode) Root() ([32]byte, error) {
	serialized, err := n.Serialize()
	if err != nil {
		return [32]byte{}, err
	}
	return hash.Hash(serialized), nil
}

func (n *RGNode) RecomputeSig() (Signature, error) {
	computed := deriveSignature(n)
	if computed != n.Sig {
		return 0, rgerrors.Newf(rgerrors.ErrSignatureMisrepresentation, "declared sig %d, recomputed %d", n.Sig, computed)
	}
	return computed, nil
}

func NullNode(scale uint32) (*RGNode, error) {
	if !hash.ValidScale(scale) {
		return nil, rgerrors.Newf(rgerrors.ErrScaleDomainFault, "invalid scale %d", scale)
	}

	childCount := 1
	if scale > 1 {
		childCount = 4
	}
	children := make([][32]byte, childCount)
	for i := range children {
		children[i] = nullPadChildRoot
	}

	return &RGNode{
		Scale:      scale,
		Volume:     big.NewInt(0),
		Variance:   big.NewInt(0),
		Sig:        SigNullPad,
		Children:   children,
		SumValues:  big.NewInt(0),
		SumSquares: big.NewInt(0),
		Count:      0,
	}, nil
}

func EmptyBlockRoot() [32]byte {
	node := &RGNode{
		Scale:    1,
		Volume:   big.NewInt(0),
		Variance: big.NewInt(0),
		Sig:      SigStagnantState,
		Children: [][32]byte{emptyBlockChildRoot},
	}
	root, err := node.Root()
	if err != nil {
		return [32]byte{}
	}
	return root
}

func deriveSignature(n *RGNode) Signature {
	if n == nil {
		return 0
	}

	if isNullPadNode(n) {
		return SigNullPad
	}

	if n.Scale == 1 {
		if isEmptyBlockNode(n) {
			return SigStagnantState
		}
		return SigAtomic
	}

	zero := big.NewInt(0)
	if n.Volume != nil && n.Volume.Cmp(zero) == 0 && n.Variance != nil && n.Variance.Cmp(zero) == 0 {
		return SigStagnantState
	}
	if n.Variance != nil && n.Variance.Cmp(zero) == 0 {
		return SigLaminarFlow
	}
	if n.Volume != nil && n.Variance != nil && n.Variance.Cmp(n.Volume) > 0 {
		return SigVolatileShock
	}
	return SigLaminarFlow
}

func isNullPadNode(n *RGNode) bool {
	if n == nil {
		return false
	}
	if len(n.Children) == 0 {
		return false
	}
	for _, child := range n.Children {
		if child != nullPadChildRoot {
			return false
		}
	}
	return n.Volume != nil && n.Volume.Sign() == 0 && n.Variance != nil && n.Variance.Sign() == 0
}

func isEmptyBlockNode(n *RGNode) bool {
	if n == nil || len(n.Children) != 1 {
		return false
	}
	return n.Children[0] == emptyBlockChildRoot && n.Volume != nil && n.Volume.Sign() == 0 && n.Variance != nil && n.Variance.Sign() == 0
}
