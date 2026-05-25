package statemachine_test

import (
	"math/big"
	"path/filepath"
	"testing"

	rgerrors "github.com/imattau/frg/core/errors"
	"github.com/imattau/frg/core/keys"
	"github.com/imattau/frg/core/ledger"
	"github.com/imattau/frg/core/node"
	"github.com/imattau/frg/core/staking"
	"github.com/imattau/frg/core/statemachine"
	"github.com/imattau/frg/core/tx"
	bolt "go.etcd.io/bbolt"
)

func openSM(t *testing.T) (*statemachine.StateMachine, *bolt.DB) {
	t.Helper()
	dir := t.TempDir()
	db, err := bolt.Open(filepath.Join(dir, "state.db"), 0600, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	l, err := ledger.New(db)
	if err != nil {
		t.Fatal(err)
	}
	s, err := staking.New(db, l)
	if err != nil {
		t.Fatal(err)
	}
	sm, err := statemachine.New(db, l, s)
	if err != nil {
		t.Fatal(err)
	}
	return sm, db
}

func TestApplyEmptyBlock(t *testing.T) {
	sm, _ := openSM(t)
	var proposer [32]byte
	proposer[0] = 1
	result, err := sm.ApplyBlock(&statemachine.Block{
		Height:         1,
		Txs:            nil,
		ProposerPubKey: proposer,
	})
	if err != nil {
		t.Fatalf("ApplyBlock: %v", err)
	}
	if result.Height != 1 {
		t.Fatalf("want height 1, got %d", result.Height)
	}
	emptyRoot := node.EmptyBlockRoot()
	if result.StateRoot != emptyRoot {
		t.Fatalf("want EmptyBlockRoot, got %x", result.StateRoot)
	}
	if result.TxsApplied != 0 {
		t.Fatalf("want 0 txs applied, got %d", result.TxsApplied)
	}
	h, err := sm.CurrentHeight()
	if err != nil {
		t.Fatal(err)
	}
	if h != 1 {
		t.Fatalf("want CurrentHeight 1, got %d", h)
	}
}

func makeTransferTx(t *testing.T, sender, receiver *keys.Keypair, value int64, nonce uint64) *tx.Tx {
	t.Helper()
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)
	tr := &tx.Tx{
		Type:           tx.TxTypeTransfer,
		Sender:         "sender",
		Receiver:       "receiver",
		Value:          new(big.Int).Mul(big.NewInt(value), scale),
		Nonce:          nonce,
		SenderPubKey:   sender.PublicKey,
		ReceiverPubKey: receiver.PublicKey,
	}
	msg, err := tr.UnsignedHash()
	if err != nil {
		t.Fatal(err)
	}
	tr.SenderSig, _ = sender.Sign(msg[:])
	tr.ReceiverSig, _ = receiver.Sign(msg[:])
	return tr
}

func TestApplySingleTransfer(t *testing.T) {
	sm, db := openSM(t)

	l, _ := ledger.New(db)
	senderKP, _ := keys.GenerateKeypair()
	receiverKP, _ := keys.GenerateKeypair()

	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)
	initial := new(big.Int).Mul(big.NewInt(1000), scale)
	if err := l.Seed(senderKP.PublicKey, initial); err != nil {
		t.Fatal(err)
	}

	var proposer [32]byte
	proposer[0] = 2
	tr := makeTransferTx(t, senderKP, receiverKP, 100, 1)
	result, err := sm.ApplyBlock(&statemachine.Block{
		Height:         1,
		Txs:            []*tx.Tx{tr},
		ProposerPubKey: proposer,
	})
	if err != nil {
		t.Fatalf("ApplyBlock: %v", err)
	}
	if result.TxsApplied != 1 {
		t.Fatalf("want 1 tx, got %d", result.TxsApplied)
	}

	senderBal, _ := l.BalanceOf(senderKP.PublicKey)
	receiverBal, _ := l.BalanceOf(receiverKP.PublicKey)
	wantReceiver := new(big.Int).Mul(big.NewInt(100), scale)
	if receiverBal.Cmp(wantReceiver) != 0 {
		t.Fatalf("receiver: want %s, got %s", wantReceiver, receiverBal)
	}
	// initial - 100 - baseFee(1)
	if senderBal.Cmp(new(big.Int).Sub(initial, wantReceiver)) >= 0 {
		t.Fatalf("sender: balance should have decreased by more than 100 due to gas, got %s", senderBal)
	}
}

func TestApplyBlockHeightSequence(t *testing.T) {
	sm, _ := openSM(t)
	var proposer [32]byte
	_, err := sm.ApplyBlock(&statemachine.Block{Height: 1, ProposerPubKey: proposer})
	if err != nil {
		t.Fatal(err)
	}
	_, err = sm.ApplyBlock(&statemachine.Block{Height: 3, ProposerPubKey: proposer})
	if err == nil {
		t.Fatal("expected ERR_021, got nil")
	}
	rge, ok := err.(*rgerrors.RGError)
	if !ok || rge.Code != rgerrors.ErrBlockHeightSequenceFault {
		t.Fatalf("expected ERR_021, got %v", err)
	}
}

func TestApplyBlockRollback(t *testing.T) {
	sm, db := openSM(t)
	l, _ := ledger.New(db)
	senderKP, _ := keys.GenerateKeypair()
	receiverKP, _ := keys.GenerateKeypair()

	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)
	if err := l.Seed(senderKP.PublicKey, new(big.Int).Mul(big.NewInt(100), scale)); err != nil {
		t.Fatal(err)
	}

	tr1 := makeTransferTx(t, senderKP, receiverKP, 60, 1)
	tr2 := makeTransferTx(t, senderKP, receiverKP, 60, 2) // this should fail due to insufficient funds

	var proposer [32]byte
	_, err := sm.ApplyBlock(&statemachine.Block{
		Height:         1,
		Txs:            []*tx.Tx{tr1, tr2},
		ProposerPubKey: proposer,
	})
	if err == nil {
		t.Fatal("expected failure, got nil")
	}

	// state should be rolled back to height 0
	h, _ := sm.CurrentHeight()
	if h != 0 {
		t.Fatalf("height should be 0, got %d", h)
	}
	bal, _ := l.BalanceOf(senderKP.PublicKey)
	if bal.Cmp(new(big.Int).Mul(big.NewInt(100), scale)) != 0 {
		t.Fatalf("balance should be 100, got %s", bal)
	}
}
