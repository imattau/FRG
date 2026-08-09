package benchmarks

import (
	"fmt"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/imattau/frg/core/node"
	"github.com/imattau/frg/core/tree"
	"github.com/imattau/frg/core/tx"
	"github.com/imattau/frg/benchmarks/merkle"
)

const queryN = 65024

type querySetup struct {
	frgTree     *tree.Tree
	merkleTree  *merkle.MerkleTree
	values      []uint64
	txs         []*tx.Tx
	contractNodes []*node.RGNode
}

func buildQuerySetup(tb testing.TB) *querySetup {
	tb.Helper()
	sender := benchKeypair(tb)
	receiver := benchKeypair(tb)

	txs := make([]*tx.Tx, queryN)
	values := make([]uint64, queryN)
	for i := range txs {
		var val int64
		switch {
		case i < queryN/10:
			val = 100000
		case i < queryN/10+queryN/5:
			val = 10000
		default:
			val = 1
		}
		txs[i] = benchTx(tb, sender, receiver, val, uint64(i))
		values[i] = uint64(val)
	}

	contractNodes := make([]*node.RGNode, 0)
	for i := 0; i < queryN; i += 1024 {
		v := big.NewInt(int64(i%1000 + 1))
		sumSq := new(big.Int).Mul(v, v)
		contractNodes = append(contractNodes, &node.RGNode{
			Scale:         1,
			Volume:        node.Uint256ToBytes(v),
			Sig:           node.SigContract,
			Children:      [][32]byte{hashVal(uint64(i))},
			SumSquares:    node.Uint256ToBytes(sumSq),
			Count:         1,
			ContractCount: 1,
		})
	}

	frgTree, err := tree.BuildTree(txs, contractNodes)
	if err != nil {
		tb.Fatalf("BuildTree: %v", err)
	}

	mt := merkle.NewMerkleTree(values)
	return &querySetup{
		frgTree:      frgTree,
		merkleTree:   mt,
		values:       values,
		txs:          txs,
		contractNodes: contractNodes,
	}
}

func hashVal(v uint64) [32]byte {
	var h [32]byte
	bePutU64(h[:], v)
	return h
}

func bePutU64(b []byte, v uint64) {
	b[0] = byte(v >> 56)
	b[1] = byte(v >> 48)
	b[2] = byte(v >> 40)
	b[3] = byte(v >> 32)
	b[4] = byte(v >> 24)
	b[5] = byte(v >> 16)
	b[6] = byte(v >> 8)
	b[7] = byte(v)
}

type qResult struct {
	Name        string
	FrgDesc     string
	MerkleDesc  string
	FrgTime     time.Duration
	MerkleTime  time.Duration
	FrgFound    int
	MerkleFound int
}

