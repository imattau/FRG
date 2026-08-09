package merkle

import (
	"encoding/binary"
	"sort"

	"github.com/imattau/frg/core/hash"
)

func leafHash(value uint64) [32]byte {
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, value)
	return hash.Hash(buf)
}

func parentHash(left, right [32]byte) [32]byte {
	combined := make([]byte, 0, 64)
	combined = append(combined, left[:]...)
	combined = append(combined, right[:]...)
	return hash.Hash(combined)
}

func Build(values []uint64) [32]byte {
	if len(values) == 0 {
		return [32]byte{}
	}

	layer := make([][32]byte, len(values))
	for i, v := range values {
		layer[i] = leafHash(v)
	}

	for len(layer) > 1 {
		nextLen := (len(layer) + 1) / 2
		next := make([][32]byte, nextLen)
		for i := 0; i < nextLen; i++ {
			left := layer[i*2]
			right := left
			if i*2+1 < len(layer) {
				right = layer[i*2+1]
			}
			next[i] = parentHash(left, right)
		}
		layer = next
	}

	return layer[0]
}

func GenerateProof(values []uint64, idx int) [][32]byte {
	if idx < 0 || idx >= len(values) {
		return nil
	}

	layer := make([][32]byte, len(values))
	for i, v := range values {
		layer[i] = leafHash(v)
	}

	var proof [][32]byte

	for len(layer) > 1 {
		siblingIdx := idx ^ 1
		if siblingIdx < len(layer) {
			proof = append(proof, layer[siblingIdx])
		} else {
			proof = append(proof, layer[idx])
		}
		idx /= 2
		nextLen := (len(layer) + 1) / 2
		next := make([][32]byte, nextLen)
		for i := 0; i < nextLen; i++ {
			left := layer[i*2]
			right := left
			if i*2+1 < len(layer) {
				right = layer[i*2+1]
			}
			next[i] = parentHash(left, right)
		}
		layer = next
	}

	return proof
}

func VerifyProof(root [32]byte, idx int, value uint64, proof [][32]byte) bool {
	current := leafHash(value)
	for _, sibling := range proof {
		if idx%2 == 0 {
			current = parentHash(current, sibling)
		} else {
			current = parentHash(sibling, current)
		}
		idx /= 2
	}
	return current == root
}

type MerkleLeafUpdate struct {
	Idx   int
	Value uint64
}

type MerkleTree struct {
	layers [][][32]byte
	root   [32]byte
}

func NewMerkleTree(values []uint64) *MerkleTree {
	if len(values) == 0 {
		return &MerkleTree{}
	}

	layers := make([][][32]byte, 0, 32)
	layer := make([][32]byte, len(values))
	for i, v := range values {
		layer[i] = leafHash(v)
	}
	layers = append(layers, layer)

	for len(layer) > 1 {
		nextLen := (len(layer) + 1) / 2
		next := make([][32]byte, nextLen)
		for i := 0; i < nextLen; i++ {
			left := layer[i*2]
			right := left
			if i*2+1 < len(layer) {
				right = layer[i*2+1]
			}
			next[i] = parentHash(left, right)
		}
		layers = append(layers, next)
		layer = next
	}

	return &MerkleTree{layers: layers, root: layer[0]}
}

func (mt *MerkleTree) Root() [32]byte {
	return mt.root
}

func (mt *MerkleTree) UpdateLeaf(idx int, newValue uint64) {
	mt.layers[0][idx] = leafHash(newValue)
	pos := idx
	for level := 0; level < len(mt.layers)-1; level++ {
		siblingIdx := pos ^ 1
		left := mt.layers[level][pos]
		right := left
		if siblingIdx < len(mt.layers[level]) {
			right = mt.layers[level][siblingIdx]
		}
		var parent [32]byte
		if pos%2 == 0 {
			parent = parentHash(left, right)
		} else {
			parent = parentHash(right, left)
		}
		pos /= 2
		mt.layers[level+1][pos] = parent
	}
	mt.root = mt.layers[len(mt.layers)-1][0]
}

func (mt *MerkleTree) UpdateLeaves(updates []MerkleLeafUpdate) {
	if len(updates) == 0 {
		return
	}

	sorted := make([]MerkleLeafUpdate, len(updates))
	copy(sorted, updates)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Idx < sorted[j].Idx })

	indexMap := make(map[int]int, len(sorted))
	for i, u := range sorted {
		indexMap[u.Idx] = i
	}
	deduped := make([]MerkleLeafUpdate, 0, len(indexMap))
	for _, pos := range indexMap {
		deduped = append(deduped, sorted[pos])
	}
	sort.Slice(deduped, func(i, j int) bool { return deduped[i].Idx < deduped[j].Idx })

	for _, u := range deduped {
		mt.layers[0][u.Idx] = leafHash(u.Value)
	}

	dirtyPositions := make(map[int]bool)
	for _, u := range deduped {
		dirtyPositions[u.Idx] = true
	}

	for level := 0; level < len(mt.layers)-1; level++ {
		parentDirty := make(map[int]bool)
		for pos := range dirtyPositions {
			parentDirty[pos/2] = true
		}

		for parent := range parentDirty {
			leftIdx := parent * 2
			rightIdx := leftIdx + 1
			left := mt.layers[level][leftIdx]
			right := left
			if rightIdx < len(mt.layers[level]) {
				right = mt.layers[level][rightIdx]
			}
			mt.layers[level+1][parent] = parentHash(left, right)
		}

		dirtyPositions = parentDirty
	}

	mt.root = mt.layers[len(mt.layers)-1][0]
}
