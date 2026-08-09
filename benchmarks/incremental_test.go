package benchmarks

import (
	"fmt"
	"testing"

	"github.com/imattau/frg/core/tree"
	"github.com/imattau/frg/core/tx"
	"github.com/imattau/frg/benchmarks/merkle"
)

func BenchmarkIncrementalRG(b *testing.B) {
	sender := benchKeypair(b)
	receiver := benchKeypair(b)

	updateCounts := []int{1, 10, 100, 1_000, 10_000}
	for _, updates := range updateCounts {
		b.Run(fmt.Sprintf("updates=%d", updates), func(b *testing.B) {
			txs := make([]*tx.Tx, 65_536)
			for i := range txs {
				txs[i] = benchTx(b, sender, receiver, int64(i+1), uint64(i))
			}
			b.ResetTimer()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				modified := make([]*tx.Tx, len(txs))
				copy(modified, txs)
				for j := 0; j < updates && j < len(modified); j++ {
					modified[j] = benchTx(b, sender, receiver, int64(j+10000), uint64(j+len(txs)))
				}
				_, _ = tree.BuildTreeRoot(modified, nil)
			}
		})
	}
}

func BenchmarkIncrementalMerkle(b *testing.B) {
	updateCounts := []int{1, 10, 100, 1_000, 10_000}
	for _, updates := range updateCounts {
		b.Run(fmt.Sprintf("updates=%d", updates), func(b *testing.B) {
			n := 65_536
			values := make([]uint64, n)
			for i := range values {
				values[i] = uint64(i + 1)
			}
			b.ResetTimer()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				modified := make([]uint64, len(values))
				copy(modified, values)
				for j := 0; j < updates && j < len(modified); j++ {
					modified[j] = uint64(j + 10000)
				}
				_ = merkle.Build(modified)
			}
		})
	}
}

func BenchmarkIncrementalRGFull(b *testing.B) {
	sender := benchKeypair(b)
	receiver := benchKeypair(b)

	b.Run("full_vs_partial", func(b *testing.B) {
		txs := make([]*tx.Tx, 65_536)
		for i := range txs {
			txs[i] = benchTx(b, sender, receiver, int64(i+1), uint64(i))
		}

		b.Run("FullRebuild", func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_, _ = tree.BuildTreeRoot(txs, nil)
			}
		})

		b.Run("PartialRebuild_1tx", func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				modified := make([]*tx.Tx, len(txs))
				copy(modified, txs)
				modified[0] = benchTx(b, sender, receiver, 99999, 0)
				_, _ = tree.BuildTreeRoot(modified, nil)
			}
		})

		b.Run("PartialRebuild_10000tx", func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				modified := make([]*tx.Tx, len(txs))
				copy(modified, txs)
				for j := 0; j < 10_000; j++ {
					modified[j] = benchTx(b, sender, receiver, int64(99999+j), uint64(j))
				}
				_, _ = tree.BuildTreeRoot(modified, nil)
			}
		})
	})
}

func BenchmarkIncrementalMerkleFull(b *testing.B) {
	n := 65_536
	values := make([]uint64, n)
	for i := range values {
		values[i] = uint64(i + 1)
	}

	b.Run("FullRebuild", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = merkle.Build(values)
		}
	})

	b.Run("PartialRebuild_1leaf", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			modified := make([]uint64, len(values))
			copy(modified, values)
			modified[0] = 99999
			_ = merkle.Build(modified)
		}
	})

	b.Run("PartialRebuild_10000leaf", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			modified := make([]uint64, len(values))
			copy(modified, values)
			for j := 0; j < 10_000; j++ {
				modified[j] = uint64(99999 + j)
			}
			_ = merkle.Build(modified)
		}
	})
}
