package tree_test

import (
	"math/big"
	"testing"

	"github.com/imattau/frg/core/node"
	"github.com/imattau/frg/core/tree"
	"github.com/imattau/frg/core/tx"
)

func TestProofCorrectness(t *testing.T) {
	sizes := []int{1, 4, 10, 64, 100, 1024}

	for _, n := range sizes {
		txs := make([]*tx.Tx, n)
		for i := range txs {
			txs[i] = makeTx("alice", "bob", int64(i+1), uint64(i))
		}

		expectedRoot, err := tree.BuildTreeRoot(txs, nil)
		if err != nil {
			t.Fatalf("BuildTreeRoot(n=%d): %v", n, err)
		}

		for idx := 0; idx < n; idx++ {
			txID, err := txs[idx].ID()
			if err != nil {
				t.Fatalf("ID(%d): %v", idx, err)
			}

			proof, err := tree.GenerateProof(txs, idx)
			if err != nil {
				t.Fatalf("GenerateProof(n=%d, idx=%d): %v", n, idx, err)
			}

			if !tree.VerifyProof(proof, txID, expectedRoot) {
				t.Fatalf("VerifyProof failed: n=%d idx=%d", n, idx)
			}
		}

		tr, err := tree.BuildTree(txs, nil)
		if err != nil {
			t.Fatalf("BuildTree(n=%d): %v", n, err)
		}

		for idx := 0; idx < n; idx++ {
			txID, _ := txs[idx].ID()
			proof, err := tr.GenerateProof(idx)
			if err != nil {
				t.Fatalf("Tree.GenerateProof(n=%d, idx=%d): %v", n, idx, err)
			}
			if !tree.VerifyProof(proof, txID, tr.Root()) {
				t.Fatalf("Tree.VerifyProof failed: n=%d idx=%d", n, idx)
			}
		}
	}
}

func TestProofWrongTxID(t *testing.T) {
	n := 10
	txs := make([]*tx.Tx, n)
	for i := range txs {
		txs[i] = makeTx("alice", "bob", int64(i+1), uint64(i))
	}

	expectedRoot, _ := tree.BuildTreeRoot(txs, nil)
	proof, _ := tree.GenerateProof(txs, 3)

	wrongTx := makeTx("eve", "mallory", 9999, 9999)
	wrongID, _ := wrongTx.ID()

	if tree.VerifyProof(proof, wrongID, expectedRoot) {
		t.Fatal("proof verified with wrong txid")
	}
}

func TestProofWrongRoot(t *testing.T) {
	n := 10
	txs := make([]*tx.Tx, n)
	for i := range txs {
		txs[i] = makeTx("alice", "bob", int64(i+1), uint64(i))
	}

	txID, _ := txs[3].ID()
	proof, _ := tree.GenerateProof(txs, 3)

	var wrongRoot [32]byte
	wrongRoot[0] = 0xFF

	if tree.VerifyProof(proof, txID, wrongRoot) {
		t.Fatal("proof verified against wrong root")
	}
}

func TestProofWrongSibling(t *testing.T) {
	n := 100
	txs := make([]*tx.Tx, n)
	for i := range txs {
		txs[i] = makeTx("alice", "bob", int64(i+1), uint64(i))
	}

	expectedRoot, _ := tree.BuildTreeRoot(txs, nil)
	txID, _ := txs[50].ID()
	proof, _ := tree.GenerateProof(txs, 50)

	if len(proof.Steps) < 2 {
		t.Fatal("expected at least 2 steps")
	}

	proof.Steps[1].Siblings[0][0] ^= 0xFF
	if tree.VerifyProof(proof, txID, expectedRoot) {
		t.Fatal("proof verified with corrupted sibling")
	}
}