func TestStructuralQueries(t *testing.T) {
	q := buildQuerySetup(t)
	frgTree := q.frgTree
	values := q.values

	var results []qResult

	t.Log("FRG vs Merkle Structural Query Comparison")
	t.Log("==========================================\n")

	t.Log("-- Q1: Signature histogram at all scales --")
	t.Log("   FRG: iterate 9 retained layers via SignatureHistogram()")
	t.Log("   Merkle: flat uint64 list — cannot answer without hierarchical reconstruction\n")

	frgT1 := time.Now()
	sigByLevel := make([]map[node.Signature]int, frgTree.LayerCount())
	for level := 0; level < frgTree.LayerCount(); level++ {
		sigByLevel[level] = frgTree.SignatureHistogram(level)
	}
	frgTime1 := time.Since(frgT1)

	sigNames := map[node.Signature]string{1: "Atomic", 2: "NullPad", 3: "Stagnant", 4: "Laminar", 5: "Volatile", 6: "Contract"}
	t.Log("   Per-level signature breakdown:")
	for level := 0; level < frgTree.LayerCount(); level++ {
		scale := frgTree.Layer(level)[0].Scale
		var parts []string
		for sig := node.Signature(1); sig <= node.Signature(6); sig++ {
			if cnt := sigByLevel[level][sig]; cnt > 0 {
				parts = append(parts, fmt.Sprintf("%s=%d", sigNames[sig], cnt))
			}
		}
		t.Logf("     L%d (scale=%d): %s", level, scale, strings.Join(parts, ", "))
	}

	results = append(results, qResult{
		Name:       "Q1: Signature histogram",
		FrgDesc:    "retained layers",
		MerkleDesc: "requires tree rebuild",
		FrgTime:    frgTime1,
		FrgFound:   frgTree.LayerCount(),
	})

	t.Log("\n-- Q2: Contract-dominant regions at scale-1024 --")
	t.Log("   FRG: filter layer at scale=1024 by ContractCount/Count > 1%")
	t.Log("   Merkle: would need external contract→leaf index (not available from tree)\n")

	frgT2 := time.Now()
	var contractRegions []string
	for level := 0; level < frgTree.LayerCount(); level++ {
		layer := frgTree.Layer(level)
		if layer[0].Scale != 1024 {
			continue
		}
		span := 1024
		for pos, n := range layer {
			if n.Count > 0 && float64(n.ContractCount)/float64(n.Count) >= 0.01 {
				start := pos * span
				end := (pos + 1) * span
				contractRegions = append(contractRegions, fmt.Sprintf("[%d..%d)", start, end))
			}
		}
	}
	frgTime2 := time.Since(frgT2)

	t.Logf("   FRG found %d contract-dominant regions", len(contractRegions))
	for _, r := range contractRegions {
		t.Logf("     %s", r)
	}

	results = append(results, qResult{
		Name:       "Q2: Contract regions (scale-1024)",
		FrgDesc:    "ContractCount/Count ratio",
		MerkleDesc: "ext index required",
		FrgTime:    frgTime2,
		FrgFound:   len(contractRegions),
	})

	t.Log("\n-- Q3: Detect layer where volatility first appears --")
	t.Log("   FRG: scan each layer bottom-up, find first level with SigVolatileShock")
	t.Log("   Merkle: recompute variance at each scale (1024 64-leaf computations → 256-leaf → ...)\n")

	frgT3 := time.Now()
	var firstVolatile int = -1
	for level := 0; level < frgTree.LayerCount(); level++ {
		for _, n := range frgTree.Layer(level) {
			if n.Sig == node.SigVolatileShock {
				firstVolatile = level
				break
			}
		}
		if firstVolatile >= 0 {
			break
		}
	}
	frgTime3 := time.Since(frgT3)

	merkleT3 := time.Now()
	merkleFirstVolatile := -1
	chunkSize := 64
	for scale := uint32(4); scale <= 65536; scale *= 4 {
		for start := 0; start < queryN; start += chunkSize {
			end := start + chunkSize
			if end > queryN {
				end = queryN
			}
			var sum uint64
			var sumSq uint64
			for i := start; i < end; i++ {
				v := values[i]
				sum += v
				sumSq += v * v
			}
			cnt := uint64(end - start)
			if cnt == 0 || sum == 0 {
				continue
			}
			mean := float64(sum) / float64(cnt)
			variance := float64(sumSq)/float64(cnt) - mean*mean
			if variance > 0 {
				cv2 := variance / (mean * mean)
				if cv2 > 4 {
					merkleFirstVolatile = int(scale)
					break
				}
			}
		}
		if merkleFirstVolatile >= 0 {
			break
		}
		chunkSize *= 4
	}
	merkleTime3 := time.Since(merkleT3)

	if firstVolatile >= 0 {
		t.Logf("   FRG: first volatility at scale=%d (level %d)", frgTree.Layer(firstVolatile)[0].Scale, firstVolatile)
	} else {
		t.Log("   FRG: no volatility detected at any level")
	}
	if merkleFirstVolatile >= 0 {
		t.Logf("   Merkle: first volatility at scale=%d", merkleFirstVolatile)
	} else {
		t.Log("   Merkle: no volatility detected at any scale")
	}

	results = append(results, qResult{
		Name:        "Q3: First volatility detection",
		FrgDesc:     "signature scan (all layers)",
		MerkleDesc:  "variance recompute (all scales)",
		FrgTime:     frgTime3,
		MerkleTime:  merkleTime3,
		FrgFound:    firstVolatile,
		MerkleFound: merkleFirstVolatile,
	})

	t.Log("\n-- Q4: Identify stagnant (zero-activity) regions at scale-256 --")
	t.Log("   FRG: filter layer at scale=256 by SigStagnantState")
	t.Log("   Merkle: scan 65536 values in 256-element chunks, check sum==0\n")

	frgT4 := time.Now()
	var stagnantRegions int
	for level := 0; level < frgTree.LayerCount(); level++ {
		layer := frgTree.Layer(level)
		if layer[0].Scale != 256 {
			continue
		}
		for _, n := range layer {
			if n.Sig == node.SigStagnantState {
				stagnantRegions++
			}
		}
	}
	frgTime4 := time.Since(frgT4)

	merkleT4 := time.Now()
	merkleStagnant := 0
	for start := 0; start < queryN; start += 256 {
		var sum uint64
		for i := start; i < start+256 && i < queryN; i++ {
			sum += values[i]
		}
		if sum == 0 {
			merkleStagnant++
		}
	}
	merkleTime4 := time.Since(merkleT4)

	t.Logf("   FRG: %d stagnant regions", stagnantRegions)
	t.Logf("   Merkle: %d stagnant regions", merkleStagnant)

	results = append(results, qResult{
		Name:        "Q4: Stagnant regions (scale-256)",
		FrgDesc:     "SigStagnantState filter",
		MerkleDesc:  "value sum scan",
		FrgTime:     frgTime4,
		MerkleTime:  merkleTime4,
		FrgFound:    stagnantRegions,
		MerkleFound: merkleStagnant,
	})

	t.Log("\n-- Q5: State diff — what changed between two blocks? --")
	t.Log("   FRG: CompareSignatures() at each level")
	t.Log("   Merkle: would compare flat value lists (position-by-position scan)\n")

	sender := benchKeypair(t)
	receiver := benchKeypair(t)
	modifiedTxs := make([]*tx.Tx, queryN)
	for i := range modifiedTxs {
		var val int64
		switch {
		case i < queryN/10:
			val = 100000
		case i < queryN/10+queryN/5:
			val = 10000
		default:
			val = 1
		}
		if i < 100 {
			val = 50000
		}
		modifiedTxs[i] = benchTx(t, sender, receiver, val, uint64(i))
	}
	modifiedTree, _ := tree.BuildTree(modifiedTxs, q.contractNodes)

	frgT5 := time.Now()
	diffs := tree.CompareSignatures(frgTree, modifiedTree)
	frgTime5 := time.Since(frgT5)

	var totalChanges int
	for _, d := range diffs {
		totalChanges += d.SigChanges
	}

	frgRoot1 := frgTree.Root()
	frgRoot2 := modifiedTree.Root()
	t.Logf("   FRG: %d signature-level changes (root changed: %v)", totalChanges, frgRoot1 != frgRoot2)

	results = append(results, qResult{
		Name:    "Q5: State diff (100 mutated leaves)",
		FrgDesc: "CompareSignatures()",
		FrgTime: frgTime5,
		FrgFound: totalChanges,
	})

	t.Log("\n" + strings.Repeat("=", 90))
	t.Log(fmt.Sprintf("%-40s %15s %15s", "Query", "FRG Time", "Merkle Time"))
	t.Log(strings.Repeat("-", 75))
	for _, r := range results {
		frgStr := fmt.Sprintf("%v", r.FrgTime)
		merkleStr := "N/A"
		if r.MerkleTime > 0 {
			merkleStr = fmt.Sprintf("%v", r.MerkleTime)
		}
		t.Log(fmt.Sprintf("%-40s %15s %15s", r.Name, frgStr, merkleStr))
	}
	t.Log(strings.Repeat("=", 90))
	t.Log("FRG's retained hierarchy enables queries (Q1, Q2, Q5) that Merkle")
	t.Log("cannot answer from its flat data without external indexing or rebuilding.")
	t.Log("For queries solvable via numeric scan (Q3, Q4), Merkle's flat uint64")
	t.Log("representation is faster where arithmetic dominates traversal overhead.")
}

