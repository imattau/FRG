package e2e_test

import (
	"context"
	"testing"
	"time"

	"github.com/imattau/frg/core/p2p"
)

func TestP2PSimplePropagate(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	kp0 := makeKeypair(t)
	kp1 := makeKeypair(t)
	n0, _ := p2p.New(ctx, kp0, p2p.Config{ListenAddr: "/ip4/127.0.0.1/tcp/0"})
	n1, _ := p2p.New(ctx, kp1, p2p.Config{ListenAddr: "/ip4/127.0.0.1/tcp/0"})
	defer n0.Close()
	defer n1.Close()

	_ = n1.Connect(ctx, n0.Addrs())
	time.Sleep(2 * time.Second)

	sub1 := n1.SubscribeTxs()

	senderKP := makeKeypair(t)
	receiverKP := makeKeypair(t)
	tr := makeTx(t, senderKP, receiverKP, 1, 1)
	if err := n0.BroadcastTx(tr); err != nil {
		t.Fatalf("broadcast failed: %v", err)
	}

	select {
	case got := <-sub1:
		t.Logf("n1 received tx: nonce=%d", got.Nonce)
	case <-time.After(10 * time.Second):
		t.Fatal("n1 never received the tx")
	}
}
