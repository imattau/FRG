package benchmarks

import (
	"encoding/binary"
	"fmt"
	"math/big"
	"runtime"
	"testing"
	"unsafe"

	"github.com/imattau/frg/core/contract"
	"github.com/imattau/frg/core/node"
	"github.com/imattau/frg/core/tree"
	"github.com/imattau/frg/core/tx"
	"github.com/imattau/frg/benchmarks/merkle"
	"github.com/imattau/frg/benchmarks/smt"
)

func BenchmarkComparison(b *testing.B) {
	scale1M := 1_000_000
	values1M := make([]uint64, scale1M)
	for i := range values1M {
		values1M[i] = uint64(i + 1)
	}
	smtKeys := make([][32]byte, scale1M)
	for i := range smtKeys {
		binary.BigEndian.PutUint64(smtKeys[i][:8], uint64(i))
	}

	sender := benchKeypair(b)
	receiver := benchKeypair(b)
	txs1M := make([]*tx.Tx, tree.TMax)
	for i := range txs1M {
		txs1M[i] = benchTx(b, sender, receiver, int64(i+1), uint64(i))
	}

	b.Run("A=1M", func(b *testing.B) {
		b.Run("FRG_Root", func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				blocks := (scale1M + tree.TMax - 1) / tree.TMax
				for blk := 0; blk < blocks; blk++ {
					start := blk * tree.TMax
					end := start + tree.TMax
					if end > scale1M {
						end = scale1M
					}
					_, _ = tree.BuildTreeRoot(txs1M[:end-start], nil)
				}
			}
		})

		b.Run("FRG_Update", func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				modified := make([]*tx.Tx, len(txs1M))
				copy(modified, txs1M)
				modified[0] = benchTx(b, sender, receiver, 99999, 0)
				_, _ = tree.BuildTreeRoot(modified, nil)
			}
		})

		b.Run("FRG_ProofVerify", func(b *testing.B) {
			txID, _ := txs1M[32768].ID()
			expectedRoot, _ := tree.BuildTreeRoot(txs1M, nil)
			proof, _ := tree.GenerateProof(txs1M, 32768)
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				tree.VerifyProof(proof, txID, expectedRoot)
			}
		})

		b.Run("Merkle_Root", func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_ = merkle.Build(values1M)
			}
		})

		b.Run("Merkle_Update", func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				modified := make([]uint64, len(values1M))
				copy(modified, values1M)
				modified[0] = 99999
				_ = merkle.Build(modified)
			}
		})

		b.Run("Merkle_ProofVerify", func(b *testing.B) {
			root := merkle.Build(values1M)
			proof := merkle.GenerateProof(values1M, 32768)
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				merkle.VerifyProof(root, 32768, values1M[32768], proof)
			}
		})

		b.Run("SMT_Root", func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				t := smt.New()
				for j := 0; j < scale1M; j++ {
					t.Insert(smtKeys[j], values1M[j])
				}
				_ = t.Root()
			}
		})

		b.Run("SMT_ProofVerify", func(b *testing.B) {
			t := smt.New()
			for j := 0; j < scale1M; j++ {
				t.Insert(smtKeys[j], values1M[j])
			}
			root := t.Root()
			proof := t.GenerateProof(smtKeys[32768])
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				t.VerifyProof(root, smtKeys[32768], values1M[32768], proof)
			}
		})

		b.Run("SMT_Update", func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				t := smt.New()
				for j := 0; j < scale1M; j++ {
					t.Insert(smtKeys[j], values1M[j])
				}
				t.Insert(smtKeys[0], 99999)
				_ = t.Root()
			}
		})
	})

	b.Run("B=Contracts", func(b *testing.B) {
		const numContracts = 100_000
		const numTxs = 10_000

		store := contract.NewStateStore()
		for i := 0; i < 100; i++ {
			key := fmt.Sprintf("key-%08d", i)
			store.Set([]byte(key), []byte(fmt.Sprintf("val-%d", i)))
		}
		stateRoot := store.StateRoot()

		contractNodes := make([]*node.RGNode, numContracts)
		for i := range contractNodes {
			v := big.NewInt(int64(i + 1))
			contractNodes[i] = &node.RGNode{
				Scale:         1,
				Volume:        node.Uint256ToBytes(v),
				Sig:           node.SigContract,
				Children:      [][32]byte{stateRoot},
				SumSquares:    node.Uint256ToBytes(new(big.Int).Mul(v, v)),
				Count:         1,
				ContractCount: 1,
			}
		}

		txs := make([]*tx.Tx, numTxs)
		for i := range txs {
			txs[i] = benchTx(b, sender, receiver, int64(i+1), uint64(i))
		}

		b.Run("FRG", func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_, _ = tree.BuildTreeRoot(txs, contractNodes)
			}
		})

		b.Run("Merkle", func(b *testing.B) {
			allValues := make([]uint64, numTxs+numContracts)
			for i := range txs {
				allValues[i] = uint64(txs[i].Value.Int64())
			}
			for i := range contractNodes {
				allValues[numTxs+i] = node.BytesToUint256(contractNodes[i].Volume).Uint64()
			}
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_ = merkle.Build(allValues)
			}
		})

		b.Run("SMT", func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				t := smt.New()
				for j := 0; j < numTxs+numContracts; j++ {
					var key [32]byte
					binary.BigEndian.PutUint64(key[:8], uint64(j))
					if j < numTxs {
						t.Insert(key, uint64(txs[j].Value.Int64()))
					} else {
						t.Insert(key, 1)
					}
				}
				_ = t.Root()
			}
		})
	})
}

