package main

import (
	"flag"
	"fmt"
	"log"
	"math/big"
	"math/rand"
	"os"
	"path/filepath"

	"github.com/imattau/frg/core/node"
	"github.com/imattau/frg/core/tree"
	"github.com/imattau/frg/core/tx"
)

type Pattern struct {
	Name string
	Gen  func(int, int) int64
}

var patterns = []Pattern{
	{"Uniform", func(i, total int) int64 { return 1000 }},
	{"SingleCluster", func(i, total int) int64 {
		if i < total/10 {
			return 100000
		}
		return 1
	}},
	{"TwoClusters", func(i, total int) int64 {
		if i < total/10 || (i >= total/2 && i < total/2+total/10) {
			return 50000
		}
		return 1
	}},
	{"Migration", func(i, total int) int64 {
		hot := (total / 10) + (i/1000)%(total/5)
		if i >= hot && i < hot+total/10 {
			return 100000
		}
		return 1
	}},
	{"Periodic", func(i, total int) int64 {
		if i%1000 < 100 {
			return 50000
		}
		return 1
	}},
	{"Random", func(i, total int) int64 {
		rng := rand.New(rand.NewSource(int64(i)))
		if rng.Float64() < 0.1 {
			return int64(rng.Float64() * 100000)
		}
		return 1
	}},
}

type LevelFeatures struct {
	Level            int
	Scale            uint32
	NodeCount        int
	AtomicPct        float64
	StagnantPct      float64
	LaminarPct       float64
	VolatilePct      float64
	ContractPct      float64
	VolatileNodeCnt  int
}

type PatternFeatures struct {
	Pattern string
	Variant int
	Seed    int64
	Levels  []LevelFeatures
	Root    [32]byte
}

func makeValueTxs(n int, pattern Pattern, seed int64, variant int) []*tx.Tx {
	rng := rand.New(rand.NewSource(seed))
	txs := make([]*tx.Tx, n)
	for i := range txs {
		val := pattern.Gen(i, n)
		if pattern.Name == "Random" && rng.Float64() < 0.1 {
			val = int64(rng.Float64() * 100000)
		}
		txs[i] = &tx.Tx{
			Type:     tx.TxTypeTransfer,
			Sender:   "sender",
			Receiver: "receiver",
			Value:    big.NewInt(val),
			Nonce:    uint64(i),
		}
	}
	return txs
}

func extractFeatures(tr *tree.Tree, pattern string, variant int, seed int64) PatternFeatures {
	pf := PatternFeatures{
		Pattern: pattern,
		Variant: variant,
		Seed:    seed,
		Root:    tr.Root(),
	}

	for level := 0; level < tr.LayerCount(); level++ {
		layer := tr.Layer(level)
		lf := LevelFeatures{
			Level:     level,
			Scale:     layer[0].Scale,
			NodeCount: len(layer),
		}

		var atomic, stagnant, laminar, volatile, contract int
		volatileCnt := 0
		for _, n := range layer {
			switch n.Sig {
			case node.SigAtomic:
				atomic++
			case node.SigStagnantState:
				stagnant++
			case node.SigLaminarFlow:
				laminar++
			case node.SigVolatileShock:
				volatile++
				volatileCnt++
			case node.SigContract:
				contract++
			}
		}

		total := float64(len(layer))
		if total > 0 {
			lf.AtomicPct = float64(atomic) / total
			lf.StagnantPct = float64(stagnant) / total
			lf.LaminarPct = float64(laminar) / total
			lf.VolatilePct = float64(volatile) / total
			lf.ContractPct = float64(contract) / total
		}
		lf.VolatileNodeCnt = volatileCnt
		pf.Levels = append(pf.Levels, lf)
	}

	return pf
}

func flattenFeatures(pf PatternFeatures) []float64 {
	vec := make([]float64, 0)
	for _, lf := range pf.Levels {
		vec = append(vec,
			lf.AtomicPct,
			lf.StagnantPct,
			lf.LaminarPct,
			lf.VolatilePct,
			lf.ContractPct,
			float64(lf.VolatileNodeCnt),
			float64(lf.NodeCount),
		)
	}
	return vec
}

func euclideanDist(a, b []float64) float64 {
	var sum float64
	for i := range a {
		d := a[i] - b[i]
		sum += d * d
	}
	return sum
}

