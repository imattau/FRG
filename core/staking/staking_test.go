package staking_test

import (
	"crypto/sha256"
	"math/big"
	"path/filepath"
	"testing"

	"github.com/imattau/frg/core/denom"
	rgerrors "github.com/imattau/frg/core/errors"
	"github.com/imattau/frg/core/keys"
	"github.com/imattau/frg/core/ledger"
	"github.com/imattau/frg/core/staking"
	bolt "go.etcd.io/bbolt"
)

func openStore(t *testing.T) (*staking.Store, *ledger.Ledger) {
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
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("staking.Close: %v", err)
		}
		if err := l.Close(); err != nil {
			t.Errorf("ledger.Close: %v", err)
		}
	})
	return s, l
}

func q(frg int64) *big.Int {
	return new(big.Int).Mul(big.NewInt(frg), denom.QuantaPerFRG)
}

func seedValidator(t *testing.T, l *ledger.Ledger, pub [32]byte, frg int64) {
	t.Helper()
	if err := l.Seed(pub, q(frg)); err != nil {
		t.Fatalf("Seed: %v", err)
	}
}

func escrowAccount(validator [32]byte) [32]byte {
	h := sha256.New()
	h.Write([]byte("FRG_STAKING_ESCROW_V1\x00"))
	h.Write(validator[:])
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

func sign(t *testing.T, kp *keys.Keypair, msg []byte) [64]byte {
	t.Helper()
	sig, err := kp.Sign(msg)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return sig
}

func assertCode(t *testing.T, err error, code rgerrors.ErrorCode) {
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

func TestBondValid(t *testing.T) {
	s, l := openStore(t)
	kp, _ := keys.GenerateKeypair()
	seedValidator(t, l, kp.PublicKey, 5000)

	if err := s.Bond(kp.PublicKey, q(2000), 1); err != nil {
		t.Fatalf("Bond: %v", err)
	}

	bal, _ := l.BalanceOf(kp.PublicKey)
	if bal.Cmp(q(3000)) != 0 {
		t.Fatalf("validator balance: got %v want 3000", bal)
	}
	escrow := escrowAccount(kp.PublicKey)
	escrowBal, _ := l.BalanceOf(escrow)
	if escrowBal.Cmp(q(2000)) != 0 {
		t.Fatalf("escrow balance: got %v want 2000", escrowBal)
	}
	vs, _ := s.ValidatorSet()
	if len(vs) != 1 {
		t.Fatalf("ValidatorSet len: got %d want 1", len(vs))
	}
	if vs[0] != kp.PublicKey {
		t.Fatalf("ValidatorSet returned wrong key")
	}
}

func TestBondBelowMinimum(t *testing.T) {
	s, l := openStore(t)
	kp, _ := keys.GenerateKeypair()
	seedValidator(t, l, kp.PublicKey, 5000)

	err := s.Bond(kp.PublicKey, q(999), 1)
	assertCode(t, err, rgerrors.ErrBondBelowMinimum)
}

func TestBondAlreadyBonded(t *testing.T) {
	s, l := openStore(t)
	kp, _ := keys.GenerateKeypair()
	seedValidator(t, l, kp.PublicKey, 5000)
	_ = s.Bond(kp.PublicKey, q(1000), 1)

	err := s.Bond(kp.PublicKey, q(1000), 2)
	assertCode(t, err, rgerrors.ErrAlreadyBonded)
}

func TestBondInsufficientFunds(t *testing.T) {
	s, l := openStore(t)
	kp, _ := keys.GenerateKeypair()
	seedValidator(t, l, kp.PublicKey, 500)

	err := s.Bond(kp.PublicKey, q(1000), 1)
	assertCode(t, err, rgerrors.ErrInsufficientFunds)
}

func TestUnbondValid(t *testing.T) {
	s, l := openStore(t)
	kp, _ := keys.GenerateKeypair()
	seedValidator(t, l, kp.PublicKey, 5000)
	_ = s.Bond(kp.PublicKey, q(1000), 1)

	if err := s.Unbond(kp.PublicKey, 10); err != nil {
		t.Fatalf("Unbond: %v", err)
	}

	vs, _ := s.ValidatorSet()
	if len(vs) != 0 {
		t.Fatalf("ValidatorSet: expected 0 after unbond, got %d", len(vs))
	}
}

func TestUnbondNotBonded(t *testing.T) {
	s, _ := openStore(t)
	kp, _ := keys.GenerateKeypair()
	err := s.Unbond(kp.PublicKey, 1)
	assertCode(t, err, rgerrors.ErrNotBonded)
}

func TestUnbondPending(t *testing.T) {
	s, l := openStore(t)
	kp, _ := keys.GenerateKeypair()
	seedValidator(t, l, kp.PublicKey, 5000)
	_ = s.Bond(kp.PublicKey, q(1000), 1)
	_ = s.Unbond(kp.PublicKey, 10)

	err := s.Unbond(kp.PublicKey, 11)
	assertCode(t, err, rgerrors.ErrUnbondingPending)
}

func TestFinalizeBeforeLockup(t *testing.T) {
	s, l := openStore(t)
	kp, _ := keys.GenerateKeypair()
	seedValidator(t, l, kp.PublicKey, 5000)
	_ = s.Bond(kp.PublicKey, q(1000), 1)
	_ = s.Unbond(kp.PublicKey, 100)

	if err := s.Finalize(kp.PublicKey, 1099); err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	bal, _ := l.BalanceOf(kp.PublicKey)
	if bal.Cmp(q(4000)) != 0 {
		t.Fatalf("balance: got %v want 4000", bal)
	}
	escrow := escrowAccount(kp.PublicKey)
	escrowBal, _ := l.BalanceOf(escrow)
	if escrowBal.Cmp(q(1000)) != 0 {
		t.Fatalf("escrow balance: got %v want 1000", escrowBal)
	}
}

func TestFinalizeAfterLockup(t *testing.T) {
	s, l := openStore(t)
	kp, _ := keys.GenerateKeypair()
	seedValidator(t, l, kp.PublicKey, 5000)
	_ = s.Bond(kp.PublicKey, q(1000), 1)
	_ = s.Unbond(kp.PublicKey, 100)

	if err := s.Finalize(kp.PublicKey, 1100); err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	bal, _ := l.BalanceOf(kp.PublicKey)
	if bal.Cmp(q(5000)) != 0 {
		t.Fatalf("balance: got %v want 5000", bal)
	}
	escrow := escrowAccount(kp.PublicKey)
	escrowBal, _ := l.BalanceOf(escrow)
	if escrowBal.Sign() != 0 {
		t.Fatalf("escrow balance: got %v want 0", escrowBal)
	}
	vs, _ := s.ValidatorSet()
	if len(vs) != 0 {
		t.Fatalf("ValidatorSet: expected 0, got %d", len(vs))
	}
}

func TestSlashBonded(t *testing.T) {
	s, l := openStore(t)
	kp, _ := keys.GenerateKeypair()
	seedValidator(t, l, kp.PublicKey, 5000)
	_ = s.Bond(kp.PublicKey, q(1000), 1)

	proof := staking.EquivocationProof{
		ValidatorPubKey: kp.PublicKey,
		HeaderA:         []byte("block-a"),
		SigA:            sign(t, kp, []byte("block-a")),
		HeaderB:         []byte("block-b"),
		SigB:            sign(t, kp, []byte("block-b")),
	}
	if err := s.Slash(kp.PublicKey, proof); err != nil {
		t.Fatalf("Slash: %v", err)
	}
	bal, _ := l.BalanceOf(kp.PublicKey)
	if bal.Cmp(q(4000)) != 0 {
		t.Fatalf("balance: got %v want 4000", bal)
	}
	escrow := escrowAccount(kp.PublicKey)
	escrowBal, _ := l.BalanceOf(escrow)
	if escrowBal.Sign() != 0 {
		t.Fatalf("escrow balance: got %v want 0", escrowBal)
	}
	vs, _ := s.ValidatorSet()
	if len(vs) != 0 {
		t.Fatalf("ValidatorSet: expected 0, got %d", len(vs))
	}
}

func TestSlashNotBonded(t *testing.T) {
	s, _ := openStore(t)
	kp, _ := keys.GenerateKeypair()
	proof := staking.EquivocationProof{ValidatorPubKey: kp.PublicKey}
	err := s.Slash(kp.PublicKey, proof)
	assertCode(t, err, rgerrors.ErrNotBonded)
}

func TestValidatorSet(t *testing.T) {
	s, l := openStore(t)
	kpA, _ := keys.GenerateKeypair()
	kpB, _ := keys.GenerateKeypair()
	seedValidator(t, l, kpA.PublicKey, 5000)
	seedValidator(t, l, kpB.PublicKey, 5000)
	_ = s.Bond(kpA.PublicKey, q(1000), 1)
	_ = s.Bond(kpB.PublicKey, q(1000), 1)
	_ = s.Unbond(kpB.PublicKey, 10)

	vs, _ := s.ValidatorSet()
	if len(vs) != 1 {
		t.Fatalf("ValidatorSet: expected 1 (bonded only), got %d", len(vs))
	}
	if vs[0] != kpA.PublicKey {
		t.Fatalf("ValidatorSet: unexpected validator")
	}
}

func TestBondedAmounts(t *testing.T) {
	s, l := openStore(t)
	kpA, _ := keys.GenerateKeypair()
	kpB, _ := keys.GenerateKeypair()
	kpC, _ := keys.GenerateKeypair()
	seedValidator(t, l, kpA.PublicKey, 5000)
	seedValidator(t, l, kpB.PublicKey, 5000)
	seedValidator(t, l, kpC.PublicKey, 5000)
	_ = s.Bond(kpA.PublicKey, q(1000), 1)
	_ = s.Bond(kpB.PublicKey, q(2000), 1)
	_ = s.Bond(kpC.PublicKey, q(3000), 1)
	_ = s.Unbond(kpC.PublicKey, 10)

	validators, amounts, err := s.BondedAmounts()
	if err != nil {
		t.Fatalf("BondedAmounts: %v", err)
	}
	if len(validators) != 2 {
		t.Fatalf("expected 2 bonded validators, got %d", len(validators))
	}
	total := new(big.Int)
	for _, a := range amounts {
		total.Add(total, a)
	}
	if total.Cmp(q(3000)) != 0 {
		t.Fatalf("total bonded: got %v want 3000", total)
	}
}

func TestPersistence(t *testing.T) {
	dir := t.TempDir()
	ledgerPath := filepath.Join(dir, "ledger.db")
	stakingPath := filepath.Join(dir, "staking.db")

	kp, _ := keys.GenerateKeypair()

	{
		l, _ := ledger.Open(ledgerPath)
		if err := l.Seed(kp.PublicKey, q(5000)); err != nil {
			t.Fatal(err)
		}
		s, _ := staking.Open(stakingPath, l)
		if err := s.Bond(kp.PublicKey, q(1000), 1); err != nil {
			t.Fatal(err)
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		if err := l.Close(); err != nil {
			t.Fatal(err)
		}
	}

	{
		l, err := ledger.Open(ledgerPath)
		if err != nil {
			t.Fatal(err)
		}
		s, err := staking.Open(stakingPath, l)
		if err != nil {
			t.Fatal(err)
		}
		vs, err := s.ValidatorSet()
		if err != nil {
			t.Fatal(err)
		}
		if len(vs) != 1 {
			t.Fatalf("after reopen: expected 1 validator, got %d", len(vs))
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		if err := l.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestStakingNew(t *testing.T) {
	dir := t.TempDir()
	db, err := bolt.Open(filepath.Join(dir, "shared.db"), 0600, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	l, err := ledger.New(db)
	if err != nil {
		t.Fatal(err)
	}
	s, err := staking.New(db, l)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_ = s
}