func BenchmarkContractDensity(b *testing.B) {
	sender := benchKeypair(b)
	receiver := benchKeypair(b)

	const numTxs = 1000

	b.Run("SparseWide", func(b *testing.B) {
		const numContracts = 100
		const keysPerContract = 1000

		contractNodes := make([]*node.RGNode, numContracts)
		for i := range contractNodes {
			store := contract.NewStateStore()
			for j := 0; j < keysPerContract; j++ {
				key := fmt.Sprintf("c%d-key-%08d", i, j)
				store.Set([]byte(key), []byte(fmt.Sprintf("val-%d", j)))
			}
			stateRoot := store.StateRoot()
			v := big.NewInt(int64(i + 1))
			contractNodes[i] = &node.RGNode{
				Scale:         1,
				Volume:        node.Uint256ToBytes(v),
				Sig:           node.SigContract,
				Children:      [][32]byte{stateRoot},
				SumSquares:    node.Uint256ToBytes(new(big.Int).Mul(v, v)),
				Count:         1,
				ContractCount: 1,
			}
		}

		txs := make([]*tx.Tx, numTxs)
		for i := range txs {
			txs[i] = benchTx(b, sender, receiver, int64(i+1), uint64(i))
		}

		b.ResetTimer()
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_, _ = tree.BuildTreeRoot(txs, contractNodes)
		}
	})

	b.Run("DenseNarrow", func(b *testing.B) {
		const numContracts = 1000
		const keysPerContract = 100

		contractNodes := make([]*node.RGNode, numContracts)
		for i := range contractNodes {
			store := contract.NewStateStore()
			for j := 0; j < keysPerContract; j++ {
				key := fmt.Sprintf("c%d-key-%08d", i, j)
				store.Set([]byte(key), []byte(fmt.Sprintf("val-%d", j)))
			}
			stateRoot := store.StateRoot()
			v := big.NewInt(int64(i + 1))
			contractNodes[i] = &node.RGNode{
				Scale:         1,
				Volume:        node.Uint256ToBytes(v),
				Sig:           node.SigContract,
				Children:      [][32]byte{stateRoot},
				SumSquares:    node.Uint256ToBytes(new(big.Int).Mul(v, v)),
				Count:         1,
				ContractCount: 1,
			}
		}

		txs := make([]*tx.Tx, numTxs)
		for i := range txs {
			txs[i] = benchTx(b, sender, receiver, int64(i+1), uint64(i))
		}

		b.ResetTimer()
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_, _ = tree.BuildTreeRoot(txs, contractNodes)
		}
	})
}