func knnPredict(train []PatternFeatures, testVec []float64, k int) string {
	type neighbor struct {
		distance float64
		pattern  string
	}
	var neighbors []neighbor

	for _, tp := range train {
		tVec := flattenFeatures(tp)
		neighbors = append(neighbors, neighbor{
			distance: euclideanDist(testVec, tVec),
			pattern:  tp.Pattern,
		})
	}

	for i := 0; i < k; i++ {
		minIdx := i
		for j := i + 1; j < len(neighbors); j++ {
			if neighbors[j].distance < neighbors[minIdx].distance {
				minIdx = j
			}
		}
		neighbors[i], neighbors[minIdx] = neighbors[minIdx], neighbors[i]
	}

	votes := make(map[string]int)
	for i := 0; i < k; i++ {
		votes[neighbors[i].pattern]++
	}

	var best string
	var bestCount int
	for p, c := range votes {
		if c > bestCount {
			best = p
			bestCount = c
		}
	}
	return best
}

func main() {
	outDir := flag.String("out", "results/economic", "output directory")
	numVariants := flag.Int("variants", 100, "variants per pattern")
	numTx := flag.Int("tx", 65536, "transactions per tree")
	flag.Parse()

	if err := os.MkdirAll(*outDir, 0755); err != nil {
		log.Fatalf("create output dir: %v", err)
	}

	fmt.Printf("Generating %d patterns × %d variants × %d txs = %d trees...\n",
		len(patterns), *numVariants, *numTx, len(patterns)*(*numVariants))

	var allFeatures []PatternFeatures

	for patIdx, pat := range patterns {
		for v := 0; v < *numVariants; v++ {
			seed := int64(patIdx*100000 + v)
			txs := makeValueTxs(*numTx, pat, seed, v)
			tr, err := tree.BuildTree(txs, nil)
			if err != nil {
				log.Fatalf("BuildTree %s/%d: %v", pat.Name, v, err)
			}
			pf := extractFeatures(tr, pat.Name, v, seed)
			allFeatures = append(allFeatures, pf)

			if (v+1)%10 == 0 {
				fmt.Printf("  %s: %d/%d variants\n", pat.Name, v+1, *numVariants)
			}
		}
	}

	totalTrees := len(allFeatures)
	fmt.Printf("Built %d trees. Running classification...\n", totalTrees)

	shuffled := make([]PatternFeatures, totalTrees)
	copy(shuffled, allFeatures)
	rand.Shuffle(totalTrees, func(i, j int) {
		shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
	})

	splitIdx := totalTrees * 70 / 100
	train := shuffled[:splitIdx]
	test := shuffled[splitIdx:]

	maxLevel := 0
	if len(allFeatures) > 0 {
		maxLevel = len(allFeatures[0].Levels)
	}

	fmt.Printf("\nPer-level classification results:\n")
	fmt.Printf("%5s %6s %10s %10s %10s %10s\n", "Level", "Scale", "Accuracy", "Precision", "Recall", "F1")

	for level := 0; level < maxLevel; level++ {
		trainVecs := make([][]float64, len(train))
		trainLabels := make([]string, len(train))
		for i, tp := range train {
			vec := make([]float64, 7)
			lf := tp.Levels[level]
			vec[0] = lf.AtomicPct
			vec[1] = lf.StagnantPct
			vec[2] = lf.LaminarPct
			vec[3] = lf.VolatilePct
			vec[4] = lf.ContractPct
			vec[5] = float64(lf.VolatileNodeCnt)
			vec[6] = float64(lf.NodeCount)
			trainVecs[i] = vec
			trainLabels[i] = tp.Pattern
		}

		correct := 0
		confMat := make(map[string]map[string]int)
		for _, p := range patterns {
			confMat[p.Name] = make(map[string]int)
			for _, q := range patterns {
				confMat[p.Name][q.Name] = 0
			}
		}

		for _, tp := range test {
			testVec := make([]float64, 7)
			lf := tp.Levels[level]
			testVec[0] = lf.AtomicPct
			testVec[1] = lf.StagnantPct
			testVec[2] = lf.LaminarPct
			testVec[3] = lf.VolatilePct
			testVec[4] = lf.ContractPct
			testVec[5] = float64(lf.VolatileNodeCnt)
			testVec[6] = float64(lf.NodeCount)

			neighbors := make([]struct {
				dist    float64
				pattern string
			}, len(trainVecs))
			for i, tv := range trainVecs {
				var sum float64
				for j := range tv {
					d := tv[j] - testVec[j]
					sum += d * d
				}
				neighbors[i].dist = sum
				neighbors[i].pattern = trainLabels[i]
			}

			for i := 0; i < 3; i++ {
				minIdx := i
				for j := i + 1; j < len(neighbors); j++ {
					if neighbors[j].dist < neighbors[minIdx].dist {
						minIdx = j
					}
				}
				neighbors[i], neighbors[minIdx] = neighbors[minIdx], neighbors[i]
			}

			votes := make(map[string]int)
			for i := 0; i < 3; i++ {
				votes[neighbors[i].pattern]++
			}

			var predicted string
			var bestCount int
			for p, c := range votes {
				if c > bestCount {
					predicted = p
					bestCount = c
				}
			}
			confMat[tp.Pattern][predicted]++
			if predicted == tp.Pattern {
				correct++
			}
		}

		accuracy := float64(correct) / float64(len(test))

		var totalPrecision, totalRecall float64
		nPatterns := 0
		for _, p := range patterns {
			name := p.Name
			tp := confMat[name][name]
			var totalActual, totalPredicted int
			for _, q := range patterns {
				totalActual += confMat[name][q.Name]
				totalPredicted += confMat[q.Name][name]
			}
			if totalPredicted > 0 && totalActual > 0 {
				totalPrecision += float64(tp) / float64(totalPredicted)
				totalRecall += float64(tp) / float64(totalActual)
				nPatterns++
			}
		}
		var macroPrecision, macroRecall, macroF1 float64
		if nPatterns > 0 {
			macroPrecision = totalPrecision / float64(nPatterns)
			macroRecall = totalRecall / float64(nPatterns)
			if macroPrecision+macroRecall > 0 {
				macroF1 = 2 * macroPrecision * macroRecall / (macroPrecision + macroRecall)
			}
		}

		scale := uint32(1)
		if len(allFeatures) > 0 && level < len(allFeatures[0].Levels) {
			scale = allFeatures[0].Levels[level].Scale
		}
		fmt.Printf("%5d %6d %9.1f%% %9.1f%% %9.1f%% %9.1f%%\n",
			level, scale, accuracy*100, macroPrecision*100, macroRecall*100, macroF1*100)
	}

	fmt.Printf("\nConfusion matrix (root level, level %d):\n", maxLevel-1)
	fmt.Print("            ")
	for _, p := range patterns {
		fmt.Printf(" %-14s", p.Name)
	}
	fmt.Println()

	if maxLevel > 0 {
		rootLevel := maxLevel - 1
		confMat := make(map[string]map[string]int)
		for _, p := range patterns {
			confMat[p.Name] = make(map[string]int)
			for _, q := range patterns {
				confMat[p.Name][q.Name] = 0
			}
		}

		trainVecs := make([][]float64, len(train))
		trainLabels := make([]string, len(train))
		for i, tp := range train {
			vec := make([]float64, 7)
			lf := tp.Levels[rootLevel]
			vec[0] = lf.AtomicPct
			vec[1] = lf.StagnantPct
			vec[2] = lf.LaminarPct
			vec[3] = lf.VolatilePct
			vec[4] = lf.ContractPct
			vec[5] = float64(lf.VolatileNodeCnt)
			vec[6] = float64(lf.NodeCount)
			trainVecs[i] = vec
			trainLabels[i] = tp.Pattern
		}

		for _, tp := range test {
			testVec := make([]float64, 7)
			lf := tp.Levels[rootLevel]
			testVec[0] = lf.AtomicPct
			testVec[1] = lf.StagnantPct
			testVec[2] = lf.LaminarPct
			testVec[3] = lf.VolatilePct
			testVec[4] = lf.ContractPct
			testVec[5] = float64(lf.VolatileNodeCnt)
			testVec[6] = float64(lf.NodeCount)

			neighbors := make([]struct {
				dist    float64
				pattern string
			}, len(trainVecs))
			for i, tv := range trainVecs {
				var sum float64
				for j := range tv {
					d := tv[j] - testVec[j]
					sum += d * d
				}
				neighbors[i].dist = sum
				neighbors[i].pattern = trainLabels[i]
			}

			for i := 0; i < 3; i++ {
				minIdx := i
				for j := i + 1; j < len(neighbors); j++ {
					if neighbors[j].dist < neighbors[minIdx].dist {
						minIdx = j
					}
				}
				neighbors[i], neighbors[minIdx] = neighbors[minIdx], neighbors[i]
			}

			votes := make(map[string]int)
			for i := 0; i < 3; i++ {
				votes[neighbors[i].pattern]++
			}

			var predicted string
			var bestCount int
			for p, c := range votes {
				if c > bestCount {
					predicted = p
					bestCount = c
				}
			}
			confMat[tp.Pattern][predicted]++
		}

		for _, p := range patterns {
			fmt.Printf("%-12s", p.Name)
			for _, q := range patterns {
				fmt.Printf(" %-14d", confMat[p.Name][q.Name])
			}
			fmt.Println()
		}
	}

	summaryPath := filepath.Join(*outDir, "summary.json")
	_ = summaryPath
	fmt.Printf("\nResults written to %s\n", *outDir)
}
