package e2e_test

import (
	"math/big"
	"path/filepath"
	"testing"

	rgerrors "github.com/imattau/frg/core/errors"
	"github.com/imattau/frg/core/keys"
	"github.com/imattau/frg/core/ledger"
	"github.com/imattau/frg/core/staking"
	"github.com/imattau/frg/core/tx"
	"github.com/imattau/frg/core/tree"
)

type harness struct {
	Ledger  *ledger.Ledger
	Staking *staking.Store
	Dir     string
}

func newHarness(t testing.TB) *harness {
	t.Helper()
	dir := t.TempDir()
	l, err := ledger.Open(filepath.Join(dir, "ledger.db"))
	if err != nil {
		t.Fatalf("ledger.Open: %v", err)
	}
	s, err := staking.Open(filepath.Join(dir, "staking.db"), l)
	if err != nil {
		t.Fatalf("staking.Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close(); _ = l.Close() })
	return &harness{Ledger: l, Staking: s, Dir: dir}
}

func makeKeypair(t testing.TB) *keys.Keypair {
	t.Helper()
	kp, err := keys.GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair: %v", err)
	}
	return kp
}

func makeTx(t testing.TB, sender, receiver *keys.Keypair, value int64, nonce uint64) *tx.Tx {
	t.Helper()
	tr := &tx.Tx{
		Type:           tx.TxTypeTransfer,
		Sender:         "sender",
		Receiver:       "receiver",
		Value:          big.NewInt(value),
		Nonce:          nonce,
		SenderPubKey:   sender.PublicKey,
		ReceiverPubKey: receiver.PublicKey,
	}
	recvSig, err := tr.SignReceiver(receiver)
	if err != nil {
		t.Fatalf("SignReceiver: %v", err)
	}
	tr.ReceiverSig = recvSig
	senderSig, err := tr.SignSender(sender)
	if err != nil {
		t.Fatalf("SignSender: %v", err)
	}
	tr.SenderSig = senderSig
	return tr
}

func buildBlock(t testing.TB, height uint64, txs []*tx.Tx) [32]byte {
	t.Helper()
	b := &tree.Block{Height: height, Txs: txs}
	root, err := b.BuildRoot()
	if err != nil {
		t.Fatalf("BuildRoot: %v", err)
	}
	return root
}

func seedAccount(t testing.TB, l *ledger.Ledger, pub [32]byte, amount int64) {
	t.Helper()
	if err := l.Seed(pub, big.NewInt(amount)); err != nil {
		t.Fatalf("Seed: %v", err)
	}
}

func bondValidator(t testing.TB, h *harness, kp *keys.Keypair, amount int64, block uint64) {
	t.Helper()
	seedAccount(t, h.Ledger, kp.PublicKey, amount+1000) // extra for fees
	if err := h.Staking.Bond(kp.PublicKey, big.NewInt(amount), block); err != nil {
		t.Fatalf("Bond: %v", err)
	}
}

func assertCode(t testing.TB, err error, code rgerrors.ErrorCode) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error %s, got nil", code)
	}
	rge, ok := err.(*rgerrors.RGError)
	if !ok {
		t.Fatalf("expected *RGError, got %T: %v", err, err)
	}
	if rge.Code != code {
		t.Fatalf("expected code %s, got %s", code, rge.Code)
	}
}