func TestProofSwappedSiblings(t *testing.T) {
	n := 100
	txs := make([]*tx.Tx, n)
	for i := range txs {
		txs[i] = makeTx("alice", "bob", int64(i+1), uint64(i))
	}

	expectedRoot, _ := tree.BuildTreeRoot(txs, nil)
	txID, _ := txs[50].ID()
	proof, _ := tree.GenerateProof(txs, 50)

	if len(proof.Steps) < 2 {
		t.Fatal("expected at least 2 steps")
	}

	proof.Steps[1].Siblings[0], proof.Steps[1].Siblings[1] = proof.Steps[1].Siblings[1], proof.Steps[1].Siblings[0]
	if tree.VerifyProof(proof, txID, expectedRoot) {
		t.Fatal("proof verified with swapped siblings")
	}
}

func TestProofWrongChildIdx(t *testing.T) {
	n := 100
	txs := make([]*tx.Tx, n)
	for i := range txs {
		txs[i] = makeTx("alice", "bob", int64(i+1), uint64(i))
	}

	expectedRoot, _ := tree.BuildTreeRoot(txs, nil)
	txID, _ := txs[50].ID()
	proof, _ := tree.GenerateProof(txs, 50)

	if len(proof.Steps) < 2 {
		t.Fatal("expected at least 2 steps")
	}

	proof.Steps[1].ChildIdx = (proof.Steps[1].ChildIdx + 1) % 4
	if tree.VerifyProof(proof, txID, expectedRoot) {
		t.Fatal("proof verified with wrong child index")
	}
}

func TestProofWrongScale(t *testing.T) {
	n := 100
	txs := make([]*tx.Tx, n)
	for i := range txs {
		txs[i] = makeTx("alice", "bob", int64(i+1), uint64(i))
	}

	expectedRoot, _ := tree.BuildTreeRoot(txs, nil)
	txID, _ := txs[50].ID()
	proof, _ := tree.GenerateProof(txs, 50)

	if len(proof.Steps) < 2 {
		t.Fatal("expected at least 2 steps")
	}

	proof.Steps[1].Scale *= 2
	if tree.VerifyProof(proof, txID, expectedRoot) {
		t.Fatal("proof verified with wrong scale")
	}
}

func TestProofWrongVolume(t *testing.T) {
	n := 100
	txs := make([]*tx.Tx, n)
	for i := range txs {
		txs[i] = makeTx("alice", "bob", int64(i+1), uint64(i))
	}

	expectedRoot, _ := tree.BuildTreeRoot(txs, nil)
	txID, _ := txs[50].ID()
	proof, _ := tree.GenerateProof(txs, 50)

	if len(proof.Steps) < 2 {
		t.Fatal("expected at least 2 steps")
	}

	proof.Steps[1].Volume[31] ^= 0xFF
	if tree.VerifyProof(proof, txID, expectedRoot) {
		t.Fatal("proof verified with wrong volume")
	}
}

func TestProofTruncated(t *testing.T) {
	n := 100
	txs := make([]*tx.Tx, n)
	for i := range txs {
		txs[i] = makeTx("alice", "bob", int64(i+1), uint64(i))
	}

	expectedRoot, _ := tree.BuildTreeRoot(txs, nil)
	txID, _ := txs[50].ID()
	proof, _ := tree.GenerateProof(txs, 50)

	truncated := &tree.InclusionProof{Steps: proof.Steps[:len(proof.Steps)-1]}
	if tree.VerifyProof(truncated, txID, expectedRoot) {
		t.Fatal("proof verified when truncated")
	}
}

func TestProofExtraStep(t *testing.T) {
	n := 100
	txs := make([]*tx.Tx, n)
	for i := range txs {
		txs[i] = makeTx("alice", "bob", int64(i+1), uint64(i))
	}

	expectedRoot, _ := tree.BuildTreeRoot(txs, nil)
	txID, _ := txs[50].ID()
	proof, _ := tree.GenerateProof(txs, 50)

	extra := &tree.ProofStep{
		Scale:    262144,
		ChildIdx: 0,
	}
	corrupted := &tree.InclusionProof{Steps: append(proof.Steps, *extra)}
	if tree.VerifyProof(corrupted, txID, expectedRoot) {
		t.Fatal("proof verified with extra step")
	}
}

