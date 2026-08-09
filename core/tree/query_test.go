package tree_test

import (
	"testing"

	"github.com/imattau/frg/core/node"
	"github.com/imattau/frg/core/tree"
	"github.com/imattau/frg/core/tx"
)

func TestQueryScaleAt(t *testing.T) {
	n := 64
	txs := make([]*tx.Tx, n)
	for i := range txs {
		txs[i] = makeTx("alice", "bob", int64(i+1), uint64(i))
	}

	tr, _ := tree.BuildTree(txs, nil)

	if tr.ScaleAt(0) != 1 {
		t.Fatalf("layer 0 scale: got %d, want 1", tr.ScaleAt(0))
	}
	if tr.ScaleAt(1) != 4 {
		t.Fatalf("layer 1 scale: got %d, want 4", tr.ScaleAt(1))
	}
	if tr.ScaleAt(2) != 16 {
		t.Fatalf("layer 2 scale: got %d, want 16", tr.ScaleAt(2))
	}
	if tr.ScaleAt(3) != 64 {
		t.Fatalf("root scale: got %d, want 64", tr.ScaleAt(3))
	}
}

func TestQuerySignatureCount(t *testing.T) {
	n := 64
	txs := make([]*tx.Tx, n)
	for i := range txs {
		txs[i] = makeTx("alice", "bob", int64(i+1), uint64(i))
	}

	tr, _ := tree.BuildTree(txs, nil)

	atomicCount := tr.SignatureCount(0, node.SigAtomic)
	if atomicCount != 64 {
		t.Fatalf("atomic count: got %d, want 64", atomicCount)
	}

	hist := tr.SignatureHistogram(1)
	if hist[node.SigLaminarFlow] != 16 {
		t.Fatalf("laminar at L1: got %d, want 16", hist[node.SigLaminarFlow])
	}
}

func TestQueryFindNodes(t *testing.T) {
	n := 10
	txs := make([]*tx.Tx, n)
	for i := range txs {
		txs[i] = makeTx("alice", "bob", int64(i+1), uint64(i))
	}

	tr, _ := tree.BuildTree(txs, nil)

	atomic := tr.FindNodes(0, func(n *node.RGNode) bool { return n.Sig == node.SigAtomic })
	if len(atomic) != 10 {
		t.Fatalf("expected 10 atomic nodes, got %d", len(atomic))
	}
}

func TestQueryCompareSignatures(t *testing.T) {
	txs1 := make([]*tx.Tx, 5)
	txs2 := make([]*tx.Tx, 5)
	for i := range txs1 {
		txs1[i] = makeTx("alice", "bob", int64(1), uint64(i))
		txs2[i] = makeTx("alice", "bob", int64(1), uint64(i))
	}
	txs2[0] = makeTx("alice", "bob", 99999, 0)

	tr1, _ := tree.BuildTree(txs1, nil)
	tr2, _ := tree.BuildTree(txs2, nil)

	diffs := tree.CompareSignatures(tr1, tr2)
	if len(diffs) == 0 {
		t.Fatal("expected diffs between different trees")
	}
}

func TestQueryContractDensity(t *testing.T) {
	n := 16
	txs := make([]*tx.Tx, n)
	for i := range txs {
		txs[i] = makeTx("alice", "bob", int64(1), uint64(i))
	}

	tr, _ := tree.BuildTree(txs, nil)

	_, _, ratio := tr.ContractDensity(0)
	if ratio != 0 {
		t.Fatalf("expected 0 contract density, got %.2f", ratio)
	}
}
