package p2p_test

import (
	"context"
	"math/big"
	"testing"
	"time"

	"github.com/imattau/frg/core/keys"
	"github.com/imattau/frg/core/p2p"
	"github.com/imattau/frg/core/tx"
)

func TestNodeStartStop(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	kp, err := keys.GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair: %v", err)
	}

	cfg := p2p.Config{
		ListenAddr: "/ip4/127.0.0.1/tcp/0",
		EnableMDNS: false,
	}

	node, err := p2p.New(ctx, kp, cfg)
	if err != nil {
		t.Fatalf("p2p.New: %v", err)
	}

	// Give it a moment to start
	time.Sleep(100 * time.Millisecond)

	if err := node.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestTwoNodeConnect(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	kp1, _ := keys.GenerateKeypair()
	kp2, _ := keys.GenerateKeypair()

	n1, err := p2p.New(ctx, kp1, p2p.Config{ListenAddr: "/ip4/127.0.0.1/tcp/0"})
	if err != nil {
		t.Fatalf("n1: %v", err)
	}
	defer n1.Close()

	n2, err := p2p.New(ctx, kp2, p2p.Config{ListenAddr: "/ip4/127.0.0.1/tcp/0"})
	if err != nil {
		t.Fatalf("n2: %v", err)
	}
	defer n2.Close()

	// Connect n2 to n1 directly
	n1Addrs := n1.Addrs()
	if err := n2.Connect(ctx, n1Addrs); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	time.Sleep(200 * time.Millisecond)

	if n1.PeerCount() == 0 {
		t.Fatal("n1 has no peers")
	}
	if n2.PeerCount() == 0 {
		t.Fatal("n2 has no peers")
	}
}

func TestTxGossip(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	kp1, _ := keys.GenerateKeypair()
	kp2, _ := keys.GenerateKeypair()
	senderKP, _ := keys.GenerateKeypair()
	receiverKP, _ := keys.GenerateKeypair()

	n1, _ := p2p.New(ctx, kp1, p2p.Config{ListenAddr: "/ip4/127.0.0.1/tcp/0"})
	defer n1.Close()
	n2, _ := p2p.New(ctx, kp2, p2p.Config{ListenAddr: "/ip4/127.0.0.1/tcp/0"})
	defer n2.Close()

	n2.Connect(ctx, n1.Addrs())
	time.Sleep(500 * time.Millisecond) // let GossipSub mesh form

	tr := &tx.Tx{
		Type:           tx.TxTypeTransfer,
		Sender:         "alice",
		Receiver:       "bob",
		Value:          big.NewInt(100),
		Nonce:          1,
		SenderPubKey:   senderKP.PublicKey,
		ReceiverPubKey: receiverKP.PublicKey,
	}
	rs, _ := tr.SignReceiver(receiverKP)
	tr.ReceiverSig = rs
	ss, _ := tr.SignSender(senderKP)
	tr.SenderSig = ss

	// n1 broadcasts, n2 should receive
	if err := n1.BroadcastTx(tr); err != nil {
		t.Fatalf("BroadcastTx: %v", err)
	}

	select {
	case received := <-n2.SubscribeTxs():
		if received.Sender != tr.Sender {
			t.Fatalf("received wrong sender: got %q want %q", received.Sender, tr.Sender)
		}
		if received.Value.Cmp(tr.Value) != 0 {
			t.Fatalf("received wrong value: got %s want %s", received.Value, tr.Value)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for tx gossip")
	}
}
