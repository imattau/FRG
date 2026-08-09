package benchmarks

import (
	"encoding/binary"
	"fmt"
	"math/big"
	"testing"

	"github.com/imattau/frg/core/hash"
	"github.com/imattau/frg/core/node"
	"github.com/imattau/frg/core/tree"
	"github.com/imattau/frg/core/tx"
	"github.com/imattau/frg/benchmarks/merkle"
	"github.com/imattau/frg/benchmarks/smt"
)

func BenchmarkRGTreeBuild(b *testing.B) {
	scales := []int{1_000, 10_000, 65_536}
	for _, n := range scales {
		b.Run(fmt.Sprintf("tx=%d", n), func(b *testing.B) {
			sender := benchKeypair(b)
			receiver := benchKeypair(b)
			txs := make([]*tx.Tx, n)
			for i := range txs {
				txs[i] = benchTx(b, sender, receiver, int64(i+1), uint64(i))
			}
			b.ResetTimer()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_, _ = tree.BuildTreeRoot(txs, nil)
			}
		})
	}
}

func BenchmarkRGTreeBuildLarge(b *testing.B) {
	sender := benchKeypair(b)
	receiver := benchKeypair(b)
	n := 1_000_000
	txs := make([]*tx.Tx, tree.TMax)
	for i := range txs {
		txs[i] = benchTx(b, sender, receiver, int64(i+1), uint64(i))
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		blocks := (n + tree.TMax - 1) / tree.TMax
		for blk := 0; blk < blocks; blk++ {
			start := blk * tree.TMax
			end := start + tree.TMax
			if end > n {
				end = n
			}
			_, _ = tree.BuildTreeRoot(txs[:end-start], nil)
		}
	}
}

func BenchmarkRGNodeSerialize(b *testing.B) {
	n := &node.RGNode{
		Scale:    4,
		Volume:   node.Uint256ToBytes(big.NewInt(1000)),
		Variance: node.Uint256ToBytes(big.NewInt(50)),
		Sig:      node.SigLaminarFlow,
		Children: make([][32]byte, 4),
	}
	for i := range n.Children {
		n.Children[i] = hash.Hash([]byte(fmt.Sprintf("child-%d", i)))
	}
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = n.Serialize()
	}
}

func BenchmarkRGTreeRootCalc(b *testing.B) {
	n := &node.RGNode{
		Scale:    4,
		Volume:   node.Uint256ToBytes(big.NewInt(1000)),
		Variance: node.Uint256ToBytes(big.NewInt(50)),
		Sig:      node.SigLaminarFlow,
		Children: make([][32]byte, 4),
	}
	for i := range n.Children {
		n.Children[i] = hash.Hash([]byte(fmt.Sprintf("child-%d", i)))
	}
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = n.Root()
	}
}

func BenchmarkRGTreeProof(b *testing.B) {
	sender := benchKeypair(b)
	receiver := benchKeypair(b)
	n := 10_000
	txs := make([]*tx.Tx, n)
	for i := range txs {
		txs[i] = benchTx(b, sender, receiver, int64(i+1), uint64(i))
	}

	b.Run("Generate", func(b *testing.B) {
		tr, err := tree.BuildTree(txs, nil)
		if err != nil {
			b.Fatalf("BuildTree: %v", err)
		}
		b.ResetTimer()
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_, _ = tr.GenerateProof(n / 2)
		}
	})
	b.Run("Verify", func(b *testing.B) {
		proof, _ := tree.GenerateProof(txs, n/2)
		txID, _ := txs[n/2].ID()
		expectedRoot, _ := tree.BuildTreeRoot(txs, nil)
		b.ResetTimer()
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			tree.VerifyProof(proof, txID, expectedRoot)
		}
	})
}

func BenchmarkMerkleTreeBuild(b *testing.B) {
	scales := []int{1_000, 10_000, 65_536, 1_000_000}
	for _, n := range scales {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			values := make([]uint64, n)
			for i := range values {
				values[i] = uint64(i + 1)
			}
			b.ResetTimer()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_ = merkle.Build(values)
			}
		})
	}
}

func BenchmarkMerkleProof(b *testing.B) {
	n := 10_000
	values := make([]uint64, n)
	for i := range values {
		values[i] = uint64(i + 1)
	}

	b.Run("Generate", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = merkle.GenerateProof(values, n/2)
		}
	})
	b.Run("Verify", func(b *testing.B) {
		root := merkle.Build(values)
		proof := merkle.GenerateProof(values, n/2)
		b.ResetTimer()
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			merkle.VerifyProof(root, n/2, values[n/2], proof)
		}
	})
}

func BenchmarkSMTTreeBuild(b *testing.B) {
	scales := []int{1_000, 10_000, 65_536}
	for _, n := range scales {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			keys := make([][32]byte, n)
			values := make([]uint64, n)
			for i := range keys {
				binary.BigEndian.PutUint64(keys[i][:8], uint64(i))
				values[i] = uint64(i + 1)
			}
			b.ResetTimer()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				t := smt.New()
				for j := 0; j < n; j++ {
					t.Insert(keys[j], values[j])
				}
				_ = t.Root()
			}
		})
	}
}

func BenchmarkSMTProof(b *testing.B) {
	n := 10_000
	keys := make([][32]byte, n)
	values := make([]uint64, n)
	for i := range keys {
		binary.BigEndian.PutUint64(keys[i][:8], uint64(i))
		values[i] = uint64(i + 1)
	}
	t := smt.New()
	for j := 0; j < n; j++ {
		t.Insert(keys[j], values[j])
	}
	root := t.Root()

	b.Run("Generate", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = t.GenerateProof(keys[n/2])
		}
	})
	b.Run("Verify", func(b *testing.B) {
		proof := t.GenerateProof(keys[n/2])
		b.ResetTimer()
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			t.VerifyProof(root, keys[n/2], values[n/2], proof)
		}
	})
}
