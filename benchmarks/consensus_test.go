package benchmarks

import (
	"context"
	"fmt"
	"math/big"
	"path/filepath"
	"testing"
	"time"

	"github.com/imattau/frg/core/blockloop"
	"github.com/imattau/frg/core/consensus"
	"github.com/imattau/frg/core/denom"
	"github.com/imattau/frg/core/keys"
	"github.com/imattau/frg/core/ledger"
	"github.com/imattau/frg/core/p2p"
	"github.com/imattau/frg/core/staking"
	"github.com/imattau/frg/core/statemachine"
	bolt "go.etcd.io/bbolt"
)

type benchNodeStack struct {
	kp      *keys.Keypair
	db      *bolt.DB
	ledger  *ledger.Ledger
	staking *staking.Store
	sm      *statemachine.StateMachine
	p2p     *p2p.Node
	bl      *blockloop.BlockLoop
	engine  *consensus.Engine
}

func newBenchNodeStack(tb testing.TB, ctx context.Context) *benchNodeStack {
	tb.Helper()
	dir := tb.TempDir()
	db, err := bolt.Open(filepath.Join(dir, "frg.db"), 0600, nil)
	if err != nil {
		tb.Fatalf("bolt.Open: %v", err)
	}

	kp, err := keys.GenerateKeypair()
	if err != nil {
		tb.Fatalf("GenerateKeypair: %v", err)
	}

	l, err := ledger.New(db)
	if err != nil {
		tb.Fatalf("ledger.New: %v", err)
	}

	s, err := staking.New(db, l)
	if err != nil {
		tb.Fatalf("staking.New: %v", err)
	}

	sm, err := statemachine.New(db, l, s)
	if err != nil {
		tb.Fatalf("statemachine.New: %v", err)
	}

	p2pNode, err := p2p.New(ctx, kp, p2p.Config{ListenAddr: "/ip4/127.0.0.1/tcp/0"})
	if err != nil {
		tb.Fatalf("p2p.New: %v", err)
	}

	bl := blockloop.New(kp, p2pNode)
	engine := consensus.New(kp, s, sm, p2pNode, bl, consensus.TimeoutConfig{
		ProposeDelay: 100 * time.Millisecond,
		Propose:      1 * time.Second,
		Prevote:      1 * time.Second,
		Precommit:    1 * time.Second,
	})

	return &benchNodeStack{
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

func (n *benchNodeStack) close() {
	n.engine.Stop()
	n.bl.Stop()
	n.p2p.Close()
	n.db.Close()
}

func BenchmarkConsensusScaling(b *testing.B) {
	if testing.Short() {
		b.Skip("skipping consensus scaling in short mode")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	targetHeights := map[int]int{
		3:  3,
		4:  3,
		7:  3,
		10: 3,
	}

	for numNodes, targetHeight := range targetHeights {
		b.Run(fmt.Sprintf("nodes=%d", numNodes), func(b *testing.B) {
			nodes := make([]*benchNodeStack, numNodes)
			for i := 0; i < numNodes; i++ {
				nodes[i] = newBenchNodeStack(b, ctx)
			}
			defer func() {
				for _, n := range nodes {
					n.close()
				}
			}()

			for _, target := range nodes {
				totalSupply := big.NewInt(0)
				for _, v := range nodes {
					balance := new(big.Int).Mul(big.NewInt(9000), denom.QuantaPerFRG)
					target.ledger.Seed(v.kp.PublicKey, balance)
					totalSupply.Add(totalSupply, balance)
					if err := target.staking.Bond(v.kp.PublicKey, new(big.Int).Mul(big.NewInt(1000), denom.QuantaPerFRG), 0); err != nil {
						b.Fatalf("bond benchmark validator: %v", err)
					}
				}
				if err := target.sm.Update(func(btx *bolt.Tx) error {
					return target.sm.SetTotalSupplyTx(btx, totalSupply)
				}); err != nil {
					b.Fatalf("set benchmark total supply: %v", err)
				}
			}

			for i := 0; i < numNodes; i++ {
				for j := 0; j < numNodes; j++ {
					if i == j {
						continue
					}
					if err := nodes[i].p2p.Connect(ctx, nodes[j].p2p.Addrs()); err != nil {
						b.Fatalf("connect benchmark nodes: %v", err)
					}
				}
			}

			time.Sleep(2 * time.Second)

			for _, n := range nodes {
				n.bl.Start(ctx)
				go func(stack *benchNodeStack) {
					stack.engine.Start(ctx)
				}(n)
			}

			time.Sleep(500 * time.Millisecond)

			sender := benchKeypair(b)
			receiver := benchKeypair(b)
			for _, n := range nodes {
				if err := n.ledger.Seed(sender.PublicKey, new(big.Int).Mul(big.NewInt(9000), denom.QuantaPerFRG)); err != nil {
					b.Fatalf("seed benchmark sender: %v", err)
				}
			}

			b.ResetTimer()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				tx := benchTx(b, sender, receiver, 10, uint64(i+1))
				nodes[0].bl.Enqueue(tx)

				timeout := time.After(60 * time.Second)
				ticker := time.NewTicker(200 * time.Millisecond)
				reached := false
				for !reached {
					select {
					case <-timeout:
						for i, n := range nodes {
							h, _ := n.sm.CurrentHeight()
							b.Logf("node %d: height=%d peers=%d consensus=%+v mempool=%d", i, h, n.p2p.PeerCount(), n.engine.Status(), n.bl.Len())
						}
						b.Fatalf("consensus timeout at height %d for %d nodes", targetHeight, numNodes)
					case <-ticker.C:
						allReached := true
						for _, n := range nodes {
							h, _ := n.sm.CurrentHeight()
							if h < uint64(targetHeight) {
								allReached = false
								break
							}
						}
						if allReached {
							reached = true
						}
					}
				}
				ticker.Stop()
			}
		})
	}
}
