package e2e_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/imattau/frg/core/p2p"
	"github.com/imattau/frg/core/tx"
)

func TestP2PFloodStress(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}

	const nodeCount = 5
	const txPerNode = 500
	const totalTxs = nodeCount * txPerNode

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// 1. Setup Nodes
	nodes := make([]*p2p.Node, nodeCount)
	for i := 0; i < nodeCount; i++ {
		kp := makeKeypair(t)
		n, err := p2p.New(ctx, kp, p2p.Config{ListenAddr: "/ip4/127.0.0.1/tcp/0"})
		if err != nil {
			t.Fatalf("node %d init failed: %v", i, err)
		}
		defer n.Close()
		nodes[i] = n
	}

	// 2. Connect Nodes (Mesh Topology)
	// Node 0 acts as a bootstrap node. Others connect to it.
	bootstrapAddr := nodes[0].Addrs()
	for i := 1; i < nodeCount; i++ {
		if err := nodes[i].Connect(ctx, bootstrapAddr); err != nil {
			t.Fatalf("node %d failed to connect to bootstrap: %v", i, err)
		}
	}

	// Allow mesh to form via GossipSub
	time.Sleep(2 * time.Second)

	// 3. Pre-generate Transactions
	// Generating valid signatures takes time, do it before the broadcast timer starts.
	type txBatch struct {
		sender   *p2p.Node
		payloads []*tx.Tx
	}
	batches := make([]txBatch, nodeCount)

	for i := 0; i < nodeCount; i++ {
		senderKP := makeKeypair(t)
		receiverKP := makeKeypair(t)
		batch := make([]*tx.Tx, txPerNode)
		for j := 0; j < txPerNode; j++ {
			batch[j] = makeTx(t, senderKP, receiverKP, 1, uint64(j+1))
		}
		batches[i] = txBatch{sender: nodes[i], payloads: batch}
	}

	// 4. Setup Parity Tracking
	var wg sync.WaitGroup
	wg.Add(nodeCount)

	type trackingMap struct {
		sync.Mutex
		seen map[[32]byte]struct{}
	}
	trackers := make([]*trackingMap, nodeCount)

	for i := 0; i < nodeCount; i++ {
		trackers[i] = &trackingMap{seen: make(map[[32]byte]struct{})}
		go func(idx int) {
			defer wg.Done()
			sub := nodes[idx].SubscribeTxs()
			for {
				select {
				case <-ctx.Done():
					return
				case tr, ok := <-sub:
					if !ok {
						return
					}
					id, err := tr.ID()
					if err != nil {
						t.Errorf("failed to get tx ID: %v", err)
						return
					}
					trackers[idx].Lock()
					trackers[idx].seen[id] = struct{}{}
					count := len(trackers[idx].seen)
					trackers[idx].Unlock()

					// If we hit the target, this node is done
					if count == totalTxs {
						return
					}
				}
			}
		}(i)
	}

	// 5. The Flood (Broadcast)
	start := time.Now()
	for i := 0; i < nodeCount; i++ {
		go func(batch txBatch) {
			for _, tr := range batch.payloads {
				// Ignore broadcast errors during flood (e.g. queue full), though ideally they succeed
				_ = batch.sender.BroadcastTx(tr)
				// Slight delay to prevent local socket buffer overflow before GossipSub can route
				time.Sleep(5 * time.Millisecond)
			}
		}(batches[i])
	}

	// 6. Wait for Parity
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-ctx.Done():
		t.Errorf("timeout waiting for mempool parity")
		for i := 0; i < nodeCount; i++ {
			trackers[i].Lock()
			t.Logf("Node %d received %d/%d txs", i, len(trackers[i].seen), totalTxs)
			trackers[i].Unlock()
		}
		t.FailNow()
	case <-done:
		duration := time.Since(start)
		tps := float64(totalTxs) / duration.Seconds()
		t.Logf("Successfully achieved mempool parity across %d nodes in %v (%.2f TPS)", nodeCount, duration, tps)
	}
}
