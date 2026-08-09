package e2e_test

import (
	"context"
	"math/big"
	"testing"
	"time"

	"github.com/imattau/frg/core/contract"
	"github.com/imattau/frg/core/tx"
)

var contractWasm7 = []byte{
	0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00,
	0x01, 0x04, 0x01, 0x60, 0x00, 0x00,
	0x03, 0x03, 0x02, 0x00, 0x00,
	0x07, 0x0F, 0x02,
	0x04, 0x69, 0x6E, 0x69, 0x74, 0x00, 0x00,
	0x04, 0x63, 0x61, 0x6C, 0x6C, 0x00, 0x01,
	0x0A, 0x07, 0x02, 0x02, 0x00, 0x0B, 0x02, 0x00, 0x0B,
}

func TestSevenNodeDevnetContracts(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping 7-node devnet test in short mode")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	numNodes := 7
	nodes := make([]*nodeStack, numNodes)
	for i := 0; i < numNodes; i++ {
		nodes[i] = newNodeStack(t, ctx)
	}
	defer func() {
		for _, n := range nodes {
			n.close()
		}
	}()

	// Genesis setup: seed + bond all validators (all accounts start with 9000 balance, 1000 bonded)
	for _, target := range nodes {
		initializeNodeGenesis(t, target, nodes, big.NewInt(9000), big.NewInt(1000))
	}

	// Full mesh connect
	for i := 0; i < numNodes; i++ {
		for j := 0; j < numNodes; j++ {
			if i == j {
				continue
			}
			nodes[i].p2p.Connect(ctx, nodes[j].p2p.Addrs())
		}
	}
	time.Sleep(2 * time.Second)

	// Start all engines
	for _, n := range nodes {
		n.bl.Start(ctx)
		go func(s *nodeStack) {
			s.engine.Start(ctx)
		}(n)
	}

	// Wait for first empty block
	if !waitForHeight(t, nodes, 1, 15*time.Second) {
		t.Fatal("block 1 not committed")
	}
	t.Logf("Block 1 committed — all 7 nodes at height 1, mempool=0")

	// Deploy a contract via node 0
	kp := nodes[0].kp
	deployTx := &tx.Tx{
		Type:         tx.TxTypeContractDeploy,
		Sender:       "test",
		Receiver:     "contract",
		Value:        big.NewInt(0),
		Nonce:        1,
		SenderPubKey: kp.PublicKey,
		WasmBytes:    contractWasm7,
	}
	sig, _ := deployTx.SignSender(kp)
	deployTx.SenderSig = sig
	nodes[0].bl.Enqueue(deployTx)

	contractAddr := contract.ContractAddr(kp.PublicKey, 1)
	t.Logf("Contract addr: %x", contractAddr)

	// Wait for block 2 (contract deploy)
	if !waitForHeight(t, nodes, 2, 15*time.Second) {
		for i, n := range nodes {
			h, _ := n.sm.CurrentHeight()
			t.Logf("node %d height: %d", i, h)
		}
		t.Fatal("block 2 not committed (contract deploy)")
	}
	t.Log("Block 2 committed — contract deployed")

	// Call the contract
	callTx := &tx.Tx{
		Type:           tx.TxTypeContractCall,
		Sender:         "test",
		Receiver:       "contract",
		Value:          big.NewInt(0),
		Nonce:          2,
		SenderPubKey:   kp.PublicKey,
		ReceiverPubKey: contractAddr,
		CallData:       []byte("call"),
	}
	sig2, _ := callTx.SignSender(kp)
	callTx.SenderSig = sig2
	nodes[0].bl.Enqueue(callTx)

	// Wait for block 3 (contract call)
	if !waitForHeight(t, nodes, 3, 15*time.Second) {
		for i, n := range nodes {
			h, _ := n.sm.CurrentHeight()
			t.Logf("node %d height: %d", i, h)
		}
		t.Fatal("block 3 not committed (contract call)")
	}
	t.Log("Block 3 committed — contract called")

	// Verify all 7 nodes agree on state root
	var ref [32]byte
	for i, n := range nodes {
		r, _ := n.sm.CurrentStateRoot()
		if i == 0 {
			ref = r
		} else if r != ref {
			t.Errorf("node %d state root mismatch: %x vs %x", i, r, ref)
		}
		h, _ := n.sm.CurrentHeight()
		if h < 3 {
			t.Errorf("node %d stuck at height %d", i, h)
		}
	}
	t.Logf("All 7 nodes agree: height>=3, root=%x", ref)
}
