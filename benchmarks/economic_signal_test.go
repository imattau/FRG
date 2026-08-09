package benchmarks

import (
	"math/rand"
	"testing"

	"github.com/imattau/frg/core/node"
	"github.com/imattau/frg/core/tree"
	"github.com/imattau/frg/core/tx"
)

func BenchmarkEconomicSignal(b *testing.B) {
	const numAccounts = 1_000_000
	const blockSize = tree.TMax

	patterns := []struct {
		name string
		gen  func(int) int64
	}{
		{"Uniform", func(i int) int64 { return 1000 }},
		{"SingleCluster", func(i int) int64 {
			if i < numAccounts/10 {
				return 100000
			}
			return 1
		}},
		{"TwoClusters", func(i int) int64 {
			if i < numAccounts/10 || (i >= numAccounts/2 && i < numAccounts/2+numAccounts/10) {
				return 50000
			}
			return 1
		}},
		{"Migration", func(i int) int64 {
			hot := (numAccounts / 10) + (i/1000)%(numAccounts/5)
			if i >= hot && i < hot+numAccounts/10 {
				return 100000
			}
			return 1
		}},
		{"Periodic", func(i int) int64 {
			if i%1000 < 100 {
				return 50000
			}
			return 1
		}},
		{"Random", func(i int) int64 {
			rng := rand.New(rand.NewSource(int64(i)))
			if rng.Float64() < 0.1 {
				return int64(rng.Float64() * 100000)
			}
			return 1
		}},
	}

	for _, pat := range patterns {
		b.Run(pat.name, func(b *testing.B) {
			sender := benchKeypair(b)
			receiver := benchKeypair(b)

			txs := make([]*tx.Tx, blockSize)
			for i := range txs {
				val := pat.gen(i % numAccounts)
				txs[i] = benchTx(b, sender, receiver, val, uint64(i))
			}

			b.ResetTimer()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_, _ = tree.BuildTreeRoot(txs, nil)
			}
		})
	}
}

func BenchmarkRGLayerSignatures(b *testing.B) {
	sender := benchKeypair(b)
	receiver := benchKeypair(b)

	b.Run("SignatureDistribution", func(b *testing.B) {
		txs := make([]*tx.Tx, tree.TMax)
		for i := range txs {
			val := int64(1)
			if i < tree.TMax/10 {
				val = 100000
			} else if i < tree.TMax/5 {
				val = 50000
			}
			txs[i] = benchTx(b, sender, receiver, val, uint64(i))
		}

		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			atomicNodes, _ := tree.AtomicLayer(txs)
			layer := atomicNodes
			sigCounts := map[node.Signature]int{}
			for len(layer) > 1 {
				for _, n := range layer {
					sigCounts[n.Sig]++
				}
				nextLayer, _ := tree.CoarsenLayer(layer)
				layer = nextLayer
			}
			_ = sigCounts
		}
	})
}

func BenchmarkClusteredActivity(b *testing.B) {
	const numAccounts = 1_000_000

	distributions := []struct {
		name string
		desc string
		gen  func(int) int64
	}{
		{"90-10", "10% extremely active, 90% inactive", func(i int) int64 {
			if i < numAccounts/10 {
				return 100000
			}
			return 1
		}},
		{"70-20-10", "10% hot, 20% warm, 70% cold", func(i int) int64 {
			if i < numAccounts/10 {
				return 100000
			}
			if i < numAccounts/10+numAccounts/5 {
				return 10000
			}
			return 1
		}},
	}

	for _, dist := range distributions {
		b.Run(dist.name, func(b *testing.B) {
			sender := benchKeypair(b)
			receiver := benchKeypair(b)

			txs := make([]*tx.Tx, tree.TMax)
			for i := range txs {
				val := dist.gen(i % numAccounts)
				txs[i] = benchTx(b, sender, receiver, val, uint64(i))
			}

			b.Run("BuildOnly", func(b *testing.B) {
				b.ResetTimer()
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					_, _ = tree.BuildTreeRoot(txs, nil)
				}
			})

			b.Run("SignatureWalk", func(b *testing.B) {
				tr, _ := tree.BuildTree(txs, nil)
				b.ResetTimer()
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					volatileCount := 0
					stagnantCount := 0
					laminarCount := 0

					for level := 0; level < tr.LayerCount(); level++ {
						for _, n := range tr.Layer(level) {
							switch n.Sig {
							case node.SigVolatileShock:
								volatileCount++
							case node.SigStagnantState:
								stagnantCount++
							case node.SigLaminarFlow:
								laminarCount++
							}
						}
					}
					_ = volatileCount
					_ = stagnantCount
					_ = laminarCount
				}
			})
		})
	}
}
