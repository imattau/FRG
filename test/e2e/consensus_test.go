package e2e_test

import (
	"context"
	"testing"
	"time"

	"github.com/imattau/frg/core/leader"
	"github.com/imattau/frg/core/p2p"
)

func TestLeaderElectionDeterminism(t *testing.T) {
	h := newHarness(t)
	validators := make([][32]byte, 4)
	for i := range validators {
		kp := makeKeypair(t)
		bondValidator(t, h, kp, 1000, 1)
		validators[i] = kp.PublicKey
	}

	var prevRoot [32]byte
	copy(prevRoot[:], []byte("genesis state root              "))

	for height := uint64(1); height <= 10; height++ {
		p1, err := leader.ElectedProposer(prevRoot, height, validators)
		if err != nil {
			t.Fatalf("height %d: %v", height, err)
		}
		p2, _ := leader.ElectedProposer(prevRoot, height, validators)
		if p1 != p2 {
			t.Fatalf("height %d: non-deterministic", height)
		}
	}
}

func TestSkipRotation(t *testing.T) {
	h := newHarness(t)
	validators := make([][32]byte, 3)
	for i := range validators {
		kp := makeKeypair(t)
		bondValidator(t, h, kp, 1000, 1)
		validators[i] = kp.PublicKey
	}

	var prevRoot [32]byte
	height := uint64(100)

	elected, _ := leader.ElectedProposer(prevRoot, height, validators)
	skip1, _ := leader.SkipProposer(prevRoot, height, validators, 1)
	skip2, _ := leader.SkipProposer(prevRoot, height, validators, 2)
	skip3, _ := leader.SkipProposer(prevRoot, height, validators, 3)

	if skip1 == elected {
		t.Fatal("skip1 should be different from elected")
	}
	if skip2 == skip1 || skip2 == elected {
		t.Fatal("skip2 should be different from skip1 and elected")
	}
	if skip3 != elected {
		t.Fatal("skip3 (len(validators)) should wrap back to elected")
	}
}

func TestLivenessPenalties(t *testing.T) {
	h := newHarness(t)
	kp := makeKeypair(t)
	bondValidator(t, h, kp, 10000, 1)

	// 4 misses — no slash
	for i := 0; i < 4; i++ {
		count, err := h.Staking.RecordMiss(kp.PublicKey)
		if err != nil {
			t.Fatalf("miss %d: %v", i+1, err)
		}
		if count != uint64(i+1) {
			t.Fatalf("miss %d: got count %d want %d", i+1, count, i+1)
		}
	}

	balBefore, _ := h.Ledger.BalanceOf(kp.PublicKey) // seeded 11000, bonded 10000 -> 1000 remaining
	_, bondedBefore, _ := h.Staking.BondedAmounts()  // 10000
	if bondedBefore[0].Int64() != 10000 {
		t.Fatalf("expected 10000 bonded, got %v", bondedBefore[0].Int64())
	}

	// 5th miss — slash 10% of 10000 = 1000
	count, err := h.Staking.RecordMiss(kp.PublicKey)
	if err != nil {
		t.Fatalf("5th miss: %v", err)
	}
	if count != 0 {
		t.Fatalf("after slash: got count %d want 0", count)
	}

	balAfter, _ := h.Ledger.BalanceOf(kp.PublicKey)
	if balAfter.Cmp(balBefore) != 0 {
		t.Fatal("slash should burn from escrow, not affect main balance")
	}

	_, bondedAfter, _ := h.Staking.BondedAmounts()
	if bondedAfter[0].Int64() != 9000 {
		t.Fatalf("bonded after slash: got %v want 9000", bondedAfter[0].Int64())
	}
}

func TestP2PGossipE2E(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	kp1 := makeKeypair(t)
	kp2 := makeKeypair(t)
	senderKP := makeKeypair(t)
	receiverKP := makeKeypair(t)

	n1, _ := p2p.New(ctx, kp1, p2p.Config{ListenAddr: "/ip4/127.0.0.1/tcp/0"})
	defer n1.Close()
	n2, _ := p2p.New(ctx, kp2, p2p.Config{ListenAddr: "/ip4/127.0.0.1/tcp/0"})
	defer n2.Close()

	if err := n2.Connect(ctx, n1.Addrs()); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	time.Sleep(500 * time.Millisecond) // let GossipSub mesh form

	tr := makeTx(t, senderKP, receiverKP, 500, 1)

	if err := n1.BroadcastTx(tr); err != nil {
		t.Fatalf("BroadcastTx: %v", err)
	}

	select {
	case received := <-n2.SubscribeTxs():
		if received.Nonce != tr.Nonce {
			t.Fatalf("received wrong nonce: got %d want %d", received.Nonce, tr.Nonce)
		}
		if received.Value.Cmp(tr.Value) != 0 {
			t.Fatalf("received wrong value: got %s want %s", received.Value, tr.Value)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for tx gossip")
	}
}
