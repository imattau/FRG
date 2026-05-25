package e2e_test

import (
	"math/big"
	"testing"

	rgerrors "github.com/imattau/frg/core/errors"
	"github.com/imattau/frg/core/staking"
	"github.com/imattau/frg/core/tree"
	"github.com/imattau/frg/core/tx"
)

func TestEquivocationSlash(t *testing.T) {
	h := newHarness(t)
	kp := makeKeypair(t)
	bondValidator(t, h, kp, 2000, 1)

	headerA := []byte("header A")
	headerB := []byte("header B")
	sigA, _ := kp.Sign(headerA)
	sigB, _ := kp.Sign(headerB)

	proof := staking.EquivocationProof{
		ValidatorPubKey: kp.PublicKey,
		HeaderA:         headerA,
		SigA:            sigA,
		HeaderB:         headerB,
		SigB:            sigB,
	}

	if err := h.Staking.Slash(kp.PublicKey, proof); err != nil {
		t.Fatalf("Slash: %v", err)
	}

	// Validator should be removed
	set, _ := h.Staking.ValidatorSet()
	if len(set) != 0 {
		t.Fatal("validator still in set after slash")
	}

	// Escrow should be burned
	bal, _ := h.Ledger.BalanceOf(kp.PublicKey)
	if bal.Cmp(big.NewInt(1000)) != 0 {
		t.Fatalf("balance: got %v want 1000 (only seed remaining, bond burned)", bal)
	}
}

func TestEquivocationSameHeader(t *testing.T) {
	h := newHarness(t)
	kp := makeKeypair(t)
	bondValidator(t, h, kp, 2000, 1)

	header := []byte("header")
	sig, _ := kp.Sign(header)

	proof := staking.EquivocationProof{
		ValidatorPubKey: kp.PublicKey,
		HeaderA:         header,
		SigA:            sig,
		HeaderB:         header,
		SigB:            sig,
	}

	err := h.Staking.Slash(kp.PublicKey, proof)
	assertCode(t, err, rgerrors.ErrCanonicalEncodingDistortion)
}

func TestEquivocationInvalidSig(t *testing.T) {
	h := newHarness(t)
	kp := makeKeypair(t)
	bondValidator(t, h, kp, 2000, 1)

	headerA := []byte("header A")
	headerB := []byte("header B")
	sigA, _ := kp.Sign(headerA)
	sigB, _ := kp.Sign(headerB)
	sigB[0] ^= 0xff // corrupt sig

	proof := staking.EquivocationProof{
		ValidatorPubKey: kp.PublicKey,
		HeaderA:         headerA,
		SigA:            sigA,
		HeaderB:         headerB,
		SigB:            sigB,
	}

	err := h.Staking.Slash(kp.PublicKey, proof)
	assertCode(t, err, rgerrors.ErrInvalidSignature)
}

func TestDOSSizeExceeded(t *testing.T) {
	sender := makeKeypair(t)
	receiver := makeKeypair(t)
	txs := make([]*tx.Tx, tree.TMax+1)
	for i := range txs {
		txs[i] = makeTx(t, sender, receiver, 1, uint64(i))
	}
	b := &tree.Block{Height: 1, Txs: txs}
	_, err := b.BuildRoot()
	assertCode(t, err, rgerrors.ErrDosSizeExceeded)
}

func TestTxPayloadTooLarge(t *testing.T) {
	sender := makeKeypair(t)
	receiver := makeKeypair(t)
	// Tx limit is 70000 bytes.
	// unsigned part is 6 + 2 + len(sender) + 2 + len(receiver) + 32 + 8
	// Plus 192 for sigs and pubkeys.
	// We need len(sender) + len(receiver) > 70000 - 192 - 50 = 69758
	// but len(sender) <= 65535 and len(receiver) <= 65535.
	bigSender := make([]byte, 60000)
	for i := range bigSender {
		bigSender[i] = 'a'
	}
	bigReceiver := make([]byte, 10000)
	for i := range bigReceiver {
		bigReceiver[i] = 'b'
	}
	tr := &tx.Tx{
		Sender:         string(bigSender),
		Receiver:       string(bigReceiver),
		Value:          big.NewInt(1),
		Nonce:          1,
		SenderPubKey:   sender.PublicKey,
		ReceiverPubKey: receiver.PublicKey,
	}
	_, err := tr.Serialize()
	assertCode(t, err, rgerrors.ErrDosSizeExceeded)
}

func TestInsufficientFundsTransfer(t *testing.T) {
	h := newHarness(t)
	sender := makeKeypair(t)
	receiver := makeKeypair(t)
	seedAccount(t, h.Ledger, sender.PublicKey, 100)

	tr := makeTx(t, sender, receiver, 200, 1)
	err := h.Ledger.Transfer(tr)
	assertCode(t, err, rgerrors.ErrInsufficientFunds)
}

func TestSlashUnbondingValidator(t *testing.T) {
	h := newHarness(t)
	kp := makeKeypair(t)
	bondValidator(t, h, kp, 2000, 1)
	if err := h.Staking.Unbond(kp.PublicKey, 1); err != nil {
		t.Fatalf("Unbond: %v", err)
	}

	headerA := []byte("header A")
	headerB := []byte("header B")
	sigA, _ := kp.Sign(headerA)
	sigB, _ := kp.Sign(headerB)

	proof := staking.EquivocationProof{
		ValidatorPubKey: kp.PublicKey,
		HeaderA:         headerA,
		SigA:            sigA,
		HeaderB:         headerB,
		SigB:            sigB,
	}

	err := h.Staking.Slash(kp.PublicKey, proof)
	assertCode(t, err, rgerrors.ErrNotBonded)
}

func TestDoubleBond(t *testing.T) {
	h := newHarness(t)
	kp := makeKeypair(t)
	bondValidator(t, h, kp, 2000, 1)

	seedAccount(t, h.Ledger, kp.PublicKey, 2000)
	err := h.Staking.Bond(kp.PublicKey, big.NewInt(1000), 2)
	assertCode(t, err, rgerrors.ErrAlreadyBonded)
}
