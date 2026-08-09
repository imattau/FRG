package e2e_test

import (
	"context"
	"math/big"
	"testing"
	"time"
)

func TestMultiBlockConsensus(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	nodes := []*nodeStack{newNodeStack(t, ctx), newNodeStack(t, ctx), newNodeStack(t, ctx)}
	defer func() {
		for _, n := range nodes {
			n.close()
		}
	}()

	for _, target := range nodes {
		for _, v := range nodes {
			target.ledger.Seed(v.kp.PublicKey, bigInt(9000))
			target.staking.Bond(v.kp.PublicKey, bigInt(1000), 0)
		}
	}

	for i := 0; i < len(nodes); i++ {
		for j := 0; j < len(nodes); j++ {
			if i == j {
				continue
			}
			nodes[i].p2p.Connect(ctx, nodes[j].p2p.Addrs())
		}
	}
	time.Sleep(1 * time.Second)

	for _, n := range nodes {
		n.bl.Start(ctx)
		go func(s *nodeStack) {
			s.engine.Start(ctx)
		}(n)
	}

	// Enqueue tx for block 1
	tx1 := makeTx(t, nodes[0].kp, nodes[1].kp, 10, 1)
	nodes[0].bl.Enqueue(tx1)

	time.Sleep(8 * time.Second)

	h, _ := nodes[0].sm.CurrentHeight()
	t.Logf("After block 1 — node-0 height: %d", h)

	// Enqueue tx for block 2
	tx2 := makeTx(t, nodes[0].kp, nodes[1].kp, 5, 2)
	nodes[0].bl.Enqueue(tx2)

	time.Sleep(8 * time.Second)

	h2, _ := nodes[0].sm.CurrentHeight()
	t.Logf("After block 2 — node-0 height: %d", h2)

	if h2 < 2 {
		t.Fatalf("expected height >= 2, got %d — multi-block consensus not advancing", h2)
	}

	// Verify all nodes agree
	for i, n := range nodes {
		ni, _ := n.sm.CurrentHeight()
		if ni < 2 {
			t.Fatalf("node %d stuck at height %d", i, ni)
		}
	}
	t.Log("all 3 nodes reached height >= 2 — multi-block consensus works")
}

func bigInt(v int64) *big.Int { return new(big.Int).SetInt64(v) }
