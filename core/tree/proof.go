package tree

import (
	"crypto/sha256"
	"encoding/binary"

	rgerrors "github.com/imattau/frg/core/errors"
	"github.com/imattau/frg/core/hash"
	"github.com/imattau/frg/core/node"
	"github.com/imattau/frg/core/tx"
)

type ProofStep struct {
	Scale    uint32
	Volume   [32]byte
	Variance [32]byte
	Sig      node.Signature
	Siblings [3][32]byte
	ChildIdx uint8
}

type InclusionProof struct {
	Steps []ProofStep
}

const (
	nodeHdrSize     = 11 + 4 + 32 + 32 + 1 + 2
	proofBufSize    = nodeHdrSize + 128
	atomicProofSize = nodeHdrSize + 32
)

var (
	fastDomain = hash.DomainRGNode
)

func generateStepProof(txs []*tx.Tx, idx int) (*InclusionProof, error) {
	if idx < 0 || idx >= len(txs) {
		return nil, rgerrors.Newf(rgerrors.ErrCanonicalEncodingDistortion, "proof index %d out of bounds [0, %d)", idx, len(txs))
	}

	layer, err := atomicLayer(txs)
	if err != nil {
		return nil, err
	}

	leaf := layer[idx]
	steps := []ProofStep{{
		Scale:    leaf.Scale,
		Volume:   leaf.Volume,
		Variance: leaf.Variance,
		Sig:      leaf.Sig,
	}}

	for len(layer) > 1 {
		chunkIdx := idx / K
		chunkStart := chunkIdx * K
		childPos := uint8(idx - chunkStart)

		var siblings [3][32]byte
		sibIdx := 0
		for j := chunkStart; j < chunkStart+K && j < len(layer); j++ {
			if j == idx {
				continue
			}
			root, err := layer[j].Root()
			if err != nil {
				return nil, err
			}
			siblings[sibIdx] = root
			sibIdx++
		}
		for sibIdx < 3 {
			nn, err := node.NullNode(layer[0].Scale)
			if err != nil {
				return nil, err
			}
			root, err := nn.Root()
			if err != nil {
				return nil, err
			}
			siblings[sibIdx] = root
			sibIdx++
		}

		nextLayer, err := coarsenLayer(layer)
		if err != nil {
			return nil, err
		}
		parent := nextLayer[chunkIdx]

		steps = append(steps, ProofStep{
			Scale:    parent.Scale,
			Volume:   parent.Volume,
			Variance: parent.Variance,
			Sig:      parent.Sig,
			Siblings: siblings,
			ChildIdx: childPos,
		})

		idx = chunkIdx
		layer = nextLayer
	}

	return &InclusionProof{Steps: steps}, nil
}

func GenerateProof(txs []*tx.Tx, idx int) (*InclusionProof, error) {
	return generateStepProof(txs, idx)
}

func (t *Tree) GenerateProof(idx int) (*InclusionProof, error) {
	if idx < 0 || idx >= len(t.layers[0]) {
		return nil, rgerrors.Newf(rgerrors.ErrCanonicalEncodingDistortion, "proof index %d out of bounds [0, %d)", idx, len(t.layers[0]))
	}

	leaf := t.layers[0][idx]
	steps := []ProofStep{{
		Scale:    leaf.Scale,
		Volume:   leaf.Volume,
		Variance: leaf.Variance,
		Sig:      leaf.Sig,
	}}

	for level := 0; level < len(t.layers)-1; level++ {
		layer := t.layers[level]
		chunkIdx := idx / K
		chunkStart := chunkIdx * K
		childPos := uint8(idx - chunkStart)

		var siblings [3][32]byte
		sibIdx := 0
		for j := chunkStart; j < chunkStart+K && j < len(layer); j++ {
			if j == idx {
				continue
			}
			root, err := layer[j].Root()
			if err != nil {
				return nil, err
			}
			siblings[sibIdx] = root
			sibIdx++
		}
		for sibIdx < 3 {
			nn, err := node.NullNode(layer[0].Scale)
			if err != nil {
				return nil, err
			}
			root, err := nn.Root()
			if err != nil {
				return nil, err
			}
			siblings[sibIdx] = root
			sibIdx++
		}

		parent := t.layers[level+1][chunkIdx]
		steps = append(steps, ProofStep{
			Scale:    parent.Scale,
			Volume:   parent.Volume,
			Variance: parent.Variance,
			Sig:      parent.Sig,
			Siblings: siblings,
			ChildIdx: childPos,
		})

		idx = chunkIdx
	}

	return &InclusionProof{Steps: steps}, nil
}

func VerifyProofCanonical(p *InclusionProof, txID [32]byte, expectedRoot [32]byte) bool {
	return verifyProofCanonical(p, txID, expectedRoot)
}

func verifyProofCanonical(p *InclusionProof, txID [32]byte, expectedRoot [32]byte) bool {
	if p == nil || len(p.Steps) == 0 {
		return false
	}

	leaf := p.Steps[0]
	if leaf.Scale != 1 {
		return false
	}

	atomic := &node.RGNode{
		Scale:    1,
		Volume:   leaf.Volume,
		Variance: leaf.Variance,
		Sig:      leaf.Sig,
		Children: [][32]byte{txID},
	}
	current, err := atomic.Root()
	if err != nil {
		return false
	}

	for _, step := range p.Steps[1:] {
		children := make([][32]byte, 4)
		sibIdx := 0
		for j := uint8(0); j < 4; j++ {
			if j == step.ChildIdx {
				children[j] = current
			} else if sibIdx < 3 {
				children[j] = step.Siblings[sibIdx]
				sibIdx++
			}
		}

		parent := &node.RGNode{
			Scale:    step.Scale,
			Volume:   step.Volume,
			Variance: step.Variance,
			Sig:      step.Sig,
			Children: children,
		}
		root, err := parent.Root()
		if err != nil {
			return false
		}
		current = root
	}

	return current == expectedRoot
}

var compactBuf [proofBufSize]byte

func VerifyProof(p *InclusionProof, txID [32]byte, expectedRoot [32]byte) bool {
	if p == nil || len(p.Steps) == 0 {
		return false
	}

	leaf := p.Steps[0]
	if leaf.Scale != 1 {
		return false
	}

	buf := compactBuf[:atomicProofSize]
	copy(buf, fastDomain)
	binary.BigEndian.PutUint32(buf[11:], 1)
	copy(buf[15:], leaf.Volume[:])
	copy(buf[47:], leaf.Variance[:])
	buf[79] = byte(leaf.Sig)
	binary.BigEndian.PutUint16(buf[80:], 1)
	copy(buf[82:], txID[:])
	current := sha256.Sum256(buf)

	for _, step := range p.Steps[1:] {
		buf = compactBuf[:proofBufSize]
		copy(buf, fastDomain)
		binary.BigEndian.PutUint32(buf[11:], step.Scale)
		copy(buf[15:], step.Volume[:])
		copy(buf[47:], step.Variance[:])
		buf[79] = byte(step.Sig)
		binary.BigEndian.PutUint16(buf[80:], 4)

		children := buf[82:]
		for j := uint8(0); j < 4; j++ {
			if j == step.ChildIdx {
				copy(children[j*32:], current[:])
			} else {
				sibIdx := int(j)
				if j > step.ChildIdx {
					sibIdx--
				}
				copy(children[j*32:], step.Siblings[sibIdx][:])
			}
		}

		current = sha256.Sum256(buf)
	}

	return current == expectedRoot
}