func TestProofWrongSig(t *testing.T) {
	n := 100
	txs := make([]*tx.Tx, n)
	for i := range txs {
		txs[i] = makeTx("alice", "bob", int64(i+1), uint64(i))
	}

	expectedRoot, _ := tree.BuildTreeRoot(txs, nil)
	txID, _ := txs[50].ID()
	proof, _ := tree.GenerateProof(txs, 50)

	if len(proof.Steps) < 2 {
		t.Fatal("expected at least 2 steps")
	}

	proof.Steps[1].Sig = node.SigVolatileShock
	if tree.VerifyProof(proof, txID, expectedRoot) {
		t.Fatal("proof verified with wrong signature")
	}
}

func TestProofScaleDoesNotOverflow(t *testing.T) {
	n := 65536
	txs := make([]*tx.Tx, n)
	for i := range txs {
		txs[i] = makeTx("alice", "bob", int64(i+1), uint64(i))
	}

	expectedRoot, _ := tree.BuildTreeRoot(txs, nil)
	txID, _ := txs[0].ID()
	proof, _ := tree.GenerateProof(txs, 0)

	if !tree.VerifyProof(proof, txID, expectedRoot) {
		t.Fatal("proof verification failed at TMax scale")
	}

	lastStep := proof.Steps[len(proof.Steps)-1]
	if lastStep.Scale != 65536 {
		t.Fatalf("expected root scale 65536, got %d", lastStep.Scale)
	}
}

func TestProofSingleTx(t *testing.T) {
	txs := []*tx.Tx{makeTx("alice", "bob", 1, 0)}
	proof, err := tree.GenerateProof(txs, 0)
	if err != nil {
		t.Fatalf("GenerateProof: %v", err)
	}
	if len(proof.Steps) != 1 {
		t.Fatalf("single-tx proof should have 1 atomic step, got %d", len(proof.Steps))
	}

	txID, _ := txs[0].ID()
	expectedRoot, _ := tree.BuildTreeRoot(txs, nil)
	if !tree.VerifyProof(proof, txID, expectedRoot) {
		t.Fatal("single-tx proof verification failed")
	}
}

func TestProofPaddingChunks(t *testing.T) {
	for _, n := range []int{2, 3, 5, 7, 10, 17} {
		txs := make([]*tx.Tx, n)
		for i := range txs {
			txs[i] = makeTx("alice", "bob", int64(i+1), uint64(i))
		}

		expectedRoot, _ := tree.BuildTreeRoot(txs, nil)

		for idx := 0; idx < n; idx++ {
			txID, _ := txs[idx].ID()
			proof, _ := tree.GenerateProof(txs, idx)
			if !tree.VerifyProof(proof, txID, expectedRoot) {
				t.Fatalf("padding proof failed: n=%d idx=%d", n, idx)
			}
		}
	}
}

func TestProofWithContracts(t *testing.T) {
	n := 64
	txs := make([]*tx.Tx, n)
	for i := range txs {
		txs[i] = makeTx("alice", "bob", int64(i+1), uint64(i))
	}

	contractNodes := make([]*node.RGNode, 2)
	for i := range contractNodes {
		v := big.NewInt(int64(i + 1))
		contractNodes[i] = &node.RGNode{
			Scale:         1,
			Volume:        node.Uint256ToBytes(v),
			Sig:           node.SigContract,
			Children:      [][32]byte{{}},
			SumSquares:    node.Uint256ToBytes(new(big.Int).Mul(v, v)),
			Count:         1,
			ContractCount: 1,
		}
	}

	tr, err := tree.BuildTree(txs, contractNodes)
	if err != nil {
		t.Fatalf("BuildTree: %v", err)
	}

	for idx := 0; idx < n; idx++ {
		txID, _ := txs[idx].ID()
		proof, err := tr.GenerateProof(idx)
		if err != nil {
			t.Fatalf("GenerateProof(idx=%d): %v", idx, err)
		}
		if !tree.VerifyProof(proof, txID, tr.Root()) {
			t.Fatalf("proof verification with contracts failed at idx=%d", idx)
		}
	}
}
