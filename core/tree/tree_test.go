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
