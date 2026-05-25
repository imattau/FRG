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

	// 2. Connect Nodes (Full Mesh)
	for i := 0; i < nodeCount; i++ {
		for j := i + 1; j < nodeCount; j++ {
			if err := nodes[i].Connect(ctx, nodes[j].Addrs()); err != nil {
				t.Fatalf("node %d failed to connect to node %d: %v", i, j, err)
			}
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
			batch[j] = makeTx(t, senderKP, receiverKP, 1, uint64(i*txPerNode+j+1))
		}
		batches[i] = txBatch{sender: nodes[i], payloads: batch}
	}

	// 4. Setup Parity Tracking
	// Each node must receive txs from all OTHER nodes via the channel.
	// Self-sent txs are not echoed by GossipSub; they are credited directly.
	// Target per node: txPerNode txs from each of the other (nodeCount-1) nodes.
	const crossNodeTarget = txPerNode * (nodeCount - 1)

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
			crossReceived := 0
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
					_, alreadySeen := trackers[idx].seen[id]
					trackers[idx].seen[id] = struct{}{}
					trackers[idx].Unlock()

					if !alreadySeen {
						crossReceived++
					}
					if crossReceived >= crossNodeTarget {
						return
					}
				}
			}
		}(i)
	}

	// 5. The Flood (Broadcast)
	// GossipSub does not echo messages back to the publisher, so we credit
	// self-sent txs to the local tracker immediately.
	start := time.Now()
	for i := 0; i < nodeCount; i++ {
		go func(idx int, batch txBatch) {
			for _, tr := range batch.payloads {
				id, err := tr.ID()
				if err == nil {
					trackers[idx].Lock()
					trackers[idx].seen[id] = struct{}{}
					trackers[idx].Unlock()
				}
				_ = batch.sender.BroadcastTx(tr)
				time.Sleep(5 * time.Millisecond)
			}
		}(i, batches[i])
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
			t.Logf("Node %d received %d/%d txs (self-sent+cross)", i, len(trackers[i].seen), totalTxs)
			trackers[i].Unlock()
		}
		t.FailNow()
	case <-done:
		duration := time.Since(start)
		tps := float64(totalTxs) / duration.Seconds()
		t.Logf("Successfully achieved mempool parity across %d nodes in %v (%.2f TPS)", nodeCount, duration, tps)
	}
}
