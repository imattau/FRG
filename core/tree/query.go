package tree

import "github.com/imattau/frg/core/node"

func (t *Tree) ScaleAt(level int) uint32 {
	return t.layers[level][0].Scale
}

func (t *Tree) NodeCount(level int) int {
	return len(t.layers[level])
}

func (t *Tree) SignatureCount(level int, sig node.Signature) int {
	count := 0
	for _, n := range t.layers[level] {
		if n.Sig == sig {
			count++
		}
	}
	return count
}

func (t *Tree) SignatureHistogram(level int) map[node.Signature]int {
	h := make(map[node.Signature]int)
	for _, n := range t.layers[level] {
		h[n.Sig]++
	}
	return h
}

func (t *Tree) FindNodes(level int, predicate func(n *node.RGNode) bool) []int {
	var indices []int
	for i, n := range t.layers[level] {
		if predicate(n) {
			indices = append(indices, i)
		}
	}
	return indices
}

func (t *Tree) ContractDensity(level int) (nodes int, contractNodes int, ratio float64) {
	for _, n := range t.layers[level] {
		if n.Count > 0 {
			nodes++
			contractNodes += int(n.ContractCount)
		}
	}
	if nodes > 0 {
		ratio = float64(contractNodes) / float64(nodes)
	}
	return
}

type LevelDiff struct {
	Level            int
	Scale            uint32
	SigChanges       int
	TotalNodes        int
	AddedNodes       int
	RemovedNodes     int
}

func CompareSignatures(a, b *Tree) []LevelDiff {
	maxLevels := a.LayerCount()
	if b.LayerCount() < maxLevels {
		maxLevels = b.LayerCount()
	}

	var diffs []LevelDiff
	for level := 0; level < maxLevels; level++ {
		la := a.layers[level]
		lb := b.layers[level]

		d := LevelDiff{
			Level:      level,
			Scale:      la[0].Scale,
			TotalNodes: len(la),
		}

		minLen := len(la)
		if len(lb) < minLen {
			minLen = len(lb)
		}

		for i := 0; i < minLen; i++ {
			if la[i].Sig != lb[i].Sig {
				d.SigChanges++
			}
		}

		if len(la) > len(lb) {
			d.RemovedNodes = len(la) - len(lb)
		} else if len(lb) > len(la) {
			d.AddedNodes = len(lb) - len(la)
		}

		diffs = append(diffs, d)
	}
	return diffs
}

func (t *Tree) VolatilityRegions(level int) []int {
	return t.FindNodes(level, func(n *node.RGNode) bool {
		return n.Sig == node.SigVolatileShock
	})
}

func (t *Tree) StagnantRegions(level int) []int {
	return t.FindNodes(level, func(n *node.RGNode) bool {
		return n.Sig == node.SigStagnantState
	})
}

func (t *Tree) NodeAt(level, index int) *node.RGNode {
	return t.layers[level][index]
}
