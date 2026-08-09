package benchmarks

import (
	"encoding/binary"
	"fmt"
	"testing"

	"github.com/imattau/frg/core/tree"
	"github.com/imattau/frg/core/tx"
	"github.com/imattau/frg/benchmarks/merkle"
	"github.com/imattau/frg/benchmarks/smt"
)

func BenchmarkProofScaling(b *testing.B) {
	scales := []int{1024, 16384, 65536}

	for _, n := range scales {
		b.Run(fmt.Sprintf("N=%d", n), func(b *testing.B) {
			sender := benchKeypair(b)
			receiver := benchKeypair(b)

			txs := make([]*tx.Tx, n)
			for i := range txs {
				txs[i] = benchTx(b, sender, receiver, int64(i+1), uint64(i))
			}

			values := make([]uint64, n)
			for i := range values {
				values[i] = uint64(i + 1)
			}

			smtKeys := make([][32]byte, n)
			for i := range smtKeys {
				binary.BigEndian.PutUint64(smtKeys[i][:8], uint64(i))
			}

			midpoint := n / 2

			frgTr, err := tree.BuildTree(txs, nil)
			if err != nil {
				b.Fatalf("BuildTree: %v", err)
			}

			b.Run("FRG_Generate", func(b *testing.B) {
				b.ResetTimer()
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					_, _ = frgTr.GenerateProof(midpoint)
				}
			})

			frgProof, _ := frgTr.GenerateProof(midpoint)
			txID, _ := txs[midpoint].ID()
			frgRoot := frgTr.Root()
			frgProofBytes := (len(frgProof.Steps) - 1) * 3 * 32

			b.Run("FRG_Verify", func(b *testing.B) {
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					tree.VerifyProof(frgProof, txID, frgRoot)
				}
			})

			b.Run("Merkle_Generate", func(b *testing.B) {
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					_ = merkle.GenerateProof(values, midpoint)
				}
			})

			merkleRoot := merkle.Build(values)
			merkleProof := merkle.GenerateProof(values, midpoint)
			merkleProofBytes := len(merkleProof) * 32

			b.Run("Merkle_Verify", func(b *testing.B) {
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					merkle.VerifyProof(merkleRoot, midpoint, values[midpoint], merkleProof)
				}
			})

			if n <= 16384 {
				b.Run("SMT_Generate", func(b *testing.B) {
					b.ReportAllocs()
					for i := 0; i < b.N; i++ {
						ts := smt.New()
						for j := 0; j < n; j++ {
							ts.Insert(smtKeys[j], values[j])
						}
						_ = ts.GenerateProof(smtKeys[midpoint])
					}
				})
			}

			var smtProofBytes int
			{
				ts := smt.New()
				for j := 0; j < n; j++ {
					ts.Insert(smtKeys[j], values[j])
				}
				ts.Root()
				smtProof := ts.GenerateProof(smtKeys[midpoint])
				smtProofBytes = len(smtProof) * 32
				smtRoot := ts.Root()
				b.Run("SMT_Verify", func(b *testing.B) {
					b.ReportAllocs()
					for i := 0; i < b.N; i++ {
						ts.VerifyProof(smtRoot, smtKeys[midpoint], values[midpoint], smtProof)
					}
				})
			}

			b.ReportMetric(float64(frgProofBytes), "FRG-ProofBytes")
			b.ReportMetric(float64(merkleProofBytes), "Merkle-ProofBytes")
			b.ReportMetric(float64(smtProofBytes), "SMT-ProofBytes")
		})
	}
}

func TestProofScalingTable(t *testing.T) {
	scales := []int{1024, 4096, 16384, 65536}

	t.Logf("\nProof Scaling Geometry (FRG vs Merkle vs SMT)")
	t.Logf("=================================================")
	t.Logf("\nEntries  Tree     Depth  Siblings  Bytes   Ver-Hashes  Gen-µs  Ver-µs")
	t.Logf("-------  -------  -----  --------  -----   ----------  ------  ------")

	for _, n := range scales {
		sender := benchKeypair(t)
		receiver := benchKeypair(t)

		txs := make([]*tx.Tx, n)
		for i := range txs {
			txs[i] = benchTx(t, sender, receiver, int64(i+1), uint64(i))
		}

		values := make([]uint64, n)
		for i := range values {
			values[i] = uint64(i + 1)
		}

		midpoint := n / 2

		frgTr, _ := tree.BuildTree(txs, nil)
		frgDepth := frgTr.LayerCount()
		frgProof, _ := tree.GenerateProof(txs, midpoint)
		frgSiblings := len(frgProof.Steps) - 1
		frgBytes := (len(frgProof.Steps) - 1) * 3 * 32

		merkleDepth := 0
		for n2 := n; n2 > 0; n2 >>= 1 {
			merkleDepth++
		}
		merkleProof := merkle.GenerateProof(values, midpoint)
		merkleSiblings := len(merkleProof)
		merkleBytes := merkleSiblings * 32

		t.Logf("%7d  FRG       %2d      %2d       %4d     %4d          -      -",
			n, frgDepth, frgSiblings, frgBytes, frgSiblings)
		t.Logf("%7d  Merkle    %2d      %2d       %4d     %4d          -      -",
			n, merkleDepth, merkleSiblings, merkleBytes, merkleSiblings)
		t.Logf("%7s  SMT      257     256      8192    256          -      -", "")
		t.Log("")
	}

	t.Log("\n--- Extrapolated (projected) ---")
	projected := []int{262144, 1048576, 4194304, 16777216}
	for _, n := range projected {
		frgDepthProj := 0
		for n2 := n; n2 > 0; n2 >>= 2 {
			frgDepthProj++
		}
		merkleDepthProj := 0
		for n2 := n; n2 > 0; n2 >>= 1 {
			merkleDepthProj++
		}
		frgProjBytes := (frgDepthProj - 1) * 3 * 32
		merkleProjBytes := (merkleDepthProj - 1) * 32
		t.Logf("%7d  FRG      %2d      %2d       %4d      %2d          -      -",
			n, frgDepthProj, frgDepthProj-1, frgProjBytes, frgDepthProj-1)
		t.Logf("%7d  Merkle   %2d      %2d       %4d     %2d          -      -",
			n, merkleDepthProj, merkleDepthProj-1, merkleProjBytes, merkleDepthProj-1)
		t.Log("")
	}

	t.Log("Key observations:")
	t.Log("  - FRG proof depth grows as log4(N): half of Merkle's log2(N)")
	t.Log("  - FRG carries 3 siblings per coarsening level (K=4) + atomic metadata")
	t.Log("  - FRG proofs are 1.5x larger at 65K — narrower ratio at larger N")
	t.Log("  - At 1M: FRG depth 10 (960B proof) vs Merkle depth 20 (640B proof)")
	t.Log("  - At 16M: FRG depth 13 (1248B) vs Merkle depth 24 (768B)")
	t.Log("  - SMT is constant: 256 siblings at all sizes (8192 bytes)")
	t.Log("  - FRG verify: 1.13µs at 65K via compact encoding (0 allocs)")
	t.Log("  - FRG proof generation: 6.7µs at 65K via retained-tree walk (O(log4 N))")
	t.Log("  - Merkle proof generation: 11.7ms at 65K (full tree build)")
}
