package tree_test

import (
	"math/big"
	"testing"

	"github.com/imattau/frg/core/tree"
	"github.com/imattau/frg/core/tx"
)

func makeTx(sender, receiver string, value int64, nonce uint64) *tx.Tx {
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)
	return &tx.Tx{
		Sender:   sender,
		Receiver: receiver,
		Value:    new(big.Int).Mul(big.NewInt(value), scale),
		Nonce:    nonce,
	}
}

func TestEmptyBlock(t *testing.T) {
	b := &tree.Block{Height: 1, Txs: nil}
	root, err := b.BuildRoot()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var zero [32]byte
	if root == zero {
		t.Fatal("empty block root is all-zero")
	}
}

func TestSingleTx(t *testing.T) {
	b := &tree.Block{
		Height: 1,
		Txs:    []*tx.Tx{makeTx("alice", "bob", 1, 0)},
	}
	root, err := b.BuildRoot()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var zero [32]byte
	if root == zero {
		t.Fatal("single tx root is all-zero")
	}
}

func TestRootDeterministic(t *testing.T) {
	txs := []*tx.Tx{
		makeTx("alice", "bob", 1, 0),
		makeTx("bob", "carol", 2, 1),
		makeTx("carol", "alice", 3, 2),
	}
	b1 := &tree.Block{Height: 1, Txs: txs}
	r1, err := b1.BuildRoot()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	b2 := &tree.Block{Height: 1, Txs: txs}
	r2, err := b2.BuildRoot()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r1 != r2 {
		t.Fatal("BuildRoot is not deterministic")
	}
}

func TestOrderMatters(t *testing.T) {
	t1 := makeTx("alice", "bob", 1, 0)
	t2 := makeTx("bob", "carol", 2, 1)

	b1 := &tree.Block{Height: 1, Txs: []*tx.Tx{t1, t2}}
	r1, err := b1.BuildRoot()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	b2 := &tree.Block{Height: 1, Txs: []*tx.Tx{t2, t1}}
	r2, err := b2.BuildRoot()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if r1 == r2 {
		t.Fatal("different tx order produced same root")
	}
}

func TestTooManyTxs(t *testing.T) {
	txs := make([]*tx.Tx, 65537)
	for i := range txs {
		txs[i] = makeTx("alice", "bob", 1, uint64(i))
	}
	b := &tree.Block{Height: 1, Txs: txs}
	_, err := b.BuildRoot()
	if err == nil {
		t.Fatal("expected ERR_010 for > 65536 txs")
	}
}

