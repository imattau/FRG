package e2e_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/imattau/frg/core/p2p"
	"github.com/imattau/frg/core/tx"
)

func TestP2PBatchStress(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}

	const nodeCount = 5
	const batchSize = 20
	const batchCount = 50
	const txPerNode = batchSize * batchCount
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

	// 2. Connect Nodes (Full Mesh)
	for i := 0; i < nodeCount; i++ {
		for j := i + 1; j < nodeCount; j++ {
			if err := nodes[i].Connect(ctx, nodes[j].Addrs()); err != nil {
				t.Fatalf("node %d failed to connect to node %d: %v", i, j, err)
			}
		}
	}

	// Allow mesh to form
	time.Sleep(2 * time.Second)

	// 3. Pre-generate Transactions
	type batchSet struct {
		sender   *p2p.Node
		payloads [][]*tx.Tx
	}
	sets := make([]batchSet, nodeCount)

	for i := 0; i < nodeCount; i++ {
		senderKP := makeKeypair(t)
		receiverKP := makeKeypair(t)
		batchPayloads := make([][]*tx.Tx, batchCount)
		for b := 0; b < batchCount; b++ {
			innerBatch := make([]*tx.Tx, batchSize)
			for j := 0; j < batchSize; j++ {
				innerBatch[j] = makeTx(t, senderKP, receiverKP, 1, uint64(b*batchSize+j+1))
			}
			batchPayloads[b] = innerBatch
		}
		sets[i] = batchSet{sender: nodes[i], payloads: batchPayloads}
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
						continue
					}
					trackers[idx].Lock()
					trackers[idx].seen[id] = struct{}{}
					count := len(trackers[idx].seen)
					trackers[idx].Unlock()

					if count == totalTxs {
						return
					}
				}
			}
		}(i)
	}

	// 5. The Batch Flood
	start := time.Now()
	for i := 0; i < nodeCount; i++ {
		go func(set batchSet) {
			for _, b := range set.payloads {
				_ = set.sender.BroadcastBatch(b)
				// Increase delay to ensure stable processing
				time.Sleep(50 * time.Millisecond)
			}
		}(sets[i])
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
		t.Logf("Successfully achieved mempool parity for %d txs across %d nodes using batching in %v (%.2f TPS)", totalTxs, nodeCount, duration, tps)
	}
}
