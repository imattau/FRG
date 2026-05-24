package tx_test

import (
	"encoding/hex"
	"math/big"
	"testing"

	"github.com/imattau/frg/core/tx"
)

func TestTxSerialize(t *testing.T) {
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)
	t1 := &tx.Tx{
		Sender:   "alice",
		Receiver: "bob",
		Value:    scale,
		Nonce:    0,
	}
	b, err := t1.Serialize()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(b) != 58 {
		t.Fatalf("expected 58 bytes, got %d", len(b))
	}
	if hex.EncodeToString(b[:6]) != "54585f563100" {
		t.Fatalf("wrong domain prefix: %x", b[:6])
	}
	if b[6] != 0x00 || b[7] != 0x05 {
		t.Fatalf("wrong len_sender: %x %x", b[6], b[7])
	}
	if string(b[8:13]) != "alice" {
		t.Fatalf("wrong sender: %s", b[8:13])
	}
}

func TestTxIDDeterministic(t *testing.T) {
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)
	t1 := &tx.Tx{Sender: "alice", Receiver: "bob", Value: scale, Nonce: 42}
	id1, err := t1.ID()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	id2, err := t1.ID()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id1 != id2 {
		t.Fatal("ID() is not deterministic")
	}
}

func TestTxPayloadSizeLimit(t *testing.T) {
	bigSender := make([]byte, 70000)
	t1 := &tx.Tx{
		Sender:   string(bigSender),
		Receiver: "bob",
		Value:    new(big.Int).SetInt64(1),
		Nonce:    0,
	}
	_, err := t1.Serialize()
	if err == nil {
		t.Fatal("expected ERR_010, got nil")
	}
}
