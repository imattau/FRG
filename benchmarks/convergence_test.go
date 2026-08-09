package benchmarks

import (
	"context"
	"math/big"
	"testing"
	"time"

	"github.com/imattau/frg/core/keys"
)

func BenchmarkConvergence(b *testing.B) {
	if testing.Short() {
		b.Skip("skipping convergence benchmark in short mode")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const numNodes = 5

	b.Run("HappyPath", func(b *testing.B) {
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
			for _, v := range nodes {
				target.ledger.Seed(v.kp.PublicKey, big.NewInt(9000))
				target.staking.Bond(v.kp.PublicKey, big.NewInt(1000), 0)
			}
		}

		for i := 0; i < numNodes; i++ {
			for j := 0; j < numNodes; j++ {
				if i == j {
					continue
				}
				nodes[i].p2p.Connect(ctx, nodes[j].p2p.Addrs())
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

		b.ResetTimer()
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			txs := make([]*struct{ kp *keys.Keypair; tx *testing.T }, numNodes)
			for j := 0; j < numNodes; j++ {
				kp, _ := keys.GenerateKeypair()
				nodes[j].bl.Enqueue(benchTx(b, kp, benchKeypair(b), 1, uint64(j+1+i*numNodes)))
				_ = txs
			}

			timeout := time.After(60 * time.Second)
			ticker := time.NewTicker(200 * time.Millisecond)
			defer ticker.Stop()

			reached := false
			for !reached {
				select {
				case <-timeout:
					b.Fatalf("convergence timeout")
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
						var stateRoot [32]byte
						for j, n := range nodes {
							root, _ := n.sm.CurrentStateRoot()
							if j == 0 {
								stateRoot = root
							} else if root != stateRoot {
								b.Fatalf("state root mismatch at node %d", j)
							}
						}
						reached = true
					}
				}
			}
			_ = sender
			_ = receiver
		}
	})
}
