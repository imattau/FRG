package testdata_test

import (
	"encoding/hex"
	"math/big"
	"testing"

	"github.com/imattau/frg/core/node"
	"github.com/imattau/frg/core/tree"
	"github.com/imattau/frg/core/tx"
)

var scale = new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)

func scaled(v int64) *big.Int {
	return new(big.Int).Mul(big.NewInt(v), scale)
}

func TestEmptyBlockRootMatchesSpec(t *testing.T) {
	root := node.EmptyBlockRoot()
	if root == [32]byte{} {
		t.Fatal("empty block root is all-zero")
	}
	b := &tree.Block{Height: 0, Txs: nil}
	bRoot, err := b.BuildRoot()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if root != bRoot {
		t.Fatalf("EmptyBlockRoot() != Block{}.BuildRoot(): %x vs %x", root, bRoot)
	}
}

func TestNullNodeAtEachScale(t *testing.T) {
	scales := []uint32{1, 4, 16, 64, 256, 1024, 4096, 16384, 65536}
	roots := make(map[uint32][32]byte)
	for _, s := range scales {
		n, err := node.NullNode(s)
		if err != nil {
			t.Fatalf("NullNode(%d) error: %v", s, err)
		}
		root, err := n.Root()
		if err != nil {
			t.Fatalf("NullNode(%d).Root() error: %v", s, err)
		}
		if root == [32]byte{} {
			t.Fatalf("NullNode(%d) root is all-zero", s)
		}
		for prevScale, prevRoot := range roots {
			if root == prevRoot {
				t.Fatalf("NullNode(%d) and NullNode(%d) have same root", s, prevScale)
			}
		}
		roots[s] = root
	}
}

func TestTxSerializeKnownVector(t *testing.T) {
	t1 := &tx.Tx{Sender: "alice", Receiver: "bob", Value: scaled(1), Nonce: 0}
	b, err := t1.Serialize()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hex.EncodeToString(b[:6]) != "54585f563100" {
		t.Fatalf("wrong TX domain prefix: %x", b[:6])
	}
	if len(b) != 58 {
		t.Fatalf("expected 58 bytes, got %d", len(b))
	}
}

func TestAtomicNodeRootNonZero(t *testing.T) {
	t1 := &tx.Tx{Sender: "alice", Receiver: "bob", Value: scaled(1), Nonce: 0}
	id, err := t1.ID()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	n := &node.RGNode{
		Scale:    1,
		Volume:   scaled(1),
		Variance: big.NewInt(0),
		Sig:      node.SigAtomic,
		Children: [][32]byte{id},
	}
	root, err := n.Root()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if root == [32]byte{} {
		t.Fatal("atomic node root is all-zero")
	}
}

func TestFiveTransactionBlock(t *testing.T) {
	txs := []*tx.Tx{
		{Sender: "alice", Receiver: "bob", Value: scaled(1), Nonce: 0},
		{Sender: "bob", Receiver: "carol", Value: scaled(2), Nonce: 1},
		{Sender: "carol", Receiver: "dave", Value: scaled(3), Nonce: 2},
		{Sender: "dave", Receiver: "eve", Value: scaled(4), Nonce: 3},
		{Sender: "eve", Receiver: "alice", Value: scaled(5), Nonce: 4},
	}
	b := &tree.Block{Height: 1, Txs: txs}
	root, err := b.BuildRoot()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if root == [32]byte{} {
		t.Fatal("5-tx block root is all-zero")
	}
	b2 := &tree.Block{Height: 1, Txs: txs}
	root2, err := b2.BuildRoot()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if root != root2 {
		t.Fatal("5-tx block root is not deterministic")
	}
}
