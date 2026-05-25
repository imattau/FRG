package validator_test

import (
	"math/big"
	"testing"

	rgerrors "github.com/imattau/frg/core/errors"
	"github.com/imattau/frg/core/keys"
	"github.com/imattau/frg/core/node"
	"github.com/imattau/frg/core/tree"
	"github.com/imattau/frg/core/tx"
	"github.com/imattau/frg/validator"
)

func mustSignTx(senderKP, receiverKP *keys.Keypair, sender, receiver string, value int64, nonce uint64) *tx.Tx {
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)
	t1 := &tx.Tx{
		Type:           tx.TxTypeTransfer,
		Sender:         sender,
		Receiver:       receiver,
		Value:          new(big.Int).Mul(big.NewInt(value), scale),
		Nonce:          nonce,
		SenderPubKey:   senderKP.PublicKey,
		ReceiverPubKey: receiverKP.PublicKey,
	}

	msg, err := t1.UnsignedHash()
	if err != nil {
		panic("mustSignTx: UnsignedHash: " + err.Error())
	}
	senderSig, err := senderKP.Sign(msg[:])
	if err != nil {
		panic("mustSignTx: Sign sender: " + err.Error())
	}
	receiverSig, err := receiverKP.Sign(msg[:])
	if err != nil {
		panic("mustSignTx: Sign receiver: " + err.Error())
	}
	t1.SenderSig = senderSig
	t1.ReceiverSig = receiverSig
	return t1
}

func makeTx(sender, receiver string, value int64, nonce uint64) *tx.Tx {
	senderKP, err := keys.GenerateKeypair()
	if err != nil {
		panic("makeTx: GenerateKeypair sender: " + err.Error())
	}
	receiverKP, err := keys.GenerateKeypair()
	if err != nil {
		panic("makeTx: GenerateKeypair receiver: " + err.Error())
	}
	return mustSignTx(senderKP, receiverKP, sender, receiver, value, nonce)
}

func TestValidateEmptyBlock(t *testing.T) {
	b := &tree.Block{Height: 1, Txs: nil}
	claimedRoot := node.EmptyBlockRoot()

	got, err := validator.Validate(b, claimedRoot)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != claimedRoot {
		t.Fatalf("root mismatch: got %x, want %x", got, claimedRoot)
	}
}

func TestValidateCorrectRoot(t *testing.T) {
	b := &tree.Block{
		Height: 1,
		Txs: []*tx.Tx{
			makeTx("alice", "bob", 1, 0),
			makeTx("bob", "carol", 2, 1),
		},
	}
	expected, err := b.BuildRoot()
	if err != nil {
		t.Fatalf("BuildRoot failed: %v", err)
	}

	got, err := validator.Validate(b, expected)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != expected {
		t.Fatalf("root mismatch: got %x, want %x", got, expected)
	}
}

func TestValidateRootMismatch(t *testing.T) {
	b := &tree.Block{
		Height: 1,
		Txs:    []*tx.Tx{makeTx("alice", "bob", 1, 0)},
	}
	var wrongRoot [32]byte
	wrongRoot[0] = 0xff

	_, err := validator.Validate(b, wrongRoot)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	rgErr, ok := err.(*rgerrors.RGError)
	if !ok {
		t.Fatalf("expected *RGError, got %T", err)
	}
	if rgErr.Code != rgerrors.ErrRootMismatch {
		t.Fatalf("expected ERR_011, got %s", rgErr.Code)
	}
}

func TestValidatePipelineFault(t *testing.T) {
	senderKP, err := keys.GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair sender: %v", err)
	}
	receiverKP, err := keys.GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair receiver: %v", err)
	}

	txs := make([]*tx.Tx, tree.TMax+1)
	for i := range txs {
		txs[i] = mustSignTx(senderKP, receiverKP, "alice", "bob", 1, uint64(i))
	}
	b := &tree.Block{Height: 1, Txs: txs}
	var claimedRoot [32]byte

	_, err = validator.Validate(b, claimedRoot)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	rgErr, ok := err.(*rgerrors.RGError)
	if !ok {
		t.Fatalf("expected *RGError, got %T", err)
	}
	if rgErr.Code != rgerrors.ErrDosSizeExceeded {
		t.Fatalf("expected ERR_010, got %s", rgErr.Code)
	}
}

func TestValidateZeroValueTx(t *testing.T) {
	b := &tree.Block{
		Height: 1,
		Txs: []*tx.Tx{
			makeTx("alice", "bob", 0, 0),
		},
	}
	expected, err := b.BuildRoot()
	if err != nil {
		t.Fatalf("BuildRoot failed: %v", err)
	}

	got, err := validator.Validate(b, expected)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != expected {
		t.Fatalf("root mismatch: got %x, want %x", got, expected)
	}
}

func TestValidateInvalidSig(t *testing.T) {
	senderKP, err := keys.GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair sender: %v", err)
	}
	receiverKP, err := keys.GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair receiver: %v", err)
	}
	badTx := mustSignTx(senderKP, receiverKP, "alice", "bob", 1, 0)
	badTx.SenderSig[0] ^= 0xff

	b := &tree.Block{Height: 1, Txs: []*tx.Tx{badTx}}
	var claimedRoot [32]byte

	_, err = validator.Validate(b, claimedRoot)
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
