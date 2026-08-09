package tree

import (
	"math/big"
	"sort"

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
	return BuildTreeRoot(b.Txs, nil)
}

// AtomicLayer creates the atomic (scale=1) node layer from transactions.
// Exported for benchmarking and proof generation.
func AtomicLayer(txs []*tx.Tx) ([]*node.RGNode, error) {
	return atomicLayer(txs)
}

// CoarsenLayer applies one level of K-ary coarse-graining to a node layer.
// Exported for benchmarking and proof generation.
func CoarsenLayer(layer []*node.RGNode) ([]*node.RGNode, error) {
	return coarsenLayer(layer)
}

// LeafUpdate describes a single leaf-level change for incremental tree mutation.
type LeafUpdate struct {
	Index int
	Tx    *tx.Tx
}

// Tree is a retained RG state tree that supports incremental leaf mutation.
// Unlike BuildTreeRoot, which discards intermediate nodes after computing the root,
// Tree retains all coarsening layers so that incremental updates can recompute
// only the affected path from leaf to root.
type Tree struct {
	layers [][]*node.RGNode
	root   [32]byte
	dirty  bool
}

// BuildTreeRoot constructs the RG state root from transactions plus optional contract state nodes.
// contractNodes are appended to the atomic layer before coarse-graining.
func BuildTreeRoot(txs []*tx.Tx, contractNodes []*node.RGNode) ([32]byte, error) {
	t, err := BuildTree(txs, contractNodes)
	if err != nil {
		return [32]byte{}, err
	}
	return t.Root(), nil
}

// BuildTree constructs a retained RG tree from transactions and optional contract nodes.
func BuildTree(txs []*tx.Tx, contractNodes []*node.RGNode) (*Tree, error) {
	if len(txs) == 0 && len(contractNodes) == 0 {
		return &Tree{layers: [][]*node.RGNode{{}}, root: node.EmptyBlockRoot()}, nil
	}
	if len(txs) > TMax {
		return nil, rgerrors.Newf(rgerrors.ErrDosSizeExceeded, "block has %d txs, max %d", len(txs), TMax)
	}

	layer := make([]*node.RGNode, 0, len(txs)+len(contractNodes))
	if len(txs) > 0 {
		txLayer, err := atomicLayer(txs)
		if err != nil {
			return nil, err
		}
		layer = append(layer, txLayer...)
	}
	layer = append(layer, contractNodes...)

	layers := make([][]*node.RGNode, 0, 10)
	layers = append(layers, layer)

	current := layer
	for len(current) > 1 {
		var err error
		current, err = coarsenLayer(current)
		if err != nil {
			return nil, err
		}
		layers = append(layers, current)
	}

	root, err := current[0].Root()
	if err != nil {
		return nil, err
	}

	return &Tree{layers: layers, root: root}, nil
}

// Root returns the current state root, recomputing if the tree is dirty.
func (t *Tree) Root() [32]byte {
	if t.dirty {
		root, err := t.layers[len(t.layers)-1][0].Root()
		if err != nil {
			return t.root
		}
		t.root = root
		t.dirty = false
	}
	return t.root
}

// UpdateLeaf replaces one leaf transaction and recomputes only the affected
// path through the hierarchy. The index must refer to an atomic node derived
// from a transaction (SigAtomic), not a contract node.
func (t *Tree) UpdateLeaf(index int, newTx *tx.Tx) error {
	return t.UpdateLeaves([]LeafUpdate{{Index: index, Tx: newTx}})
}