func TestTreeEmptyBlock(t *testing.T) {
	tr, err := tree.BuildTree(nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	root := tr.Root()
	var zero [32]byte
	if root == zero {
		t.Fatal("empty tree root is all-zero")
	}
	if tr.LayerCount() != 1 {
		t.Fatalf("expected 1 layer, got %d", tr.LayerCount())
	}
}

func TestTreeBuildDeterministic(t *testing.T) {
	txs := []*tx.Tx{
		makeTx("alice", "bob", 1, 0),
		makeTx("bob", "carol", 2, 1),
	}
	tr1, err := tree.BuildTree(txs, nil)
	if err != nil {
		t.Fatalf("BuildTree: %v", err)
	}
	tr2, err := tree.BuildTree(txs, nil)
	if err != nil {
		t.Fatalf("BuildTree: %v", err)
	}
	if tr1.Root() != tr2.Root() {
		t.Fatal("BuildTree is not deterministic")
	}
}

func TestTreeMatchesBuildTreeRoot(t *testing.T) {
	txs := []*tx.Tx{
		makeTx("alice", "bob", 1, 0),
		makeTx("bob", "carol", 2, 1),
		makeTx("carol", "dave", 3, 2),
		makeTx("dave", "eve", 4, 3),
		makeTx("eve", "alice", 5, 4),
	}
	tr, err := tree.BuildTree(txs, nil)
	if err != nil {
		t.Fatalf("BuildTree: %v", err)
	}
	rootDirect, err := tree.BuildTreeRoot(txs, nil)
	if err != nil {
		t.Fatalf("BuildTreeRoot: %v", err)
	}
	if tr.Root() != rootDirect {
		t.Fatal("Tree.Root() != BuildTreeRoot()")
	}
}

func TestTreeUpdateLeafMatchesFullRebuild(t *testing.T) {
	n := 100
	txs := make([]*tx.Tx, n)
	for i := range txs {
		txs[i] = makeTx("alice", "bob", int64(i+1), uint64(i))
	}

	for _, idx := range []int{0, 4, 17, 63, 99} {
		tr, err := tree.BuildTree(txs, nil)
		if err != nil {
			t.Fatalf("BuildTree: %v", err)
		}

		modified := makeTx("bob", "alice", int64(idx+1000), uint64(idx+1000))
		if err := tr.UpdateLeaf(idx, modified); err != nil {
			t.Fatalf("UpdateLeaf(%d): %v", idx, err)
		}

		altered := make([]*tx.Tx, n)
		copy(altered, txs)
		altered[idx] = modified
		fullRoot, err := tree.BuildTreeRoot(altered, nil)
		if err != nil {
			t.Fatalf("BuildTreeRoot: %v", err)
		}

		if tr.Root() != fullRoot {
			t.Fatalf("UpdateLeaf root at idx=%d != full rebuild root", idx)
		}
	}
}

func TestTreeUpdateLeavesMultiple(t *testing.T) {
	n := 64
	txs := make([]*tx.Tx, n)
	for i := range txs {
		txs[i] = makeTx("alice", "bob", int64(i+1), uint64(i))
	}

	tr, err := tree.BuildTree(txs, nil)
	if err != nil {
		t.Fatalf("BuildTree: %v", err)
	}

	updates := []tree.LeafUpdate{
		{Index: 0, Tx: makeTx("bob", "alice", 1000, 1000)},
		{Index: 4, Tx: makeTx("carol", "dave", 2000, 2000)},
		{Index: 15, Tx: makeTx("eve", "alice", 3000, 3000)},
		{Index: 63, Tx: makeTx("bob", "carol", 4000, 4000)},
	}
	if err := tr.UpdateLeaves(updates); err != nil {
		t.Fatalf("UpdateLeaves: %v", err)
	}

	altered := make([]*tx.Tx, n)
	copy(altered, txs)
	for _, u := range updates {
		altered[u.Index] = u.Tx
	}
	fullRoot, err := tree.BuildTreeRoot(altered, nil)
	if err != nil {
		t.Fatalf("BuildTreeRoot: %v", err)
	}

	if tr.Root() != fullRoot {
		t.Fatal("UpdateLeaves root != full rebuild root")
	}
}

func TestTreeUpdateLeavesSameParent(t *testing.T) {
	n := 20
	txs := make([]*tx.Tx, n)
	for i := range txs {
		txs[i] = makeTx("alice", "bob", int64(i+1), uint64(i))
	}

	tr, err := tree.BuildTree(txs, nil)
	if err != nil {
		t.Fatalf("BuildTree: %v", err)
	}

	updates := []tree.LeafUpdate{
		{Index: 0, Tx: makeTx("x", "y", 100, 100)},
		{Index: 1, Tx: makeTx("x", "y", 200, 200)},
		{Index: 2, Tx: makeTx("x", "y", 300, 300)},
	}
	if err := tr.UpdateLeaves(updates); err != nil {
		t.Fatalf("UpdateLeaves: %v", err)
	}

	altered := make([]*tx.Tx, n)
	copy(altered, txs)
	for _, u := range updates {
		altered[u.Index] = u.Tx
	}
	fullRoot, err := tree.BuildTreeRoot(altered, nil)
	if err != nil {
		t.Fatalf("BuildTreeRoot: %v", err)
	}

	if tr.Root() != fullRoot {
		t.Fatal("UpdateLeaves (same parent) root != full rebuild root")
	}
}

func TestTreeUpdateLeavesDeduplication(t *testing.T) {
	n := 10
	txs := make([]*tx.Tx, n)
	for i := range txs {
		txs[i] = makeTx("alice", "bob", int64(i+1), uint64(i))
	}

	tr, err := tree.BuildTree(txs, nil)
	if err != nil {
		t.Fatalf("BuildTree: %v", err)
	}

	winner := makeTx("winner", "loser", 9999, 9999)
	updates := []tree.LeafUpdate{
		{Index: 3, Tx: makeTx("x", "y", 1, 1)},
		{Index: 3, Tx: makeTx("x", "y", 2, 2)},
		{Index: 3, Tx: winner},
	}
	if err := tr.UpdateLeaves(updates); err != nil {
		t.Fatalf("UpdateLeaves: %v", err)
	}

	altered := make([]*tx.Tx, n)
	copy(altered, txs)
	altered[3] = winner
	fullRoot, err := tree.BuildTreeRoot(altered, nil)
	if err != nil {
		t.Fatalf("BuildTreeRoot: %v", err)
	}

	if tr.Root() != fullRoot {
		t.Fatal("UpdateLeaves dedup root != full rebuild root")
	}
}

func TestTreeUpdateLeafInvalidIndex(t *testing.T) {
	txs := []*tx.Tx{
		makeTx("alice", "bob", 1, 0),
	}
	tr, _ := tree.BuildTree(txs, nil)

	badTx := makeTx("x", "y", 100, 100)
	if err := tr.UpdateLeaf(-1, badTx); err == nil {
		t.Fatal("expected error for negative index")
	}
	if err := tr.UpdateLeaf(1, badTx); err == nil {
		t.Fatal("expected error for out-of-bounds index")
	}
}

func TestTreeLayerAccess(t *testing.T) {
	n := 64
	txs := make([]*tx.Tx, n)
	for i := range txs {
		txs[i] = makeTx("alice", "bob", int64(i+1), uint64(i))
	}

	tr, err := tree.BuildTree(txs, nil)
	if err != nil {
		t.Fatalf("BuildTree: %v", err)
	}

	expectedLayers := 4
	if tr.LayerCount() != expectedLayers {
		t.Fatalf("expected %d layers, got %d", expectedLayers, tr.LayerCount())
	}

	if len(tr.Layer(0)) != 64 {
		t.Fatalf("expected 64 nodes at layer 0, got %d", len(tr.Layer(0)))
	}
	if len(tr.Layer(1)) != 16 {
		t.Fatalf("expected 16 nodes at layer 1, got %d", len(tr.Layer(1)))
	}
	if len(tr.Layer(2)) != 4 {
		t.Fatalf("expected 4 nodes at layer 2, got %d", len(tr.Layer(2)))
	}
	if len(tr.Layer(3)) != 1 {
		t.Fatalf("expected 1 node at layer 3, got %d", len(tr.Layer(3)))
	}
}

func TestTreeUpdateRootMemoization(t *testing.T) {
	n := 10
	txs := make([]*tx.Tx, n)
	for i := range txs {
		txs[i] = makeTx("alice", "bob", int64(i+1), uint64(i))
	}

	tr, _ := tree.BuildTree(txs, nil)
	r1 := tr.Root()
	r2 := tr.Root()
	if r1 != r2 {
		t.Fatal("cached root mismatch")
	}

	modified := makeTx("bob", "alice", 1000, 1000)
	_ = tr.UpdateLeaf(0, modified)
	r3 := tr.Root()
	if r3 == r1 {
		t.Fatal("root should change after update")
	}
	r4 := tr.Root()
	if r3 != r4 {
		t.Fatal("cached root mismatch after update")
	}
}

func TestTreeUpdateWithPadding(t *testing.T) {
	n := 10
	txs := make([]*tx.Tx, n)
	for i := range txs {
		txs[i] = makeTx("alice", "bob", int64(i+1), uint64(i))
	}

	tr, err := tree.BuildTree(txs, nil)
	if err != nil {
		t.Fatalf("BuildTree: %v", err)
	}

	idx := 9
	modified := makeTx("bob", "alice", int64(idx+1000), uint64(idx+1000))
	if err := tr.UpdateLeaf(idx, modified); err != nil {
		t.Fatalf("UpdateLeaf(%d): %v", idx, err)
	}

	altered := make([]*tx.Tx, n)
	copy(altered, txs)
	altered[idx] = modified
	fullRoot, err := tree.BuildTreeRoot(altered, nil)
	if err != nil {
		t.Fatalf("BuildTreeRoot: %v", err)
	}

	if tr.Root() != fullRoot {
		t.Fatal("UpdateLeaf with padding root != full rebuild root")
	}
}
