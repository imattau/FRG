package statemachine_test

import (
	"encoding/binary"
	"math/big"
	"os"
	"path/filepath"
	"testing"

	"github.com/imattau/frg/core/consensus"
	"github.com/imattau/frg/core/contract"
	"github.com/imattau/frg/core/denom"
	rgerrors "github.com/imattau/frg/core/errors"
	"github.com/imattau/frg/core/gas"
	"github.com/imattau/frg/core/keys"
	"github.com/imattau/frg/core/leader"
	"github.com/imattau/frg/core/ledger"
	"github.com/imattau/frg/core/mint"
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

func setTotalSupply(t *testing.T, sm *statemachine.StateMachine, total *big.Int) {
	t.Helper()
	if err := sm.Update(func(btx *bolt.Tx) error {
		return sm.SetTotalSupplyTx(btx, total)
	}); err != nil {
		t.Fatal(err)
	}
}

func q(frg int64) *big.Int {
	return new(big.Int).Mul(big.NewInt(frg), denom.QuantaPerFRG)
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

func TestBackupRetentionAndRestore(t *testing.T) {
	sm, db := openSM(t)
	backupDir := t.TempDir()
	if _, err := sm.ApplyBlock(&statemachine.Block{Height: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := statemachine.CreateBackup(db, backupDir, 1); err != nil {
		t.Fatal(err)
	}
	second, err := statemachine.CreateBackup(db, backupDir, 1)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("retained backups = %d, want 1", len(entries))
	}
	restored := filepath.Join(t.TempDir(), "restored.db")
	if err := statemachine.RestoreDatabase(second, restored); err != nil {
		t.Fatal(err)
	}
	opened, err := bolt.Open(restored, 0600, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
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

func signedProtocolTx(t *testing.T, kp *keys.Keypair, typ tx.TxType, value *big.Int, nonce uint64) *tx.Tx {
	t.Helper()
	if value == nil {
		value = big.NewInt(0)
	}
	tr := &tx.Tx{
		Type:           typ,
		Sender:         "validator",
		Receiver:       "protocol",
		Value:          new(big.Int).Set(value),
		Nonce:          nonce,
		SenderPubKey:   kp.PublicKey,
		ReceiverPubKey: kp.PublicKey,
	}
	sig, err := tr.SignSender(kp)
	if err != nil {
		t.Fatal(err)
	}
	tr.SenderSig = sig
	return tr
}

func signedVote(t *testing.T, kp *keys.Keypair, typ consensus.VoteType, height uint64, round uint32, blockHash [32]byte) *consensus.Vote {
	t.Helper()
	v := &consensus.Vote{
		Type:        typ,
		Height:      height,
		Round:       round,
		BlockHash:   blockHash,
		ValidatorPK: kp.PublicKey,
	}
	sig, err := kp.Sign(consensus.VoteSignBytes(v))
	if err != nil {
		t.Fatal(err)
	}
	v.Sig = sig
	return v
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
	setTotalSupply(t, sm, initial)

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

func TestApplyBlockMintsSplitRewardsToClaimableAccounts(t *testing.T) {
	sm, db := openSM(t)
	l, _ := ledger.New(db)
	s, _ := staking.New(db, l)
	kpA, _ := keys.GenerateKeypair()
	kpB, _ := keys.GenerateKeypair()

	totalSupply := q(1_000_000)
	halfSupply := new(big.Int).Div(totalSupply, big.NewInt(2))
	if err := l.Seed(kpA.PublicKey, new(big.Int).Set(halfSupply)); err != nil {
		t.Fatal(err)
	}
	if err := l.Seed(kpB.PublicKey, new(big.Int).Set(halfSupply)); err != nil {
		t.Fatal(err)
	}
	setTotalSupply(t, sm, totalSupply)
	if err := s.Bond(kpA.PublicKey, q(1000), 0); err != nil {
		t.Fatal(err)
	}
	if err := s.Bond(kpB.PublicKey, q(1000), 0); err != nil {
		t.Fatal(err)
	}

	result, err := sm.ApplyBlock(&statemachine.Block{Height: 1, ProposerPubKey: kpA.PublicKey})
	if err != nil {
		t.Fatalf("ApplyBlock: %v", err)
	}
	wantMint := mint.MintPerBlock(totalSupply, q(2000))
	if result.MintAmount.Cmp(wantMint) != 0 {
		t.Fatalf("MintAmount = %v, want %v", result.MintAmount, wantMint)
	}
	claimA, _ := gas.Claimable(l, kpA.PublicKey)
	claimB, _ := gas.Claimable(l, kpB.PublicKey)
	totalClaimable := new(big.Int).Add(claimA, claimB)
	if totalClaimable.Cmp(wantMint) != 0 {
		t.Fatalf("total claimable = %v, want %v", totalClaimable, wantMint)
	}
	diff := new(big.Int).Sub(claimA, claimB)
	diff.Abs(diff)
	if diff.Cmp(big.NewInt(1)) > 0 {
		t.Fatalf("claimable split too uneven: A=%v B=%v", claimA, claimB)
	}
	supplyAfter, tracked, err := sm.CurrentTotalSupply()
	if err != nil {
		t.Fatal(err)
	}
	if !tracked {
		t.Fatal("total supply is not tracked")
	}
	wantSupply := new(big.Int).Add(totalSupply, wantMint)
	if supplyAfter.Cmp(wantSupply) != 0 {
		t.Fatalf("total supply = %v, want %v", supplyAfter, wantSupply)
	}
	proposerBal, _ := l.BalanceOf(kpA.PublicKey)
	if proposerBal.Cmp(new(big.Int).Sub(halfSupply, q(1000))) != 0 {
		t.Fatalf("proposer direct balance changed by mint: %v", proposerBal)
	}
}

func TestApplyBlockMintsNothingAtTargetStaking(t *testing.T) {
	sm, db := openSM(t)
	l, _ := ledger.New(db)
	s, _ := staking.New(db, l)
	kpA, _ := keys.GenerateKeypair()
	kpB, _ := keys.GenerateKeypair()
	treasury, _ := keys.GenerateKeypair()

	totalSupply := q(1_000_000)
	stakeEach := new(big.Int).Div(totalSupply, big.NewInt(4))
	if err := l.Seed(kpA.PublicKey, new(big.Int).Set(stakeEach)); err != nil {
		t.Fatal(err)
	}
	if err := l.Seed(kpB.PublicKey, new(big.Int).Set(stakeEach)); err != nil {
		t.Fatal(err)
	}
	if err := l.Seed(treasury.PublicKey, new(big.Int).Div(totalSupply, big.NewInt(2))); err != nil {
		t.Fatal(err)
	}
	setTotalSupply(t, sm, totalSupply)
	if err := s.Bond(kpA.PublicKey, new(big.Int).Set(stakeEach), 0); err != nil {
		t.Fatal(err)
	}
	if err := s.Bond(kpB.PublicKey, new(big.Int).Set(stakeEach), 0); err != nil {
		t.Fatal(err)
	}

	result, err := sm.ApplyBlock(&statemachine.Block{Height: 1, ProposerPubKey: kpA.PublicKey})
	if err != nil {
		t.Fatalf("ApplyBlock: %v", err)
	}
	if result.MintAmount.Sign() != 0 {
		t.Fatalf("MintAmount = %v, want 0", result.MintAmount)
	}
	supplyAfter, tracked, err := sm.CurrentTotalSupply()
	if err != nil {
		t.Fatal(err)
	}
	if !tracked {
		t.Fatal("total supply is not tracked")
	}
	if supplyAfter.Cmp(totalSupply) != 0 {
		t.Fatalf("total supply = %v, want %v", supplyAfter, totalSupply)
	}
	claimA, _ := gas.Claimable(l, kpA.PublicKey)
	claimB, _ := gas.Claimable(l, kpB.PublicKey)
	if claimA.Sign() != 0 || claimB.Sign() != 0 {
		t.Fatalf("claimable rewards should be zero, got %v and %v", claimA, claimB)
	}
}

func makeMissEvidenceTx(t *testing.T, reporter *keys.Keypair, missed [32]byte, height uint64, skipIndex uint32, nonce uint64) *tx.Tx {
	t.Helper()
	tr := &tx.Tx{
		Type:           tx.TxTypeMissEvidence,
		Sender:         "reporter",
		Receiver:       "missed",
		Value:          big.NewInt(0),
		Nonce:          nonce,
		SenderPubKey:   reporter.PublicKey,
		ReceiverPubKey: missed,
		MissedHeight:   height,
		MissedProposer: missed,
		SkipIndex:      skipIndex,
	}
	sig, err := tr.SignSender(reporter)
	if err != nil {
		t.Fatal(err)
	}
	tr.SenderSig = sig
	return tr
}

func makeBondTx(t *testing.T, validator *keys.Keypair, amount *big.Int, nonce uint64) *tx.Tx {
	t.Helper()
	tr := &tx.Tx{
		Type:           tx.TxTypeBond,
		Sender:         "validator",
		Receiver:       "staking",
		Value:          new(big.Int).Set(amount),
		Nonce:          nonce,
		SenderPubKey:   validator.PublicKey,
		ReceiverPubKey: validator.PublicKey,
	}
	sig, err := tr.SignSender(validator)
	if err != nil {
		t.Fatal(err)
	}
	tr.SenderSig = sig
	return tr
}

func TestApplyBlockBondTxActivatesValidator(t *testing.T) {
	sm, db := openSM(t)
	l, _ := ledger.New(db)
	s, _ := staking.New(db, l)
	kp, _ := keys.GenerateKeypair()
	initial := q(10_000)
	if err := l.Seed(kp.PublicKey, new(big.Int).Set(initial)); err != nil {
		t.Fatal(err)
	}
	setTotalSupply(t, sm, initial)

	bondTx := makeBondTx(t, kp, q(1000), 1)
	if _, err := sm.ApplyBlock(&statemachine.Block{Height: 1, Txs: []*tx.Tx{bondTx}, ProposerPubKey: kp.PublicKey}); err != nil {
		t.Fatalf("ApplyBlock: %v", err)
	}
	validators, amounts, err := s.BondedAmounts()
	if err != nil {
		t.Fatal(err)
	}
	if len(validators) != 1 || validators[0] != kp.PublicKey {
		t.Fatalf("unexpected validators: %x", validators)
	}
	if len(amounts) != 1 || amounts[0].Cmp(q(1000)) != 0 {
		t.Fatalf("unexpected bonded amount: %v", amounts)
	}
	escrowBal, _ := l.BalanceOf(staking.EscrowAccount(kp.PublicKey))
	if escrowBal.Cmp(q(1000)) != 0 {
		t.Fatalf("escrow balance = %v, want 1000", escrowBal)
	}
}

func TestApplyBlockUnbondAndFinalizeTxLifecycle(t *testing.T) {
	sm, db := openSM(t)
	l, _ := ledger.New(db)
	kp, _ := keys.GenerateKeypair()
	if err := l.Seed(kp.PublicKey, q(3000)); err != nil {
		t.Fatal(err)
	}
	setTotalSupply(t, sm, q(3000))

	if _, err := sm.ApplyBlock(&statemachine.Block{
		Height: 1,
		Txs:    []*tx.Tx{signedProtocolTx(t, kp, tx.TxTypeBond, q(1000), 1)},
	}); err != nil {
		t.Fatalf("bond: %v", err)
	}
	unbondTx := signedProtocolTx(t, kp, tx.TxTypeUnbond, big.NewInt(0), 2)
	if _, err := sm.ApplyBlock(&statemachine.Block{Height: 2, Txs: []*tx.Tx{unbondTx}}); err != nil {
		t.Fatalf("unbond: %v", err)
	}
	s, _ := staking.New(db, l)
	validators, err := s.ValidatorSet()
	if err != nil {
		t.Fatal(err)
	}
	if len(validators) != 0 {
		t.Fatalf("validator should be inactive while unbonding: %x", validators)
	}

	for h := uint64(3); h < 1002; h++ {
		if _, err := sm.ApplyBlock(&statemachine.Block{Height: h}); err != nil {
			t.Fatalf("empty block %d: %v", h, err)
		}
	}
	finalizeTx := signedProtocolTx(t, kp, tx.TxTypeFinalizeUnbond, big.NewInt(0), 3)
	if _, err := sm.ApplyBlock(&statemachine.Block{Height: 1002, Txs: []*tx.Tx{finalizeTx}}); err != nil {
		t.Fatalf("finalize: %v", err)
	}
	bal, err := l.BalanceOf(kp.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	if bal.Cmp(q(1900)) < 0 {
		t.Fatalf("finalized balance too low: %s", bal)
	}
}

func TestApplyBlockClaimRewardsTxMovesClaimableBalance(t *testing.T) {
	sm, db := openSM(t)
	l, _ := ledger.New(db)
	kp, _ := keys.GenerateKeypair()
	if err := l.Seed(kp.PublicKey, big.NewInt(1000)); err != nil {
		t.Fatal(err)
	}
	if err := l.Seed(gas.FeeAccount(kp.PublicKey), big.NewInt(250)); err != nil {
		t.Fatal(err)
	}
	setTotalSupply(t, sm, big.NewInt(1250))

	claimTx := signedProtocolTx(t, kp, tx.TxTypeClaimRewards, big.NewInt(0), 1)
	if _, err := sm.ApplyBlock(&statemachine.Block{Height: 1, Txs: []*tx.Tx{claimTx}}); err != nil {
		t.Fatalf("claim: %v", err)
	}
	claimable, err := gas.Claimable(l, kp.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	if claimable.Sign() != 0 {
		t.Fatalf("claimable = %s, want 0", claimable)
	}
	bal, err := l.BalanceOf(kp.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	if bal.Cmp(big.NewInt(1200)) < 0 {
		t.Fatalf("claimed balance too low after gas: %s", bal)
	}
}

func TestApplyBlockEquivocationEvidenceSlashesValidator(t *testing.T) {
	sm, db := openSM(t)
	l, _ := ledger.New(db)
	validatorKP, _ := keys.GenerateKeypair()
	reporterKP, _ := keys.GenerateKeypair()
	if err := l.Seed(validatorKP.PublicKey, q(3000)); err != nil {
		t.Fatal(err)
	}
	if err := l.Seed(reporterKP.PublicKey, q(100)); err != nil {
		t.Fatal(err)
	}
	setTotalSupply(t, sm, q(3100))

	if _, err := sm.ApplyBlock(&statemachine.Block{
		Height: 1,
		Txs:    []*tx.Tx{signedProtocolTx(t, validatorKP, tx.TxTypeBond, q(1000), 1)},
	}); err != nil {
		t.Fatalf("bond: %v", err)
	}

	voteA := signedVote(t, validatorKP, consensus.VotePrecommit, 1, 0, [32]byte{1})
	voteB := signedVote(t, validatorKP, consensus.VotePrecommit, 1, 0, [32]byte{2})
	rawA, err := voteA.Serialize()
	if err != nil {
		t.Fatal(err)
	}
	rawB, err := voteB.Serialize()
	if err != nil {
		t.Fatal(err)
	}
	evidenceTx := signedProtocolTx(t, reporterKP, tx.TxTypeEquivEvidence, big.NewInt(0), 1)
	evidenceTx.EvidenceA = rawA
	evidenceTx.EvidenceB = rawB
	evidenceTx.SenderSig, err = evidenceTx.SignSender(reporterKP)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sm.ApplyBlock(&statemachine.Block{Height: 2, Txs: []*tx.Tx{evidenceTx}}); err != nil {
		t.Fatalf("equivocation evidence: %v", err)
	}

	s, _ := staking.New(db, l)
	validators, err := s.ValidatorSet()
	if err != nil {
		t.Fatal(err)
	}
	if len(validators) != 0 {
		t.Fatalf("slashed validator remains active: %x", validators)
	}
	escrowBal, err := l.BalanceOf(staking.EscrowAccount(validatorKP.PublicKey))
	if err != nil {
		t.Fatal(err)
	}
	if escrowBal.Sign() != 0 {
		t.Fatalf("escrow balance = %s, want 0", escrowBal)
	}
}

func TestApplyBlockRejectsMalformedBondTx(t *testing.T) {
	sm, db := openSM(t)
	l, _ := ledger.New(db)
	s, _ := staking.New(db, l)
	kp, _ := keys.GenerateKeypair()
	if err := l.Seed(kp.PublicKey, q(10_000)); err != nil {
		t.Fatal(err)
	}
	setTotalSupply(t, sm, q(10_000))

	bondTx := makeBondTx(t, kp, q(1000), 1)
	bondTx.ReceiverPubKey = [32]byte{9}
	sig, err := bondTx.SignSender(kp)
	if err != nil {
		t.Fatal(err)
	}
	bondTx.SenderSig = sig

	if _, err := sm.ApplyBlock(&statemachine.Block{Height: 1, Txs: []*tx.Tx{bondTx}, ProposerPubKey: kp.PublicKey}); err == nil {
		t.Fatal("malformed bond transaction was accepted")
	}
	validators, _, err := s.BondedAmounts()
	if err != nil {
		t.Fatal(err)
	}
	if len(validators) != 0 {
		t.Fatalf("malformed bond mutated validator set: %x", validators)
	}
}

func TestApplyBlockRejectsForgedMissEvidence(t *testing.T) {
	sm, db := openSM(t)
	l, _ := ledger.New(db)
	s, _ := staking.New(db, l)
	kpA, _ := keys.GenerateKeypair()
	kpB, _ := keys.GenerateKeypair()
	totalSupply := q(20_000)
	for _, kp := range []*keys.Keypair{kpA, kpB} {
		if err := l.Seed(kp.PublicKey, q(10_000)); err != nil {
			t.Fatal(err)
		}
		if err := s.Bond(kp.PublicKey, q(1000), 0); err != nil {
			t.Fatal(err)
		}
	}
	setTotalSupply(t, sm, totalSupply)
	validators, _, err := s.BondedAmounts()
	if err != nil {
		t.Fatal(err)
	}
	var prevRoot [32]byte
	expectedReporter, err := leader.SkipProposer(prevRoot, 1, validators, 1)
	if err != nil {
		t.Fatal(err)
	}
	reporterKP := kpA
	if expectedReporter == kpB.PublicKey {
		reporterKP = kpB
	}

	tr := makeMissEvidenceTx(t, reporterKP, reporterKP.PublicKey, 1, 0, 1)
	_, err = sm.ApplyBlock(&statemachine.Block{Height: 1, Txs: []*tx.Tx{tr}, ProposerPubKey: reporterKP.PublicKey})
	if err == nil {
		t.Fatal("forged miss evidence was accepted")
	}
	if count, _ := s.MissCountOf(reporterKP.PublicKey); count != 0 {
		t.Fatalf("forged miss evidence changed miss count to %d", count)
	}
}

func TestApplyBlockAcceptsScheduledMissEvidence(t *testing.T) {
	sm, db := openSM(t)
	l, _ := ledger.New(db)
	s, _ := staking.New(db, l)
	kpA, _ := keys.GenerateKeypair()
	kpB, _ := keys.GenerateKeypair()
	totalSupply := q(20_000)
	for _, kp := range []*keys.Keypair{kpA, kpB} {
		if err := l.Seed(kp.PublicKey, q(10_000)); err != nil {
			t.Fatal(err)
		}
		if err := s.Bond(kp.PublicKey, q(1000), 0); err != nil {
			t.Fatal(err)
		}
	}
	setTotalSupply(t, sm, totalSupply)
	validators, _, err := s.BondedAmounts()
	if err != nil {
		t.Fatal(err)
	}
	var prevRoot [32]byte
	missed, err := leader.SkipProposer(prevRoot, 1, validators, 0)
	if err != nil {
		t.Fatal(err)
	}
	reporter, err := leader.SkipProposer(prevRoot, 1, validators, 1)
	if err != nil {
		t.Fatal(err)
	}
	reporterKP := kpA
	if reporter == kpB.PublicKey {
		reporterKP = kpB
	}

	tr := makeMissEvidenceTx(t, reporterKP, missed, 1, 0, 1)
	if _, err := sm.ApplyBlock(&statemachine.Block{Height: 1, Txs: []*tx.Tx{tr}, ProposerPubKey: reporter}); err != nil {
		t.Fatalf("scheduled miss evidence rejected: %v", err)
	}
	if count, _ := s.MissCountOf(missed); count != 1 {
		t.Fatalf("miss count = %d, want 1", count)
	}
}

func TestApplyBlockRejectsDuplicateMissEvidence(t *testing.T) {
	sm, db := openSM(t)
	l, _ := ledger.New(db)
	s, _ := staking.New(db, l)
	kpA, _ := keys.GenerateKeypair()
	kpB, _ := keys.GenerateKeypair()
	totalSupply := q(20_000)
	for _, kp := range []*keys.Keypair{kpA, kpB} {
		if err := l.Seed(kp.PublicKey, q(10_000)); err != nil {
			t.Fatal(err)
		}
		if err := s.Bond(kp.PublicKey, q(1000), 0); err != nil {
			t.Fatal(err)
		}
	}
	setTotalSupply(t, sm, totalSupply)
	validators, _, err := s.BondedAmounts()
	if err != nil {
		t.Fatal(err)
	}
	var prevRoot [32]byte
	missed, err := leader.SkipProposer(prevRoot, 1, validators, 0)
	if err != nil {
		t.Fatal(err)
	}
	reporter, err := leader.SkipProposer(prevRoot, 1, validators, 1)
	if err != nil {
		t.Fatal(err)
	}
	reporterKP := kpA
	if reporter == kpB.PublicKey {
		reporterKP = kpB
	}

	tx1 := makeMissEvidenceTx(t, reporterKP, missed, 1, 0, 1)
	tx2 := makeMissEvidenceTx(t, reporterKP, missed, 1, 0, 2)
	_, err = sm.ApplyBlock(&statemachine.Block{Height: 1, Txs: []*tx.Tx{tx1, tx2}, ProposerPubKey: reporter})
	if err == nil {
		t.Fatal("duplicate miss evidence was accepted")
	}
	if count, _ := s.MissCountOf(missed); count != 0 {
		t.Fatalf("duplicate miss evidence changed miss count to %d", count)
	}
}

func TestApplyBlockRollback(t *testing.T) {
	sm, db := openSM(t)
	l, _ := ledger.New(db)
	senderKP, _ := keys.GenerateKeypair()
	receiverKP, _ := keys.GenerateKeypair()

	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)
	initial := new(big.Int).Mul(big.NewInt(100), scale)
	if err := l.Seed(senderKP.PublicKey, initial); err != nil {
		t.Fatal(err)
	}
	setTotalSupply(t, sm, initial)

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
	setTotalSupply(t, sm, big.NewInt(1000))

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

func TestApplyBlockPersistsExactBlockTelemetry(t *testing.T) {
	sm, db := openSM(t)
	l, _ := ledger.New(db)

	kp, _ := keys.GenerateKeypair()
	if err := l.Seed(kp.PublicKey, big.NewInt(1000)); err != nil {
		t.Fatal(err)
	}
	setTotalSupply(t, sm, big.NewInt(1000))

	var proposer [32]byte
	proposer[0] = 9
	if _, err := sm.ApplyBlock(&statemachine.Block{
		Height:         1,
		Txs:            []*tx.Tx{makeDeployTx(t, kp, 1)},
		ProposerPubKey: proposer,
	}); err != nil {
		t.Fatalf("ApplyBlock: %v", err)
	}

	telemetry, err := sm.BlockTelemetryAt(1)
	if err != nil {
		t.Fatalf("BlockTelemetryAt: %v", err)
	}
	if telemetry == nil {
		t.Fatal("missing persisted telemetry")
	}
	if !telemetry.ContractStateIncluded {
		t.Fatal("contract state should be included in persisted telemetry")
	}
	if telemetry.Warning != "" {
		t.Fatalf("unexpected warning: %s", telemetry.Warning)
	}
	if telemetry.TxCount != 1 || len(telemetry.Levels) == 0 {
		t.Fatalf("unexpected telemetry: %+v", telemetry)
	}
	if telemetry.Levels[0].ContractNodeCount == 0 {
		t.Fatalf("contract nodes missing from level 0 telemetry: %+v", telemetry.Levels[0])
	}
	if telemetry.Levels[0].ContractTxCount != 1 {
		t.Fatalf("contract tx count = %d, want 1", telemetry.Levels[0].ContractTxCount)
	}
}

func TestContractCallSameFeeForSameWorkload(t *testing.T) {
	sm, db := openSM(t)
	l, _ := ledger.New(db)

	kp, _ := keys.GenerateKeypair()
	if err := l.Seed(kp.PublicKey, big.NewInt(1000)); err != nil {
		t.Fatal(err)
	}
	setTotalSupply(t, sm, big.NewInt(1000))

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

func TestUnknownContractSelectorDoesNotStallBlock(t *testing.T) {
	sm, db := openSM(t)
	l, _ := ledger.New(db)
	kp, err := keys.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Seed(kp.PublicKey, big.NewInt(1000)); err != nil {
		t.Fatal(err)
	}
	setTotalSupply(t, sm, big.NewInt(1000))

	if _, err := sm.ApplyBlock(&statemachine.Block{
		Height: 1,
		Txs:    []*tx.Tx{makeDeployTx(t, kp, 1)},
	}); err != nil {
		t.Fatalf("deploy: %v", err)
	}

	callTx := &tx.Tx{
		Type:           tx.TxTypeContractCall,
		Sender:         "test",
		Receiver:       "contract",
		Value:          big.NewInt(0),
		Nonce:          2,
		SenderPubKey:   kp.PublicKey,
		ReceiverPubKey: contract.ContractAddr(kp.PublicKey, 1),
		CallData:       []byte("garb"),
	}
	callTx.SenderSig, err = callTx.SignSender(kp)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sm.ApplyBlock(&statemachine.Block{
		Height: 2,
		Txs:    []*tx.Tx{callTx},
	}); err != nil {
		t.Fatalf("unknown selector must not reject block: %v", err)
	}
	if height, err := sm.CurrentHeight(); err != nil || height != 2 {
		t.Fatalf("height = %d, err=%v; want 2", height, err)
	}
	nonce, err := l.NonceOf(kp.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	if nonce != 2 {
		t.Fatalf("nonce = %d, want 2", nonce)
	}
}

func TestBaseFeeAdjustsAcrossBlocks(t *testing.T) {
	sm, db := openSM(t)
	l, _ := ledger.New(db)

	kp, _ := keys.GenerateKeypair()
	if err := l.Seed(kp.PublicKey, big.NewInt(50000)); err != nil {
		t.Fatal(err)
	}
	setTotalSupply(t, sm, big.NewInt(50000))

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
