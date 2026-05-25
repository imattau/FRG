package gas_test

import (
	"math/big"
	"path/filepath"
	"testing"

	"github.com/imattau/frg/core/gas"
	"github.com/imattau/frg/core/keys"
	"github.com/imattau/frg/core/ledger"
)

func openLedger(t *testing.T) *ledger.Ledger {
	t.Helper()
	l, err := ledger.Open(filepath.Join(t.TempDir(), "ledger.db"))
	if err != nil {
		t.Fatalf("ledger.Open: %v", err)
	}
	t.Cleanup(func() {
		if err := l.Close(); err != nil {
			t.Errorf("ledger.Close: %v", err)
		}
	})
	return l
}

func TestFeeAccountDeterministic(t *testing.T) {
	kp, _ := keys.GenerateKeypair()
	a := gas.FeeAccount(kp.PublicKey)
	b := gas.FeeAccount(kp.PublicKey)
	if a != b {
		t.Fatal("FeeAccount not deterministic")
	}
}

func TestFeeAccountUnique(t *testing.T) {
	kpA, _ := keys.GenerateKeypair()
	kpB, _ := keys.GenerateKeypair()
	if gas.FeeAccount(kpA.PublicKey) == gas.FeeAccount(kpB.PublicKey) {
		t.Fatal("FeeAccount collision")
	}
}

func TestBaseFeeAtTarget(t *testing.T) {
	base := big.NewInt(1000)
	result := gas.BaseFee(base, gas.TargetTxCount)
	if result.Cmp(base) != 0 {
		t.Fatalf("at target: got %v want 1000", result)
	}
}

func TestBaseFeeAboveTarget(t *testing.T) {
	base := big.NewInt(1000)
	result := gas.BaseFee(base, gas.TargetTxCount*2)
	expected := big.NewInt(1125)
	if result.Cmp(expected) != 0 {
		t.Fatalf("above target: got %v want %v", result, expected)
	}
}

func TestBaseFeeBelowTarget(t *testing.T) {
	base := big.NewInt(1000)
	result := gas.BaseFee(base, 0)
	expected := big.NewInt(875)
	if result.Cmp(expected) != 0 {
		t.Fatalf("below target: got %v want %v", result, expected)
	}
}

func TestBaseFeeFloor(t *testing.T) {
	base := big.NewInt(1)
	result := gas.BaseFee(base, 0)
	if result.Cmp(big.NewInt(1)) != 0 {
		t.Fatalf("floor: got %v want 1", result)
	}
}

func TestAccrueEqual(t *testing.T) {
	l := openLedger(t)
	kpA, _ := keys.GenerateKeypair()
	kpB, _ := keys.GenerateKeypair()
	validators := [][32]byte{kpA.PublicKey, kpB.PublicKey}
	bonds := []*big.Int{big.NewInt(1000), big.NewInt(1000)}

	totalFees := big.NewInt(200)
	if err := gas.Accrue(l, totalFees, validators, bonds); err != nil {
		t.Fatalf("Accrue: %v", err)
	}

	balA, _ := gas.Claimable(l, kpA.PublicKey)
	balB, _ := gas.Claimable(l, kpB.PublicKey)
	if balA.Cmp(big.NewInt(100)) != 0 {
		t.Fatalf("balA: got %v want 100", balA)
	}
	if balB.Cmp(big.NewInt(100)) != 0 {
		t.Fatalf("balB: got %v want 100", balB)
	}
}

func TestAccrueProportional(t *testing.T) {
	l := openLedger(t)
	kpA, _ := keys.GenerateKeypair()
	kpB, _ := keys.GenerateKeypair()
	validators := [][32]byte{kpA.PublicKey, kpB.PublicKey}
	bonds := []*big.Int{big.NewInt(3000), big.NewInt(1000)}

	totalFees := big.NewInt(800)
	if err := gas.Accrue(l, totalFees, validators, bonds); err != nil {
		t.Fatalf("Accrue: %v", err)
	}

	balA, _ := gas.Claimable(l, kpA.PublicKey)
	balB, _ := gas.Claimable(l, kpB.PublicKey)
	if balA.Cmp(big.NewInt(460)) != 0 {
		t.Fatalf("balA: got %v want 460", balA)
	}
	if balB.Cmp(big.NewInt(340)) != 0 {
		t.Fatalf("balB: got %v want 340", balB)
	}
}

func TestAccrueAccumulates(t *testing.T) {
	l := openLedger(t)
	kp, _ := keys.GenerateKeypair()
	validators := [][32]byte{kp.PublicKey}
	bonds := []*big.Int{big.NewInt(1000)}

	_ = gas.Accrue(l, big.NewInt(100), validators, bonds)
	_ = gas.Accrue(l, big.NewInt(100), validators, bonds)

	bal, _ := gas.Claimable(l, kp.PublicKey)
	if bal.Cmp(big.NewInt(200)) != 0 {
		t.Fatalf("accumulated: got %v want 200", bal)
	}
}

func TestClaimValid(t *testing.T) {
	l := openLedger(t)
	kp, _ := keys.GenerateKeypair()
	validators := [][32]byte{kp.PublicKey}
	bonds := []*big.Int{big.NewInt(1000)}

	_ = gas.Accrue(l, big.NewInt(100), validators, bonds)

	if err := gas.Claim(l, kp.PublicKey); err != nil {
		t.Fatalf("Claim: %v", err)
	}

	claimable, _ := gas.Claimable(l, kp.PublicKey)
	if claimable.Sign() != 0 {
		t.Fatalf("claimable after claim: got %v want 0", claimable)
	}

	bal, _ := l.BalanceOf(kp.PublicKey)
	if bal.Cmp(big.NewInt(100)) != 0 {
		t.Fatalf("main balance: got %v want 100", bal)
	}
}

func TestClaimZero(t *testing.T) {
	l := openLedger(t)
	kp, _ := keys.GenerateKeypair()

	if err := gas.Claim(l, kp.PublicKey); err != nil {
		t.Fatalf("Claim zero: %v", err)
	}
	bal, _ := l.BalanceOf(kp.PublicKey)
	if bal.Sign() != 0 {
		t.Fatalf("balance after zero claim: got %v want 0", bal)
	}
}

func TestClaimable(t *testing.T) {
	l := openLedger(t)
	kp, _ := keys.GenerateKeypair()
	validators := [][32]byte{kp.PublicKey}
	bonds := []*big.Int{big.NewInt(1000)}
	_ = gas.Accrue(l, big.NewInt(100), validators, bonds)

	bal, err := gas.Claimable(l, kp.PublicKey)
	if err != nil {
		t.Fatalf("Claimable: %v", err)
	}
	if bal.Cmp(big.NewInt(100)) != 0 {
		t.Fatalf("claimable: got %v want 100", bal)
	}
}
