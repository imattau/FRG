package tx_test

import (
	"encoding/hex"
	"math/big"
	"testing"

	rgerrors "github.com/imattau/frg/core/errors"
	"github.com/imattau/frg/core/keys"
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
	if len(b) != 250 {
		t.Fatalf("expected 250 bytes, got %d", len(b))
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

func signedTx(t *testing.T, sender, receiver string, value *big.Int, nonce uint64) *tx.Tx {
	t.Helper()
	senderKP, err := keys.GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair sender: %v", err)
	}
	receiverKP, err := keys.GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair receiver: %v", err)
	}
	t1 := &tx.Tx{
		Sender:         sender,
		Receiver:       receiver,
		Value:          new(big.Int).Set(value),
		Nonce:          nonce,
		SenderPubKey:   senderKP.PublicKey,
		ReceiverPubKey: receiverKP.PublicKey,
	}
	msg, err := t1.UnsignedHash()
	if err != nil {
		t.Fatalf("UnsignedHash() error: %v", err)
	}
	sig, err := senderKP.Sign(msg[:])
	if err != nil {
		t.Fatalf("Sign sender: %v", err)
	}
	rsig, err := receiverKP.Sign(msg[:])
	if err != nil {
		t.Fatalf("Sign receiver: %v", err)
	}
	t1.SenderSig = sig
	t1.ReceiverSig = rsig
	return t1
}

func TestVerifySigsValid(t *testing.T) {
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)
	t1 := signedTx(t, "alice", "bob", scale, 7)
	if err := t1.VerifySigs(); err != nil {
		t.Fatalf("VerifySigs() unexpected error: %v", err)
	}
}

func TestVerifySigsSenderInvalid(t *testing.T) {
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)
	t1 := signedTx(t, "alice", "bob", scale, 7)
	t1.SenderSig[0] ^= 0xff
	err := t1.VerifySigs()
	if err == nil {
		t.Fatal("expected ERR_012, got nil")
	}
	rgErr, ok := err.(*rgerrors.RGError)
	if !ok {
		t.Fatalf("expected *RGError, got %T", err)
	}
	if rgErr.Code != rgerrors.ErrInvalidSignature {
		t.Fatalf("expected ERR_012, got %s", rgErr.Code)
	}
}

func TestVerifySigsReceiverInvalid(t *testing.T) {
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)
	t1 := signedTx(t, "alice", "bob", scale, 7)
	t1.ReceiverSig[0] ^= 0xff
	err := t1.VerifySigs()
	if err == nil {
		t.Fatal("expected ERR_012, got nil")
	}
	rgErr, ok := err.(*rgerrors.RGError)
	if !ok {
		t.Fatalf("expected *RGError, got %T", err)
	}
	if rgErr.Code != rgerrors.ErrInvalidSignature {
		t.Fatalf("expected ERR_012, got %s", rgErr.Code)
	}
}
