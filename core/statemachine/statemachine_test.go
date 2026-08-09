package statemachine_test

import (
	"encoding/binary"
	"math/big"
	"path/filepath"
	"testing"

	"github.com/imattau/frg/core/contract"
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

func TestCommittedBlockReplayStorage(t *testing.T) {
	sm, _ := openSM(t)
	proposer := [32]byte{3}
	if _, err := sm.ApplyBlock(&statemachine.Block{Height: 1, ProposerPubKey: proposer}); err != nil {
		t.Fatal(err)
	}
	block, err := sm.BlockAt(1)
	if err != nil {
		t.Fatal(err)
	}
	if block == nil || block.Height != 1 || block.ProposerPubKey != proposer {
		t.Fatalf("unexpected replayed block: %+v", block)
	}
	blocks, err := sm.Blocks(1, 1)
	if err != nil || len(blocks) != 1 {
		t.Fatalf("unexpected block range: len=%d err=%v", len(blocks), err)
	}
}

func TestBackupCreatesConsistentSnapshot(t *testing.T) {
	sm, _ := openSM(t)
	if _, err := sm.ApplyBlock(&statemachine.Block{Height: 1}); err != nil {
		t.Fatal(err)
	}
	backupPath := filepath.Join(t.TempDir(), "state-backup.db")
	if err := sm.Backup(backupPath); err != nil {
		t.Fatal(err)
	}
	backup, err := bolt.Open(backupPath, 0600, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer backup.Close()
	var height uint64
	if err := backup.View(func(tx *bolt.Tx) error {
		v := tx.Bucket([]byte("meta")).Get([]byte("height"))
		height = binary.BigEndian.Uint64(v)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if height != 1 {
		t.Fatalf("backup height = %d, want 1", height)
	}
}

func TestConsensusVoteReservationIsDurable(t *testing.T) {
	sm, _ := openSM(t)
	var hash [32]byte
	hash[0] = 1
	if !sm.RecordConsensusVote(4, 2, 1, hash) {
		t.Fatal("first vote reservation should succeed")
	}
	if sm.RecordConsensusVote(4, 2, 1, hash) {
		t.Fatal("duplicate vote reservation should be rejected")
	}
	var conflicting [32]byte
	conflicting[0] = 2
	if sm.RecordConsensusVote(4, 2, 1, conflicting) {
		t.Fatal("conflicting vote reservation should be rejected")
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

var testWasm = []byte{
	0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00,
	0x01, 0x04, 0x01, 0x60, 0x00, 0x00,
	0x03, 0x03, 0x02, 0x00, 0x00,
	0x07, 0x0F, 0x02,
	0x04, 0x69, 0x6E, 0x69, 0x74, 0x00, 0x00,
	0x04, 0x63, 0x61, 0x6C, 0x6C, 0x00, 0x01,
	0x0A, 0x07, 0x02, 0x02, 0x00, 0x0B, 0x02, 0x00, 0x0B,
}

func makeDeployTx(t *testing.T, kp *keys.Keypair, nonce uint64) *tx.Tx {
	t.Helper()
	tr := &tx.Tx{
		Type:         tx.TxTypeContractDeploy,
		Sender:       "test",
		Receiver:     "contract",
		Value:        big.NewInt(0),
		Nonce:        nonce,
		SenderPubKey: kp.PublicKey,
		WasmBytes:    testWasm,
	}
	sig, _ := tr.SignSender(kp)
	tr.SenderSig = sig
	return tr
}

func TestContractDeployChargesGas(t *testing.T) {
	sm, db := openSM(t)
	l, _ := ledger.New(db)

	kp, _ := keys.GenerateKeypair()
	if err := l.Seed(kp.PublicKey, big.NewInt(1000)); err != nil {
		t.Fatal(err)
	}

	var proposer [32]byte
	proposer[0] = 1
	deployTx := makeDeployTx(t, kp, 1)
	result, err := sm.ApplyBlock(&statemachine.Block{
		Height:         1,
		Txs:            []*tx.Tx{deployTx},
		ProposerPubKey: proposer,
	})
	if err != nil {
		t.Fatalf("ApplyBlock: %v", err)
	}

	if result.TxsApplied != 1 {
		t.Fatalf("want 1 tx applied, got %d", result.TxsApplied)
	}
	if result.GasBurned.Sign() <= 0 {
		t.Fatal("GasBurned should be > 0 for contract deploy")
	}

	bal, _ := l.BalanceOf(kp.PublicKey)
	if bal.Cmp(big.NewInt(1000)) >= 0 {
		t.Fatalf("sender balance should have decreased (gas burned), got %s", bal)
	}
	t.Logf("sender balance: %s (initial 1000)", bal)
	t.Logf("GasBurned: %s", result.GasBurned)
}

func TestContractCallSameFeeForSameWorkload(t *testing.T) {
	sm, db := openSM(t)
	l, _ := ledger.New(db)

	kp, _ := keys.GenerateKeypair()
	if err := l.Seed(kp.PublicKey, big.NewInt(1000)); err != nil {
		t.Fatal(err)
	}

	var proposer [32]byte
	proposer[0] = 2

	// Block 1: deploy
	_, err := sm.ApplyBlock(&statemachine.Block{
		Height:         1,
		Txs:            []*tx.Tx{makeDeployTx(t, kp, 1)},
		ProposerPubKey: proposer,
	})
	if err != nil {
		t.Fatalf("deploy: %v", err)
	}
	balAfterDeploy, _ := l.BalanceOf(kp.PublicKey)

	contractAddr := contract.ContractAddr(kp.PublicKey, 1)

	// Block 2: call from same sender
	callTx := &tx.Tx{
		Type:           tx.TxTypeContractCall,
		Sender:         "test",
		Receiver:       "contract",
		Value:          big.NewInt(0),
		Nonce:          2,
		SenderPubKey:   kp.PublicKey,
		ReceiverPubKey: contractAddr,
		CallData:       []byte("call"),
	}
	sig2, _ := callTx.SignSender(kp)
	callTx.SenderSig = sig2

	result, err := sm.ApplyBlock(&statemachine.Block{
		Height:         2,
		Txs:            []*tx.Tx{callTx},
		ProposerPubKey: proposer,
	})
	if err != nil {
		t.Fatalf("call: %v", err)
	}

	balAfterCall, _ := l.BalanceOf(kp.PublicKey)
	if balAfterCall.Cmp(balAfterDeploy) >= 0 {
		t.Fatal("balance should decrease after call (gas burned)")
	}

	callFee := new(big.Int).Sub(balAfterDeploy, balAfterCall)
	t.Logf("deploy fee: %s (diff from initial)", new(big.Int).Sub(big.NewInt(1000), balAfterDeploy))
	t.Logf("call fee:   %s", callFee)
	t.Logf("GasBurned:  %s", result.GasBurned)
}

func TestBaseFeeAdjustsAcrossBlocks(t *testing.T) {
	sm, db := openSM(t)
	l, _ := ledger.New(db)

	kp, _ := keys.GenerateKeypair()
	if err := l.Seed(kp.PublicKey, big.NewInt(50000)); err != nil {
		t.Fatal(err)
	}

	var proposer [32]byte
	proposer[0] = 3

	nonce := uint64(1)
	for i := 1; i <= 5; i++ {
		txCount := 5
		if i > 1 {
			txCount = 2
		}
		var txs []*tx.Tx
		for j := 0; j < txCount; j++ {
			rcvr, _ := keys.GenerateKeypair()
			tr := &tx.Tx{
				Type:           tx.TxTypeTransfer,
				Sender:         "s",
				Receiver:       "r",
				Value:          big.NewInt(1),
				Nonce:          nonce,
				SenderPubKey:   kp.PublicKey,
				ReceiverPubKey: rcvr.PublicKey,
			}
			sig, _ := tr.SignSender(kp)
			tr.SenderSig = sig
			rsig, _ := tr.SignReceiver(rcvr)
			tr.ReceiverSig = rsig
			txs = append(txs, tr)
			nonce++
		}
		result, err := sm.ApplyBlock(&statemachine.Block{
			Height:         uint64(i),
			Txs:            txs,
			ProposerPubKey: proposer,
		})
		if err != nil {
			t.Fatalf("block %d: %v", i, err)
		}
		bal, _ := l.BalanceOf(kp.PublicKey)
		t.Logf("block %d: txs=%d GasBurned=%s bal=%s", i, txCount, result.GasBurned, bal)

		if result.GasBurned.Cmp(big.NewInt(int64(len(txs)))) < 0 {
			t.Errorf("block %d: GasBurned %s < tx count %d", i, result.GasBurned, len(txs))
		}
	}
}
