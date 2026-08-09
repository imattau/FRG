package smt

import (
	"encoding/binary"

	"github.com/imattau/frg/core/hash"
)

var defaultNodes [257][32]byte

func init() {
	for i := 1; i < len(defaultNodes); i++ {
		combined := make([]byte, 0, 64)
		combined = append(combined, defaultNodes[i-1][:]...)
		combined = append(combined, defaultNodes[i-1][:]...)
		defaultNodes[i] = hash.Hash(combined)
	}
}

type leaf struct {
	key   [32]byte
	value uint64
}

type smtNode struct {
	hash  [32]byte
	depth int
	left  *smtNode
	right *smtNode
}

func valueHash(value uint64) [32]byte {
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, value)
	return hash.Hash(buf)
}

func nodeHash(left, right [32]byte) [32]byte {
	combined := make([]byte, 0, 64)
	combined = append(combined, left[:]...)
	combined = append(combined, right[:]...)
	return hash.Hash(combined)
}

func isBitSet(key [32]byte, bit int) bool {
	byteIdx := bit / 8
	bitIdx := 7 - (bit % 8)
	return (key[byteIdx]>>bitIdx)&1 == 1
}

type Tree struct {
	leaves map[[32]byte]uint64
	root   [32]byte
	dirty  bool
}

func New() *Tree {
	return &Tree{leaves: make(map[[32]byte]uint64)}
}

func (t *Tree) Insert(key [32]byte, value uint64) {
	t.leaves[key] = value
	t.dirty = true
}

func (t *Tree) Root() [32]byte {
	if t.dirty {
		t.root = t.build()
		t.dirty = false
	}
	return t.root
}

func (t *Tree) build() [32]byte {
	if len(t.leaves) == 0 {
		return defaultNodes[256]
	}
	root := &smtNode{hash: defaultNodes[256], depth: 256}
	for key, val := range t.leaves {
		leafHash := valueHash(val)
		current := root
		for bit := 0; bit < 256; bit++ {
			var next **smtNode
			if isBitSet(key, bit) {
				next = &current.right
			} else {
				next = &current.left
			}
			if *next == nil {
				*next = &smtNode{hash: defaultNodes[255-bit], depth: 255 - bit}
			}
			current = *next
		}
		current.hash = leafHash
		current.left = nil
		current.right = nil
	}
	computeHashes(root)
	return root.hash
}

func computeHashes(n *smtNode) [32]byte {
	if n.left == nil && n.right == nil {
		return n.hash
	}
	var leftHash, rightHash [32]byte
	if n.left != nil {
		leftHash = computeHashes(n.left)
	} else {
		leftHash = defaultNodes[n.depth-1]
	}
	if n.right != nil {
		rightHash = computeHashes(n.right)
	} else {
		rightHash = defaultNodes[n.depth-1]
	}
	n.hash = nodeHash(leftHash, rightHash)
	return n.hash
}

func (t *Tree) GenerateProof(key [32]byte) [][32]byte {
	t.Root()
	root := &smtNode{hash: defaultNodes[256], depth: 256}
	for k, val := range t.leaves {
		leafHash := valueHash(val)
		current := root
		for bit := 0; bit < 256; bit++ {
			var next **smtNode
			if isBitSet(k, bit) {
				next = &current.right
			} else {
				next = &current.left
			}
			if *next == nil {
				*next = &smtNode{hash: defaultNodes[255-bit], depth: 255 - bit}
			}
			current = *next
		}
		current.hash = leafHash
		current.left = nil
		current.right = nil
	}
	computeHashes(root)

	proof := make([][32]byte, 256)
	current := root
	for bit := 0; bit < 256; bit++ {
		if isBitSet(key, bit) {
			if current.left != nil {
				proof[bit] = current.left.hash
			} else {
				proof[bit] = defaultNodes[255-bit]
			}
			current = current.right
		} else {
			if current.right != nil {
				proof[bit] = current.right.hash
			} else {
				proof[bit] = defaultNodes[255-bit]
			}
			current = current.left
		}
	}
	return proof
}

func (t *Tree) VerifyProof(root [32]byte, key [32]byte, value uint64, proof [][32]byte) bool {
	if len(proof) != 256 {
		return false
	}
	current := valueHash(value)
	for bit := 0; bit < 256; bit++ {
		if isBitSet(key, bit) {
			current = nodeHash(proof[bit], current)
		} else {
			current = nodeHash(current, proof[bit])
		}
	}
	return current == root
}
