package ledger_test

import (
	"errors"
	"math/big"
	"path/filepath"
	"testing"

	rgerrors "github.com/imattau/frg/core/errors"
	"github.com/imattau/frg/core/keys"
	"github.com/imattau/frg/core/ledger"
	"github.com/imattau/frg/core/tx"
)

func makeTx(t *testing.T, senderKP, receiverKP *keys.Keypair, value int64, nonce uint64) *tx.Tx {
	t.Helper()
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)
	tr := &tx.Tx{
		Type:           tx.TxTypeTransfer,
		Sender:         "alice",
		Receiver:       "bob",
		Value:          new(big.Int).Mul(big.NewInt(value), scale),
		Nonce:          nonce,
		SenderPubKey:   senderKP.PublicKey,
		ReceiverPubKey: receiverKP.PublicKey,
	}
	msg, err := tr.UnsignedHash()
	if err != nil {
		t.Fatal(err)
	}
	senderSig, err := senderKP.Sign(msg[:])
	if err != nil {
		t.Fatal(err)
	}
	receiverSig, err := receiverKP.Sign(msg[:])
	if err != nil {
		t.Fatal(err)
	}
	tr.SenderSig = senderSig
	tr.ReceiverSig = receiverSig
	return tr
}

func openLedger(t *testing.T) *ledger.Ledger {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ledger.db")
	l, err := ledger.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := l.Close(); err != nil {
			t.Errorf("close ledger: %v", err)
		}
	})
	return l
}

func TestBalanceOfZero(t *testing.T) {
	l := openLedger(t)
	kp, err := keys.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	bal, err := l.BalanceOf(kp.PublicKey)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if bal.Sign() != 0 {
		t.Fatalf("expected zero balance, got %s", bal)
	}
}

func TestTransferValid(t *testing.T) {
	l := openLedger(t)
	senderKP, err := keys.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	receiverKP, err := keys.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}

	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)
	initial := new(big.Int).Mul(big.NewInt(100), scale)
	if err := l.Seed(senderKP.PublicKey, initial); err != nil {
		t.Fatal(err)
	}

	tr := makeTx(t, senderKP, receiverKP, 40, 1)
	if err := l.Transfer(tr); err != nil {
		t.Fatalf("Transfer failed: %v", err)
	}

	senderBal, _ := l.BalanceOf(senderKP.PublicKey)
	receiverBal, _ := l.BalanceOf(receiverKP.PublicKey)

	wantSender := new(big.Int).Mul(big.NewInt(60), scale)
	wantReceiver := new(big.Int).Mul(big.NewInt(40), scale)
	if senderBal.Cmp(wantSender) != 0 {
		t.Errorf("sender: got %s, want %s", senderBal, wantSender)
	}
	if receiverBal.Cmp(wantReceiver) != 0 {
		t.Errorf("receiver: got %s, want %s", receiverBal, wantReceiver)
	}
}

func TestTransferInsufficientFunds(t *testing.T) {
	l := openLedger(t)
	senderKP, err := keys.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	receiverKP, err := keys.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}

	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)
	initial := new(big.Int).Mul(big.NewInt(10), scale)
	if err := l.Seed(senderKP.PublicKey, initial); err != nil {
		t.Fatal(err)
	}

	tr := makeTx(t, senderKP, receiverKP, 50, 1)
	err = l.Transfer(tr)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	rge, ok := err.(*rgerrors.RGError)
	if !ok || rge.Code != rgerrors.ErrInsufficientFunds {
		t.Errorf("expected ERR_013, got %v", err)
	}
}

func TestTransferInvalidSig(t *testing.T) {
	l := openLedger(t)
	senderKP, err := keys.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	receiverKP, err := keys.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}

	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)
	if err := l.Seed(senderKP.PublicKey, new(big.Int).Mul(big.NewInt(100), scale)); err != nil {
		t.Fatal(err)
	}

	tr := makeTx(t, senderKP, receiverKP, 10, 1)
	tr.SenderSig = [64]byte{}

	err = l.Transfer(tr)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	rge, ok := err.(*rgerrors.RGError)
	if !ok || rge.Code != rgerrors.ErrInvalidSignature {
		t.Errorf("expected ERR_012, got %v", err)
	}
}