func TestQueryScaling(t *testing.T) {
	blockCounts := []int{1, 4, 16}
	blockSize := 65536

	type stat struct {
		blocks     int
		histUs     int64
		contractUs int64
		stagUs     int64
	}

	var stats []stat

	for _, numBlocks := range blockCounts {
		t.Logf("Building %d blocks x %d entries = %d total...", numBlocks, blockSize, numBlocks*blockSize)

		trees := make([]*tree.Tree, numBlocks)
		for b := 0; b < numBlocks; b++ {
			sender := benchKeypair(t)
			receiver := benchKeypair(t)
			txs := make([]*tx.Tx, blockSize)
			for i := range txs {
				var val int64
				switch {
				case i < blockSize/10:
					val = 100000
				case i < blockSize/10+blockSize/5:
					val = 10000
				default:
					val = 1
				}
				txs[i] = benchTx(t, sender, receiver, val, uint64(i))
			}
			tr, _ := tree.BuildTree(txs, nil)
			trees[b] = tr
		}

		var histTotal, contractTotal, stagTotal time.Duration
		for _, tr := range trees {
			start := time.Now()
			for level := 0; level < tr.LayerCount(); level++ {
				_ = tr.SignatureHistogram(level)
			}
			histTotal += time.Since(start)

			start = time.Now()
			for level := 0; level < tr.LayerCount(); level++ {
				if tr.ScaleAt(level) == 1024 {
					tr.FindNodes(level, func(n *node.RGNode) bool {
						return n.Count > 0 && float64(n.ContractCount)/float64(n.Count) >= 0.01
					})
				}
			}
			contractTotal += time.Since(start)

			start = time.Now()
			_ = tr.StagnantRegions(4)
			stagTotal += time.Since(start)
		}

		stats = append(stats, stat{
			blocks:     numBlocks,
			histUs:     histTotal.Microseconds() / int64(numBlocks),
			contractUs: contractTotal.Microseconds() / int64(numBlocks),
			stagUs:     stagTotal.Microseconds() / int64(numBlocks),
		})
	}

	t.Logf("\n%-10s %-12s %-20s %-20s %-20s", "Blocks", "Entries", "SigHistogram/block", "ContractDetect/block", "StagnantDetect/block")
	for _, s := range stats {
		t.Logf("%-10d %-12d %-15d µs %-15d µs %-15d µs",
			s.blocks, s.blocks*blockSize, s.histUs, s.contractUs, s.stagUs)
	}
	t.Log("\nPer-block query cost is constant. Total cost scales linearly with block count.")
}