// UpdateLeaves replaces multiple leaf transactions in one pass, merging
// overlapping ancestor paths to avoid redundant recomputation.
func (t *Tree) UpdateLeaves(updates []LeafUpdate) error {
	if len(updates) == 0 {
		return nil
	}

	sorted := make([]LeafUpdate, len(updates))
	copy(sorted, updates)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Index < sorted[j].Index })

	indexMap := make(map[int]int, len(sorted))
	for i, u := range sorted {
		indexMap[u.Index] = i
	}
	deduped := make([]LeafUpdate, 0, len(indexMap))
	for _, pos := range indexMap {
		deduped = append(deduped, sorted[pos])
	}
	sort.Slice(deduped, func(i, j int) bool { return deduped[i].Index < deduped[j].Index })
	sorted = deduped

	leafLayer := t.layers[0]
	for _, u := range sorted {
		if u.Index < 0 || u.Index >= len(leafLayer) {
			return rgerrors.Newf(rgerrors.ErrCanonicalEncodingDistortion, "leaf index %d out of bounds [0, %d)", u.Index, len(leafLayer))
		}
		if leafLayer[u.Index].Sig != node.SigAtomic {
			return rgerrors.Newf(rgerrors.ErrCanonicalEncodingDistortion, "leaf at index %d is not an atomic node", u.Index)
		}
		newNode, err := makeAtomicNode(u.Tx)
		if err != nil {
			return err
		}
		leafLayer[u.Index] = newNode
	}

	dirtyPositions := make(map[int]bool)
	for _, u := range sorted {
		dirtyPositions[u.Index] = true
	}

	for level := 0; level < len(t.layers)-1; level++ {
		parentDirty := make(map[int]bool)
		for pos := range dirtyPositions {
			parentDirty[pos/K] = true
		}

		for parent := range parentDirty {
			chunkStart := parent * K
			newNode, err := t.rebuildChunk(level, chunkStart)
			if err != nil {
				return err
			}
			t.layers[level+1][parent] = newNode
		}

		dirtyPositions = parentDirty
	}

	t.dirty = true
	return nil
}

// rebuildChunk recomputes a single parent node from K children at the given level.
func (t *Tree) rebuildChunk(level int, chunkStart int) (*node.RGNode, error) {
	layer := t.layers[level]
	end := chunkStart + K
	if end > len(layer) {
		end = len(layer)
	}
	chunk := make([]*node.RGNode, 0, K)
	chunk = append(chunk, layer[chunkStart:end]...)
	for len(chunk) < K {
		nn, err := node.NullNode(layer[0].Scale)
		if err != nil {
			return nil, err
		}
		chunk = append(chunk, nn)
	}
	parentScale := layer[0].Scale * K
	return coarseGrain(chunk, parentScale)
}

// Layer returns the node layer at the given coarsening level. Level 0 is
// the atomic (scale=1) layer. Panics if level is out of bounds.
func (t *Tree) Layer(level int) []*node.RGNode {
	return t.layers[level]
}

// LayerCount returns the number of coarsening levels in the tree.
func (t *Tree) LayerCount() int {
	return len(t.layers)
}

func makeAtomicNode(t *tx.Tx) (*node.RGNode, error) {
	txID, err := t.ID()
	if err != nil {
		return nil, err
	}

	value := new(big.Int).Set(t.Value)
	sumSquares := new(big.Int).Mul(value, value)
	if sumSquares.Sign() < 0 || sumSquares.Cmp(hash.UINT256_MAX) > 0 {
		return nil, rgerrors.New(rgerrors.ErrArithmeticOverflow, "tx square exceeds uint256")
	}

	n := &node.RGNode{
		Scale:      1,
		Volume:     node.Uint256ToBytes(value),
		Sig:        node.SigAtomic,
		Children:   [][32]byte{txID},
		SumSquares: node.Uint256ToBytes(sumSquares),
		Count:      1,
	}
	if t.Type == tx.TxTypeContractDeploy || t.Type == tx.TxTypeContractCall {
		n.ContractCount = 1
	}
	return n, nil
}

func atomicLayer(txs []*tx.Tx) ([]*node.RGNode, error) {
	nodes := make([]*node.RGNode, len(txs))
	for i, t := range txs {
		n, err := makeAtomicNode(t)
		if err != nil {
			return nil, err
		}
		nodes[i] = n
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

	var tmp big.Int
	for i, child := range chunk {
		childRoot, err := child.Root()
		if err != nil {
			return nil, err
		}
		children[i] = childRoot

		tmp.SetBytes(child.Volume[:])
		sumValues.Add(sumValues, &tmp)
		if sumValues.Sign() < 0 || sumValues.Cmp(hash.UINT256_MAX) > 0 {
			return nil, rgerrors.New(rgerrors.ErrArithmeticOverflow, "sumValues overflow")
		}

		tmp.SetBytes(child.SumSquares[:])
		sumSquares.Add(sumSquares, &tmp)
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
		Volume:        node.Uint256ToBytes(volume),
		Variance:      node.Uint256ToBytes(variance),
		Children:      children,
		SumSquares:    node.Uint256ToBytes(sumSquares),
		Count:         count,
		ContractCount: contractCount,
	}
	parent.Sig = node.DeriveSignature(parent)

	if _, err := parent.RecomputeSig(); err != nil {
		return nil, err
	}

	return parent, nil
}