func TestBurnValid(t *testing.T) {
	l := openLedger(t)
	kp, err := keys.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)
	initial := new(big.Int).Mul(big.NewInt(100), scale)
	if err := l.Seed(kp.PublicKey, initial); err != nil {
		t.Fatal(err)
	}

	burnAmt := new(big.Int).Mul(big.NewInt(40), scale)
	if err := l.Burn(kp.PublicKey, burnAmt); err != nil {
		t.Fatalf("Burn failed: %v", err)
	}

	bal, _ := l.BalanceOf(kp.PublicKey)
	want := new(big.Int).Mul(big.NewInt(60), scale)
	if bal.Cmp(want) != 0 {
		t.Errorf("after burn: got %s, want %s", bal, want)
	}
}

func TestBurnInsufficientFunds(t *testing.T) {
	l := openLedger(t)
	kp, err := keys.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)
	if err := l.Seed(kp.PublicKey, new(big.Int).Mul(big.NewInt(10), scale)); err != nil {
		t.Fatal(err)
	}

	burnAmt := new(big.Int).Mul(big.NewInt(50), scale)
	err = l.Burn(kp.PublicKey, burnAmt)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	rge, ok := err.(*rgerrors.RGError)
	if !ok || rge.Code != rgerrors.ErrInsufficientFunds {
		t.Errorf("expected ERR_013, got %v", err)
	}
}

func TestLedgerPersistence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ledger.db")

	kp, err := keys.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)
	want := new(big.Int).Mul(big.NewInt(77), scale)

	l1, err := ledger.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := l1.Seed(kp.PublicKey, want); err != nil {
		t.Fatal(err)
	}
	if err := l1.Close(); err != nil {
		t.Fatal(err)
	}

	l2, err := ledger.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer l2.Close()

	bal, err := l2.BalanceOf(kp.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	if bal.Cmp(want) != 0 {
		t.Errorf("after reopen: got %s, want %s", bal, want)
	}
}

func TestMoveValid(t *testing.T) {
	l := openLedger(t)
	kpA, _ := keys.GenerateKeypair()
	kpB, _ := keys.GenerateKeypair()
	seed := big.NewInt(2000)
	if err := l.Seed(kpA.PublicKey, seed); err != nil {
		t.Fatal(err)
	}

	if err := l.Move(kpA.PublicKey, kpB.PublicKey, big.NewInt(500)); err != nil {
		t.Fatalf("Move: %v", err)
	}
	balA, _ := l.BalanceOf(kpA.PublicKey)
	balB, _ := l.BalanceOf(kpB.PublicKey)
	if balA.Cmp(big.NewInt(1500)) != 0 {
		t.Fatalf("sender balance: got %v want 1500", balA)
	}
	if balB.Cmp(big.NewInt(500)) != 0 {
		t.Fatalf("receiver balance: got %v want 500", balB)
	}
}

func TestNonceOf(t *testing.T) {
	l := openLedger(t)
	kp, _ := keys.GenerateKeypair()

	// unknown account → 0
	n, err := l.NonceOf(kp.PublicKey)
	if err != nil {
		t.Fatalf("NonceOf: %v", err)
	}
	if n != 0 {
		t.Fatalf("unknown account nonce: got %d want 0", n)
	}
}

func TestNonceFirstTx(t *testing.T) {
	l := openLedger(t)
	senderKP, _ := keys.GenerateKeypair()
	receiverKP, _ := keys.GenerateKeypair()
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)
	_ = l.Seed(senderKP.PublicKey, new(big.Int).Mul(big.NewInt(1000), scale))

	tr := makeTx(t, senderKP, receiverKP, 100, 1)
	if err := l.Transfer(tr); err != nil {
		t.Fatalf("first tx nonce=1: %v", err)
	}
	n, _ := l.NonceOf(senderKP.PublicKey)
	if n != 1 {
		t.Fatalf("nonce after first tx: got %d want 1", n)
	}
}

func TestNonceSequential(t *testing.T) {
	l := openLedger(t)
	senderKP, _ := keys.GenerateKeypair()
	receiverKP, _ := keys.GenerateKeypair()
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)
	_ = l.Seed(senderKP.PublicKey, new(big.Int).Mul(big.NewInt(1000), scale))

	tr1 := makeTx(t, senderKP, receiverKP, 100, 1)
	if err := l.Transfer(tr1); err != nil {
		t.Fatalf("tx1: %v", err)
	}
	tr2 := makeTx(t, senderKP, receiverKP, 100, 2)
	if err := l.Transfer(tr2); err != nil {
		t.Fatalf("tx2: %v", err)
	}
	n, _ := l.NonceOf(senderKP.PublicKey)
	if n != 2 {
		t.Fatalf("nonce after tx2: got %d want 2", n)
	}
}

