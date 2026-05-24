package testdata_test

import (
	"math/big"
	"testing"

	"github.com/imattau/frg/core/node"
	"github.com/imattau/frg/core/tree"
	"github.com/imattau/frg/core/tx"
	"pgregory.net/rapid"
)

func genTx(t *rapid.T) *tx.Tx {
	sender := rapid.StringMatching(`[a-z]{1,20}`).Draw(t, "sender")
	receiver := rapid.StringMatching(`[a-z]{1,20}`).Draw(t, "receiver")
	v := rapid.Int64Range(1, 1_000_000).Draw(t, "value")
	nonce := rapid.Uint64().Draw(t, "nonce")
	s := new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)
	return &tx.Tx{
		Sender:   sender,
		Receiver: receiver,
		Value:    new(big.Int).Mul(big.NewInt(v), s),
		Nonce:    nonce,
	}
}

func TestPropSingleRootAlways(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(1, 64).Draw(t, "n")
		txs := make([]*tx.Tx, n)
		for i := range txs {
			txs[i] = genTx(t)
		}
		b := &tree.Block{Height: 1, Txs: txs}
		root, err := b.BuildRoot()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		var zero [32]byte
		if root == zero {
			t.Fatal("root is all-zero")
		}
	})
}

func TestPropOrderMatters(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		t1 := genTx(t)
		t2 := genTx(t)

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
		id1, _ := t1.ID()
		id2, _ := t2.ID()
		if id1 == id2 {
			t.Skip()
		}
		if r1 == r2 {
			t.Fatal("different tx order produced same root")
		}
	})
}

func TestPropEmptyBlockStable(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		r1 := node.EmptyBlockRoot()
		r2 := node.EmptyBlockRoot()
		if r1 != r2 {
			t.Fatal("EmptyBlockRoot is not stable")
		}
	})
}

func TestPropVarianceNonNegative(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(1, 16).Draw(t, "n")
		txs := make([]*tx.Tx, n)
		for i := range txs {
			txs[i] = genTx(t)
		}
		b := &tree.Block{Height: 1, Txs: txs}
		_, err := b.BuildRoot()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func TestPropScaleDomain(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		validScales := []uint32{1, 4, 16, 64, 256, 1024, 4096, 16384, 65536}
		for _, s := range validScales {
			_, err := node.NullNode(s)
			if err != nil {
				t.Fatalf("NullNode(%d) failed: %v", s, err)
			}
		}
	})
}

func TestPropNoPaddingFraud(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(1, 7).Draw(t, "n")
		txs := make([]*tx.Tx, n)
		for i := range txs {
			txs[i] = genTx(t)
		}
		b := &tree.Block{Height: 1, Txs: txs}
		_, err := b.BuildRoot()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}
