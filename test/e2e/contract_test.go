package e2e_test

import (
	"context"
	"math/big"
	"testing"
	"time"

	"github.com/imattau/frg/core/contract"
	"github.com/imattau/frg/core/tx"
)

// minimalWasm is a valid WASM module exporting "init" and "call" (both no-ops).
// Hand-crafted binary: 1 type ()→(), 2 funcs, 2 exports, 2 code bodies.
var contractWasm = []byte{
	0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00,
	0x01, 0x04, 0x01, 0x60, 0x00, 0x00,
	0x03, 0x03, 0x02, 0x00, 0x00,
	0x07, 0x0F, 0x02,
	0x04, 0x69, 0x6E, 0x69, 0x74, 0x00, 0x00,
	0x04, 0x63, 0x61, 0x6C, 0x6C, 0x00, 0x01,
	0x0A, 0x07, 0x02, 0x02, 0x00, 0x0B, 0x02, 0x00, 0x0B,
}

func TestContractE2E(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	nodes := []*nodeStack{newNodeStack(t, ctx), newNodeStack(t, ctx)}
	defer func() {
		for _, n := range nodes {
			n.close()
		}
	}()

	// Genesis: seed + bond both validators
	for _, target := range nodes {
		initializeNodeGenesis(t, target, nodes, q(9000), q(1000))
	}

	// Connect mesh
	for i := 0; i < len(nodes); i++ {
		for j := 0; j < len(nodes); j++ {
			if i == j {
				continue
			}
			if err := nodes[i].p2p.Connect(ctx, nodes[j].p2p.Addrs()); err != nil {
				t.Fatal(err)
			}
		}
	}
	time.Sleep(500 * time.Millisecond)

	// Start consensus
	for _, n := range nodes {
		n.bl.Start(ctx)
		go func(s *nodeStack) {
			if err := s.engine.Start(ctx); err != nil {
				t.Logf("engine: %v", err)
			}
		}(n)
	}

	// Deploy contract and call it in the same block
	deployTx := &tx.Tx{
		Type:         tx.TxTypeContractDeploy,
		Sender:       "alice",
		Receiver:     "contract",
		Value:        big.NewInt(0),
		Nonce:        1,
		SenderPubKey: nodes[0].kp.PublicKey,
		WasmBytes:    contractWasm,
	}
	sig, _ := deployTx.SignSender(nodes[0].kp)
	deployTx.SenderSig = sig

	contractAddr := contract.ContractAddr(nodes[0].kp.PublicKey, 1)
	t.Logf("contract address: %x", contractAddr)

	callTx := &tx.Tx{
		Type:           tx.TxTypeContractCall,
		Sender:         "alice",
		Receiver:       "contract",
		Value:          big.NewInt(0),
		Nonce:          2,
		SenderPubKey:   nodes[0].kp.PublicKey,
		ReceiverPubKey: contractAddr,
		CallData:       []byte("call"),
	}
	sig2, _ := callTx.SignSender(nodes[0].kp)
	callTx.SenderSig = sig2

	nodes[0].bl.Enqueue(deployTx)
	nodes[0].bl.Enqueue(callTx)

	if !waitForHeight(t, nodes, 1, 15*time.Second) {
		t.Fatal("consensus did not produce block 1 (contract deploy + call)")
	}
	t.Log("block 1 committed (contract deploy + call)")

	r1, _ := nodes[0].sm.CurrentStateRoot()
	r2, _ := nodes[1].sm.CurrentStateRoot()
	if r1 != r2 {
		t.Fatalf("state root mismatch: %x vs %x", r1, r2)
	}
	t.Logf("state root agreed: %x", r1)
}

func waitForHeight(t *testing.T, nodes []*nodeStack, target uint64, timeout time.Duration) bool {
	t.Helper()
	deadline := time.After(timeout)
	ticker := time.NewTicker(300 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-deadline:
			for i, n := range nodes {
				h, _ := n.sm.CurrentHeight()
				t.Logf("node %d height: %d", i, h)
			}
			return false
		case <-ticker.C:
			all := true
			for _, n := range nodes {
				h, _ := n.sm.CurrentHeight()
				if h < target {
					all = false
					break
				}
			}
			if all {
				return true
			}
		}
	}
}