func TestProofGeometry(t *testing.T) {
	const entries = 65536

	sender := benchKeypair(t)
	receiver := benchKeypair(t)
	txs := make([]*tx.Tx, entries)
	for i := range txs {
		txs[i] = benchTx(t, sender, receiver, int64(i+1), uint64(i))
	}

	frgTree, err := tree.BuildTree(txs, nil)
	if err != nil {
		t.Fatalf("BuildTree: %v", err)
	}
	frgDepth := frgTree.LayerCount()
	frgSiblings := (frgDepth - 1) * 3
	frgProofBytes := frgSiblings * 32
	frgHashes := frgDepth - 1

	values := make([]uint64, entries)
	for i := range values {
		values[i] = uint64(i + 1)
	}
	merkleDepth := 0
	for n := entries; n > 0; n >>= 1 {
		merkleDepth++
	}
	merkleSiblings := merkleDepth - 1
	merkleProofBytes := merkleSiblings * 32

	smtKeys := make([][32]byte, entries)
	for i := range smtKeys {
		binary.BigEndian.PutUint64(smtKeys[i][:8], uint64(i))
	}
	smtDepth := 257
	smtSiblings := 256
	smtProofBytes := 256 * 32

	t.Logf("\nTree    Entries  Depth  ProofSiblings  ProofBytes  HashesPerVerify")
	t.Logf("FRG     %7d  %5d  %13d  %10d  %15d", entries, frgDepth, frgSiblings, frgProofBytes, frgHashes)
	t.Logf("Merkle  %7d  %5d  %13d  %10d  %15d", entries, merkleDepth, merkleSiblings, merkleProofBytes, merkleSiblings)
	t.Logf("SMT     %7d  %5d  %13d  %10d  %15d", entries, smtDepth, smtSiblings, smtProofBytes, 256)
}

func TestMemoryBreakdown(t *testing.T) {
	const n = 65536

	sender := benchKeypair(t)
	receiver := benchKeypair(t)
	txs := make([]*tx.Tx, n)
	for i := range txs {
		txs[i] = benchTx(t, sender, receiver, int64(i+1), uint64(i))
	}

	runtime.GC()
	var m1 runtime.MemStats
	runtime.ReadMemStats(&m1)

	tr, err := tree.BuildTree(txs, nil)
	if err != nil {
		t.Fatalf("BuildTree: %v", err)
	}
	_ = tr.Root()

	runtime.GC()
	var m2 runtime.MemStats
	runtime.ReadMemStats(&m2)

	totalAlloc := m2.TotalAlloc - m1.TotalAlloc

	totalNodes := 0
	totalChildBytes := uint64(0)
	inlineByteFields := 0

	structSize := unsafe.Sizeof(node.RGNode{})

	for level := 0; level < tr.LayerCount(); level++ {
		layer := tr.Layer(level)
		totalNodes += len(layer)
		for _, n := range layer {
			totalChildBytes += uint64(len(n.Children) * 32)
			inlineByteFields += 96
		}
	}

	structBytes := uint64(totalNodes) * uint64(structSize)
	inlineBytes := uint64(inlineByteFields)

	t.Logf("\nFRG Memory Breakdown (%d entries):", n)
	t.Logf("  Total nodes:       %d", totalNodes)
	t.Logf("  Layers:            %d", tr.LayerCount())
	t.Logf("  TotalAlloc:        %.1f MB", float64(totalAlloc)/1e6)
	t.Logf("  Struct overhead:   %.1f MB (%d nodes x %d B)", float64(structBytes)/1e6, totalNodes, structSize)
	t.Logf("  Child hashes:      %.1f MB", float64(totalChildBytes)/1e6)
	t.Logf("  uint256 fields:    %.1f MB (3x[32]byte inline per node)", float64(inlineBytes)/1e6)
	t.Logf("  Per-node approx:   %.0f B", float64(totalAlloc)/float64(totalNodes))

	runtime.GC()
	var m3 runtime.MemStats
	runtime.ReadMemStats(&m3)

	values := make([]uint64, n)
	for i := range values {
		values[i] = uint64(i + 1)
	}
	_ = merkle.Build(values)

	runtime.GC()
	var m4 runtime.MemStats
	runtime.ReadMemStats(&m4)

	merkleAlloc := m4.TotalAlloc - m3.TotalAlloc
	t.Logf("\nMerkle Memory Breakdown (%d entries):", n)
	t.Logf("  TotalAlloc:        %.1f MB", float64(merkleAlloc)/1e6)
	t.Logf("  FRG/Merkle ratio:  %.1fx", float64(totalAlloc)/float64(merkleAlloc))
}
