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
	SigContract      Signature = 6
)

var (
	nullPadChildRoot    = hash.Hash([]byte(hash.DomainNullPad))
	emptyBlockChildRoot = hash.Hash([]byte(hash.DomainEmptyBlock))
)

type RGNode struct {
	Scale    uint32
	Volume   [32]byte
	Variance [32]byte
	Sig      Signature
	Children [][32]byte

	SumSquares    [32]byte
	Count         uint64
	ContractCount uint64
}

func Uint256ToBytes(v *big.Int) [32]byte {
	if v == nil {
		return [32]byte{}
	}
	var b [32]byte
	vb := v.Bytes()
	copy(b[32-len(vb):], vb)
	return b
}

func BytesToUint256(b [32]byte) *big.Int {
	return new(big.Int).SetBytes(b[:])
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

	v := BytesToUint256(n.Volume)
	if v.Sign() < 0 || v.Cmp(hash.UINT256_MAX) > 0 {
		return nil, rgerrors.New(rgerrors.ErrArithmeticOverflow, "volume out of uint256 range")
	}
	vr := BytesToUint256(n.Variance)
	if vr.Sign() < 0 || vr.Cmp(hash.UINT256_MAX) > 0 {
		return nil, rgerrors.New(rgerrors.ErrArithmeticOverflow, "variance out of uint256 range")
	}

	size := 11 + 4 + 32 + 32 + 1 + 2 + len(n.Children)*32
	buf := make([]byte, size)
	pos := 0

	pos += copy(buf[pos:], hash.DomainRGNode)

	binary.BigEndian.PutUint32(buf[pos:], n.Scale)
	pos += 4

	copy(buf[pos:], n.Volume[:])
	pos += 32

	copy(buf[pos:], n.Variance[:])
	pos += 32

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

	if scale == 1 {
		return &RGNode{
			Scale:    1,
			Sig:      SigNullPad,
			Children: [][32]byte{nullPadChildRoot},
		}, nil
	}

	childNull, err := NullNode(scale / 4)
	if err != nil {
		return nil, err
	}
	childRoot, err := childNull.Root()
	if err != nil {
		return nil, err
	}
	children := make([][32]byte, 4)
	for i := range children {
		children[i] = childRoot
	}

	return &RGNode{
		Scale:    scale,
		Sig:      SigNullPad,
		Children: children,
	}, nil
}

func EmptyBlockRoot() [32]byte {
	node := &RGNode{
		Scale:    1,
		Sig:      SigStagnantState,
		Children: [][32]byte{emptyBlockChildRoot},
	}
	root, err := node.Root()
	if err != nil {
		return [32]byte{}
	}
	return root
}

func DeriveSignature(n *RGNode) Signature {
	return deriveSignature(n)
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

	volume := BytesToUint256(n.Volume)
	variance := BytesToUint256(n.Variance)

	zero := big.NewInt(0)
	if volume.Cmp(zero) == 0 && variance.Cmp(zero) == 0 {
		return SigStagnantState
	}

	if n.ContractCount == n.Count && n.Count > 0 && volume.Cmp(zero) > 0 {
		return SigContract
	}

	if variance.Cmp(zero) == 0 {
		return SigLaminarFlow
	}

	if n.Count > 0 {
		count := new(big.Int).SetUint64(n.Count)
		mean := new(big.Int).Div(volume, count)
		threshold := new(big.Int).Mul(mean, mean)
		threshold.Mul(threshold, big.NewInt(4))
		if variance.Cmp(threshold) > 0 {
			return SigVolatileShock
		}
	}
	return SigLaminarFlow
}

func isNullPadNode(n *RGNode) bool {
	if n == nil || len(n.Children) == 0 {
		return false
	}
	if BytesToUint256(n.Volume).Sign() != 0 {
		return false
	}
	if BytesToUint256(n.Variance).Sign() != 0 {
		return false
	}
	if n.Count != 0 {
		return false
	}
	if n.Scale == 1 {
		return len(n.Children) == 1 && n.Children[0] == nullPadChildRoot
	}
	childNull, err := NullNode(n.Scale / 4)
	if err != nil {
		return false
	}
	childRoot, err := childNull.Root()
	if err != nil {
		return false
	}
	for _, ch := range n.Children {
		if ch != childRoot {
			return false
		}
	}
	return true
}

func isEmptyBlockNode(n *RGNode) bool {
	if n == nil || len(n.Children) != 1 {
		return false
	}
	return n.Children[0] == emptyBlockChildRoot && BytesToUint256(n.Volume).Sign() == 0 && BytesToUint256(n.Variance).Sign() == 0
}
