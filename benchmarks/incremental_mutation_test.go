package benchmarks

import (
	"fmt"
	"testing"

	"github.com/imattau/frg/core/tree"
	"github.com/imattau/frg/core/tx"
	"github.com/imattau/frg/benchmarks/merkle"
)

func BenchmarkIncrementalMutation(b *testing.B) {
	const n = 65536

	sender := benchKeypair(b)
	receiver := benchKeypair(b)

	txs := make([]*tx.Tx, n)
	for i := range txs {
		txs[i] = benchTx(b, sender, receiver, int64(i+1), uint64(i))
	}

	const poolSize = 10000
	altTxs := make([]*tx.Tx, poolSize)
	for i := range altTxs {
		altTxs[i] = benchTx(b, sender, receiver, int64(i+1000000), uint64(i+1000000+n))
	}

	frgTree, err := tree.BuildTree(txs, nil)
	if err != nil {
		b.Fatalf("BuildTree: %v", err)
	}

	updateCounts := []int{1, 10, 100, 1000}

	for _, updates := range updateCounts {
		b.Run(fmt.Sprintf("FRG_Update_%d", updates), func(b *testing.B) {
			leafUpdates := make([]tree.LeafUpdate, updates)
			b.ResetTimer()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				for j := 0; j < updates; j++ {
					pos := (i*updates + j) % n
					txIdx := (i*updates + j) % poolSize
					leafUpdates[j].Index = pos
					leafUpdates[j].Tx = altTxs[txIdx]
				}
				_ = frgTree.UpdateLeaves(leafUpdates)
			}
		})
	}

	values := make([]uint64, n)
	for i := range values {
		values[i] = uint64(i + 1)
	}
	merkleTree := merkle.NewMerkleTree(values)

	for _, updates := range updateCounts {
		b.Run(fmt.Sprintf("Merkle_Update_%d", updates), func(b *testing.B) {
			leafUpdates := make([]merkle.MerkleLeafUpdate, updates)
			b.ResetTimer()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				for j := 0; j < updates; j++ {
					pos := (i*updates + j) % n
					leafUpdates[j].Idx = pos
					leafUpdates[j].Value = uint64(i*updates + j + 1000000)
				}
				merkleTree.UpdateLeaves(leafUpdates)
			}
		})
	}
}
