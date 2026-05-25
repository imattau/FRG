package e2e_test

import (
	"context"
	"math/big"
	"path/filepath"
	"testing"
	"time"

	"github.com/imattau/frg/core/blockloop"
	"github.com/imattau/frg/core/consensus"
	"github.com/imattau/frg/core/keys"
	"github.com/imattau/frg/core/ledger"
	"github.com/imattau/frg/core/p2p"
	"github.com/imattau/frg/core/staking"
	"github.com/imattau/frg/core/statemachine"
	bolt "go.etcd.io/bbolt"
)

type nodeStack struct {
	kp      *keys.Keypair
	db      *bolt.DB
	ledger  *ledger.Ledger
	staking *staking.Store
	sm      *statemachine.StateMachine
	p2p     *p2p.Node
	bl      *blockloop.BlockLoop
	engine  *consensus.Engine
}

func newNodeStack(t *testing.T, ctx context.Context) *nodeStack {
	t.Helper()
	dir := t.TempDir()
	db, err := bolt.Open(filepath.Join(dir, "frg.db"), 0600, nil)
	if err != nil {
		t.Fatalf("bolt.Open: %v", err)
	}

	kp, err := keys.GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair: %v", err)
	}

	l, err := ledger.New(db)
	if err != nil {
		t.Fatalf("ledger.New: %v", err)
	}

	s, err := staking.New(db, l)
	if err != nil {
		t.Fatalf("staking.New: %v", err)
	}

	sm, err := statemachine.New(db, l, s)
	if err != nil {
		t.Fatalf("statemachine.New: %v", err)
	}

	p2pNode, err := p2p.New(ctx, kp, p2p.Config{ListenAddr: "/ip4/127.0.0.1/tcp/0"})
	if err != nil {
		t.Fatalf("p2p.New: %v", err)
	}

	bl := blockloop.New(kp, p2pNode)
	engine := consensus.New(kp, s, sm, p2pNode, bl, consensus.TimeoutConfig{
		Propose:   1 * time.Second,
		Prevote:   1 * time.Second,
		Precommit: 1 * time.Second,
	})

	return &nodeStack{
		kp:      kp,
		db:      db,
		ledger:  l,
		staking: s,
		sm:      sm,
		p2p:     p2pNode,
		bl:      bl,
		engine:  engine,
	}
}

func (n *nodeStack) close() {
	n.engine.Stop()
	n.bl.Stop()
	n.p2p.Close()
	n.db.Close()
}

func TestMultiNodeConsensus(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping multi-node test in short mode")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const numNodes = 4
	nodes := make([]*nodeStack, numNodes)
	for i := 0; i < numNodes; i++ {
		nodes[i] = newNodeStack(t, ctx)
	}
	defer func() {
		for _, n := range nodes {
			n.close()
		}
	}()

	// Genesis setup: seed + bond all 4 on all nodes
	for _, target := range nodes {
		for _, v := range nodes {
			if err := target.ledger.Seed(v.kp.PublicKey, big.NewInt(9000)); err != nil {
				t.Fatalf("ledger.Seed: %v", err)
			}
			if err := target.staking.Bond(v.kp.PublicKey, big.NewInt(1000), 0); err != nil {
				t.Fatalf("staking.Bond: %v", err)
			}
		}
	}

	// Full mesh connect
	for i := 0; i < numNodes; i++ {
		for j := 0; j < numNodes; j++ {
			if i == j {
				continue
			}
			if err := nodes[i].p2p.Connect(ctx, nodes[j].p2p.Addrs()); err != nil {
				t.Fatalf("p2p.Connect: %v", err)
			}
		}
	}

	time.Sleep(1 * time.Second) // let GossipSub mesh form

	// Start all
	for _, n := range nodes {
		if err := n.bl.Start(ctx); err != nil {
			t.Fatalf("bl.Start: %v", err)
		}
		go func(stack *nodeStack) {
			if err := stack.engine.Start(ctx); err != nil {
				t.Logf("engine.Start exited with: %v", err)
			}
		}(n)
	}

	// Submit tx to node 0
	rcvr, _ := keys.GenerateKeypair()
	tx := makeTx(t, nodes[0].kp, rcvr, 10, 1)
	nodes[0].bl.Enqueue(tx)

	// Wait for consensus
	timeout := time.After(30 * time.Second)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-timeout:
			for i, n := range nodes {
				h, _ := n.sm.CurrentHeight()
				t.Logf("node %d height: %d", i, h)
			}
			t.Fatal("consensus did not commit a block within timeout")
		case <-ticker.C:
			allReached := true
			for _, n := range nodes {
				h, _ := n.sm.CurrentHeight()
				if h < 1 {
					allReached = false
					break
				}
			}
			if allReached {
				// Verify all nodes agree on state root at height 1
				var root [32]byte
				for i, n := range nodes {
					r, _ := n.sm.CurrentStateRoot()
					if i == 0 {
						root = r
					} else if r != root {
						t.Fatalf("node %d state root mismatch at height 1", i)
					}
				}
				return // Success
			}
		}
	}
}
