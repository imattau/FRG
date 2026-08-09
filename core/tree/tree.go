package tree

import (
	"math/big"

	rgerrors "github.com/imattau/frg/core/errors"
	"github.com/imattau/frg/core/hash"
	"github.com/imattau/frg/core/node"
	"github.com/imattau/frg/core/tx"
)

const (
	K    = 4
	TMax = 65536
)

type Block struct {
	Height uint64
	Txs    []*tx.Tx
}

func (b *Block) BuildRoot() ([32]byte, error) {
	if len(b.Txs) == 0 {
		return node.EmptyBlockRoot(), nil
	}
	if len(b.Txs) > TMax {
		return [32]byte{}, rgerrors.Newf(rgerrors.ErrDosSizeExceeded, "block has %d txs, max %d", len(b.Txs), TMax)
	}

	layer, err := atomicLayer(b.Txs)
	if err != nil {
		return [32]byte{}, err
	}

	for len(layer) > 1 {
		layer, err = coarsenLayer(layer)
		if err != nil {
			return [32]byte{}, err
		}
	}

	return layer[0].Root()
}

func atomicLayer(txs []*tx.Tx) ([]*node.RGNode, error) {
	nodes := make([]*node.RGNode, len(txs))
	for i, t := range txs {
		txID, err := t.ID()
		if err != nil {
			return nil, err
		}

		value := new(big.Int).Set(t.Value)
		sumSquares := new(big.Int).Mul(value, value)
		if sumSquares.Sign() < 0 || sumSquares.Cmp(hash.UINT256_MAX) > 0 {
			return nil, rgerrors.New(rgerrors.ErrArithmeticOverflow, "tx square exceeds uint256")
		}

		nodes[i] = &node.RGNode{
			Scale:      1,
			Volume:     value,
			Variance:   big.NewInt(0),
			Sig:        node.SigAtomic,
			Children:   [][32]byte{txID},
			SumValues:  value,
			SumSquares: sumSquares,
			Count:      1,
		}
		if t.Type == tx.TxTypeContractDeploy || t.Type == tx.TxTypeContractCall {
			nodes[i].ContractCount = 1
		}
	}
	return nodes, nil
}

func coarsenLayer(layer []*node.RGNode) ([]*node.RGNode, error) {
	parentScale := layer[0].Scale * K
	if !hash.ValidScale(parentScale) {
		return nil, rgerrors.Newf(rgerrors.ErrScaleDomainFault, "invalid parent scale %d", parentScale)
	}

	parents := make([]*node.RGNode, 0, (len(layer)+K-1)/K)
	for i := 0; i < len(layer); i += K {
		end := i + K
		if end > len(layer) {
			end = len(layer)
		}

		chunk := make([]*node.RGNode, 0, K)
		chunk = append(chunk, layer[i:end]...)

		needPad := K - len(chunk)
		paddingMask := uint8(0)
		for p := 0; p < needPad; p++ {
			slot := len(chunk) + p
			paddingMask |= 1 << slot
		}
		if paddingMask >= 1<<K {
			return nil, rgerrors.Newf(rgerrors.ErrMaskOutOfBounds, "padding_mask %d >= %d", paddingMask, 1<<K)
		}

		for p := 0; p < needPad; p++ {
			nullNode, err := node.NullNode(layer[0].Scale)
			if err != nil {
				return nil, err
			}
			chunk = append(chunk, nullNode)
		}

		for slot := 0; slot < K; slot++ {
			if (paddingMask>>slot)&1 == 0 {
				continue
			}
			expectedNull, err := node.NullNode(layer[0].Scale)
			if err != nil {
				return nil, err
			}
			expectedRoot, err := expectedNull.Root()
			if err != nil {
				return nil, err
			}
			actualRoot, err := chunk[slot].Root()
			if err != nil {
				return nil, err
			}
			if actualRoot != expectedRoot {
				return nil, rgerrors.New(rgerrors.ErrPaddingSubstitutionFraud, "masked child is not canonical NULL_Λ")
			}
		}

		parent, err := coarseGrain(chunk, parentScale)
		if err != nil {
			return nil, err
		}
		parents = append(parents, parent)
	}

	return parents, nil
}

func coarseGrain(chunk []*node.RGNode, parentScale uint32) (*node.RGNode, error) {
	sumValues := big.NewInt(0)
	sumSquares := big.NewInt(0)
	count := uint64(0)
	contractCount := uint64(0)
	children := make([][32]byte, K)

	for i, child := range chunk {
		childRoot, err := child.Root()
		if err != nil {
			return nil, err
		}
		children[i] = childRoot

		addBigInt(sumValues, child.SumValues)
		if sumValues.Sign() < 0 || sumValues.Cmp(hash.UINT256_MAX) > 0 {
			return nil, rgerrors.New(rgerrors.ErrArithmeticOverflow, "sumValues overflow")
		}

		addBigInt(sumSquares, child.SumSquares)
		if sumSquares.Sign() < 0 || sumSquares.Cmp(hash.UINT256_MAX) > 0 {
			return nil, rgerrors.New(rgerrors.ErrArithmeticOverflow, "sumSquares overflow")
		}

		count += child.Count
		contractCount += child.ContractCount
	}

	volume := new(big.Int).Set(sumValues)
	variance := big.NewInt(0)
	if count > 0 {
		n := new(big.Int).SetUint64(count)
		// Population variance = E[x²] - E[x]²
		// sumSquares is in SCALE² units (value*value), sumValues in SCALE units.
		// secondMoment = sumSquares / count  (SCALE² units)
		// mean         = sumValues / count   (SCALE units)
		// variance     = secondMoment - mean²  (SCALE² units, consistent)
		secondMoment := new(big.Int).Div(new(big.Int).Set(sumSquares), n)
		mean := new(big.Int).Div(new(big.Int).Set(sumValues), n)
		meanSquared := new(big.Int).Mul(mean, mean)
		variance.Sub(secondMoment, meanSquared)
		if variance.Sign() < 0 {
			variance.SetInt64(0)
		}
	}

	parent := &node.RGNode{
		Scale:         parentScale,
		Volume:        volume,
		Variance:      variance,
		Children:      children,
		SumValues:     new(big.Int).Set(sumValues),
		SumSquares:    new(big.Int).Set(sumSquares),
		Count:         count,
		ContractCount: contractCount,
	}
	parent.Sig = node.DeriveSignature(parent)

	if _, err := parent.RecomputeSig(); err != nil {
		return nil, err
	}

	return parent, nil
}

func addBigInt(dst *big.Int, src *big.Int) {
	if src == nil {
		return
	}
	dst.Add(dst, src)
}
