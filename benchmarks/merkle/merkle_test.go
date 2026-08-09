package merkle

import (
	"testing"
)

func TestMerkleTreeMatchesBuild(t *testing.T) {
	n := 1000
	values := make([]uint64, n)
	for i := range values {
		values[i] = uint64(i + 1)
	}

	mt := NewMerkleTree(values)
	root := Build(values)

	if mt.Root() != root {
		t.Fatal("MerkleTree.Root() != Build()")
	}
}

func TestMerkleTreeUpdateLeaf(t *testing.T) {
	n := 100
	values := make([]uint64, n)
	for i := range values {
		values[i] = uint64(i + 1)
	}

	for _, idx := range []int{0, 4, 17, 63, 99} {
		mt := NewMerkleTree(values)
		mt.UpdateLeaf(idx, 99999)

		altered := make([]uint64, n)
		copy(altered, values)
		altered[idx] = 99999
		fullRoot := Build(altered)

		if mt.Root() != fullRoot {
			t.Fatalf("UpdateLeaf at idx=%d: root mismatch", idx)
		}
	}
}

func TestMerkleTreeUpdateLeaves(t *testing.T) {
	n := 64
	values := make([]uint64, n)
	for i := range values {
		values[i] = uint64(i + 1)
	}

	mt := NewMerkleTree(values)
	updates := []MerkleLeafUpdate{
		{Idx: 0, Value: 1000},
		{Idx: 4, Value: 2000},
		{Idx: 15, Value: 3000},
		{Idx: 63, Value: 4000},
	}
	mt.UpdateLeaves(updates)

	altered := make([]uint64, n)
	copy(altered, values)
	for _, u := range updates {
		altered[u.Idx] = u.Value
	}
	fullRoot := Build(altered)

	if mt.Root() != fullRoot {
		t.Fatal("UpdateLeaves root != full rebuild root")
	}
}

func TestMerkleTreeUpdateLeavesSameParent(t *testing.T) {
	n := 20
	values := make([]uint64, n)
	for i := range values {
		values[i] = uint64(i + 1)
	}

	mt := NewMerkleTree(values)
	updates := []MerkleLeafUpdate{
		{Idx: 0, Value: 100},
		{Idx: 1, Value: 200},
		{Idx: 2, Value: 300},
	}
	mt.UpdateLeaves(updates)

	altered := make([]uint64, n)
	copy(altered, values)
	for _, u := range updates {
		altered[u.Idx] = u.Value
	}
	fullRoot := Build(altered)

	if mt.Root() != fullRoot {
		t.Fatal("UpdateLeaves (same parent) root != full rebuild root")
	}
}

func TestMerkleTreeUpdateLeavesDedup(t *testing.T) {
	n := 10
	values := make([]uint64, n)
	for i := range values {
		values[i] = uint64(i + 1)
	}

	mt := NewMerkleTree(values)
	updates := []MerkleLeafUpdate{
		{Idx: 3, Value: 1},
		{Idx: 3, Value: 2},
		{Idx: 3, Value: 9999},
	}
	mt.UpdateLeaves(updates)

	altered := make([]uint64, n)
	copy(altered, values)
	altered[3] = 9999
	fullRoot := Build(altered)

	if mt.Root() != fullRoot {
		t.Fatal("UpdateLeaves dedup root != full rebuild root")
	}
}
