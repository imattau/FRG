package mint_test

import (
	"math/big"
	"path/filepath"
	"testing"

	"github.com/imattau/frg/core/gas"
	"github.com/imattau/frg/core/keys"
	"github.com/imattau/frg/core/ledger"
	"github.com/imattau/frg/core/mint"
)

// supply returns n FRG tokens as quanta (n × 10^18).
func frg(n int64) *big.Int {
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)
	return new(big.Int).Mul(big.NewInt(n), scale)
}

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

func TestMintPerBlockAtTarget(t *testing.T) {
	supply := frg(400_000_000)
	staked := new(big.Int).Div(supply, big.NewInt(2))
	result := mint.MintPerBlock(supply, staked)
	if result.Sign() != 0 {
		t.Fatalf("at target: expected 0, got %v", result)
	}
}

func TestMintPerBlockAboveTarget(t *testing.T) {
	supply := frg(400_000_000)
	staked := new(big.Int).Mul(supply, big.NewInt(3))
	staked.Div(staked, big.NewInt(4))
	result := mint.MintPerBlock(supply, staked)
	if result.Sign() != 0 {
		t.Fatalf("above target: expected 0, got %v", result)
	}
}

func TestMintPerBlockBelowTarget(t *testing.T) {
	supply := frg(400_000_000)
	staked := big.NewInt(0)
	result := mint.MintPerBlock(supply, staked)

	expected := new(big.Int).Mul(supply, big.NewInt(10))
	expected.Div(expected, big.NewInt(100))
	expected.Div(expected, big.NewInt(mint.BlocksPerYear))

	if result.Cmp(expected) != 0 {
		t.Fatalf("zero staking: got %v want %v", result, expected)
	}
}

func TestMintPerBlockPartialDeficit(t *testing.T) {
	supply := frg(400_000_000)
	staked := new(big.Int).Div(supply, big.NewInt(4))
	result := mint.MintPerBlock(supply, staked)

	maxPerBlock := new(big.Int).Mul(supply, big.NewInt(10))
	maxPerBlock.Div(maxPerBlock, big.NewInt(100))
	maxPerBlock.Div(maxPerBlock, big.NewInt(mint.BlocksPerYear))

	expected := new(big.Int).Div(maxPerBlock, big.NewInt(2))
	if result.Cmp(expected) != 0 {
		t.Fatalf("partial deficit: got %v want %v", result, expected)
	}
}

func TestSplitRewardEqual(t *testing.T) {
	reward := big.NewInt(300)
	shares := mint.SplitReward(reward, 3)
	if len(shares) != 3 {
		t.Fatalf("len: got %d want 3", len(shares))
	}
	for i, s := range shares {
		if s.Cmp(big.NewInt(100)) != 0 {
			t.Fatalf("share[%d]: got %v want 100", i, s)
		}
	}
}

func TestSplitRewardRemainder(t *testing.T) {
	reward := big.NewInt(100)
	shares := mint.SplitReward(reward, 3)
	if shares[0].Cmp(big.NewInt(34)) != 0 {
		t.Fatalf("share[0]: got %v want 34", shares[0])
	}
	if shares[1].Cmp(big.NewInt(33)) != 0 {
		t.Fatalf("share[1]: got %v want 33", shares[1])
	}
	if shares[2].Cmp(big.NewInt(33)) != 0 {
		t.Fatalf("share[2]: got %v want 33", shares[2])
	}
}

func TestMintCreditsAccount(t *testing.T) {
	l := openLedger(t)
	kp, _ := keys.GenerateKeypair()

	if err := mint.Mint(l, kp.PublicKey, big.NewInt(500)); err != nil {
		t.Fatalf("Mint: %v", err)
	}

	bal, err := l.BalanceOf(gas.FeeAccount(kp.PublicKey))
	if err != nil {
		t.Fatalf("BalanceOf: %v", err)
	}
	if bal.Cmp(big.NewInt(500)) != 0 {
		t.Fatalf("fee account balance: got %v want 500", bal)
	}
}

func TestMintAccumulates(t *testing.T) {
	l := openLedger(t)
	kp, _ := keys.GenerateKeypair()

	_ = mint.Mint(l, kp.PublicKey, big.NewInt(300))
	_ = mint.Mint(l, kp.PublicKey, big.NewInt(200))

	bal, _ := l.BalanceOf(gas.FeeAccount(kp.PublicKey))
	if bal.Cmp(big.NewInt(500)) != 0 {
		t.Fatalf("accumulated: got %v want 500", bal)
	}
}