func TestNonceReplay(t *testing.T) {
	l := openLedger(t)
	senderKP, _ := keys.GenerateKeypair()
	receiverKP, _ := keys.GenerateKeypair()
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)
	_ = l.Seed(senderKP.PublicKey, new(big.Int).Mul(big.NewInt(1000), scale))

	tr := makeTx(t, senderKP, receiverKP, 100, 1)
	if err := l.Transfer(tr); err != nil {
		t.Fatalf("first tx: %v", err)
	}

	err := l.Transfer(tr)
	if err == nil {
		t.Fatal("expected error for replay, got nil")
	}
	var rge *rgerrors.RGError
	if !errors.As(err, &rge) || rge.Code != rgerrors.ErrSequenceFault {
		t.Fatalf("expected ERR_018, got %v", err)
	}
}

func TestNonceGap(t *testing.T) {
	l := openLedger(t)
	senderKP, _ := keys.GenerateKeypair()
	receiverKP, _ := keys.GenerateKeypair()
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)
	_ = l.Seed(senderKP.PublicKey, new(big.Int).Mul(big.NewInt(1000), scale))

	tr := makeTx(t, senderKP, receiverKP, 100, 2) // skipped 1
	err := l.Transfer(tr)
	if err == nil {
		t.Fatal("expected error for gap, got nil")
	}
	var rge *rgerrors.RGError
	if !errors.As(err, &rge) || rge.Code != rgerrors.ErrSequenceFault {
		t.Fatalf("expected ERR_018, got %v", err)
	}
}

func TestNonceZeroRejected(t *testing.T) {
	l := openLedger(t)
	senderKP, _ := keys.GenerateKeypair()
	receiverKP, _ := keys.GenerateKeypair()
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)
	_ = l.Seed(senderKP.PublicKey, new(big.Int).Mul(big.NewInt(1000), scale))

	tr := makeTx(t, senderKP, receiverKP, 100, 0)
	err := l.Transfer(tr)
	if err == nil {
		t.Fatal("expected error for nonce 0, got nil")
	}
	var rge *rgerrors.RGError
	if !errors.As(err, &rge) || rge.Code != rgerrors.ErrSequenceFault {
		t.Fatalf("expected ERR_018, got %v", err)
	}
}

func TestNonceAtomicity(t *testing.T) {
	l := openLedger(t)
	senderKP, _ := keys.GenerateKeypair()
	receiverKP, _ := keys.GenerateKeypair()
	_ = l.Seed(senderKP.PublicKey, big.NewInt(50))

	tr := makeTx(t, senderKP, receiverKP, 100, 1) // insufficient funds
	err := l.Transfer(tr)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	n, _ := l.NonceOf(senderKP.PublicKey)
	if n != 0 {
		t.Fatalf("nonce advanced on failed transfer: got %d want 0", n)
	}
}

func TestNonceIsolated(t *testing.T) {
	l := openLedger(t)
	kpA, _ := keys.GenerateKeypair()
	kpB, _ := keys.GenerateKeypair()
	receiver, _ := keys.GenerateKeypair()
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)
	_ = l.Seed(kpA.PublicKey, new(big.Int).Mul(big.NewInt(1000), scale))
	_ = l.Seed(kpB.PublicKey, new(big.Int).Mul(big.NewInt(1000), scale))

	if err := l.Transfer(makeTx(t, kpA, receiver, 10, 1)); err != nil {
		t.Fatalf("nA tx1: %v", err)
	}
	if err := l.Transfer(makeTx(t, kpA, receiver, 10, 2)); err != nil {
		t.Fatalf("nA tx2: %v", err)
	}
	if err := l.Transfer(makeTx(t, kpB, receiver, 10, 1)); err != nil {
		t.Fatalf("nB tx1: %v", err)
	}

	nA, _ := l.NonceOf(kpA.PublicKey)
	nB, _ := l.NonceOf(kpB.PublicKey)
	if nA != 2 {
		t.Errorf("nA: got %d want 2", nA)
	}
	if nB != 1 {
		t.Errorf("nB: got %d want 1", nB)
	}
}

func TestMoveInsufficientFunds(t *testing.T) {
	l := openLedger(t)
	kpA, _ := keys.GenerateKeypair()
	kpB, _ := keys.GenerateKeypair()
	if err := l.Seed(kpA.PublicKey, big.NewInt(100)); err != nil {
		t.Fatal(err)
	}

	err := l.Move(kpA.PublicKey, kpB.PublicKey, big.NewInt(500))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var rge *rgerrors.RGError
	if !errors.As(err, &rge) || rge.Code != rgerrors.ErrInsufficientFunds {
		t.Fatalf("expected ERR_013, got %v", err)
	}
}
