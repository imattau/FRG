package e2e_test

import (
	"math/big"
	"testing"

	rgerrors "github.com/imattau/frg/core/errors"
	"github.com/imattau/frg/core/gas"
	"github.com/imattau/frg/core/mint"
	"github.com/imattau/frg/core/tx"
)

func TestLedgerTransferRoundTrip(t *testing.T) {
	h := newHarness(t)
	sender := makeKeypair(t)
	receiver := makeKeypair(t)
	seedAccount(t, h.Ledger, sender.PublicKey, 1000)

	tr := makeTx(t, sender, receiver, 400, 1)
	if err := h.Ledger.Transfer(tr); err != nil {
		t.Fatalf("Transfer: %v", err)
	}
	sbal, _ := h.Ledger.BalanceOf(sender.PublicKey)
	rbal, _ := h.Ledger.BalanceOf(receiver.PublicKey)
	if sbal.Cmp(big.NewInt(600)) != 0 {
		t.Fatalf("sender: got %v want 600", sbal)
	}
	if rbal.Cmp(big.NewInt(400)) != 0 {
		t.Fatalf("receiver: got %v want 400", rbal)
	}
}

func TestLedgerOverflowRejected(t *testing.T) {
	h := newHarness(t)
	sender := makeKeypair(t)
	// uint256 max + 1
	maxPlusOne := new(big.Int).Lsh(big.NewInt(1), 256)
	if err := h.Ledger.Seed(sender.PublicKey, maxPlusOne); err == nil {
		t.Fatal("expected overflow error, got nil")
	}
}

func TestStakingBondUnbondCycle(t *testing.T) {
	h := newHarness(t)
	kp := makeKeypair(t)
	bondValidator(t, h, kp, 2000, 1)

	if err := h.Staking.Unbond(kp.PublicKey, 100); err != nil {
		t.Fatalf("Unbond: %v", err)
	}
	// before lockup — no-op (funds not returned)
	if err := h.Staking.Finalize(kp.PublicKey, 1099); err != nil {
		t.Fatalf("Finalize before lockup: %v", err)
	}
	bal, _ := h.Ledger.BalanceOf(kp.PublicKey)
	// bond was 2000, seeded 3000 (amount+1000), remaining 1000
	if bal.Cmp(big.NewInt(1000)) != 0 {
		t.Fatalf("before lockup: got %v want 1000", bal)
	}
	// after lockup
	if err := h.Staking.Finalize(kp.PublicKey, 1101); err != nil {
		t.Fatalf("Finalize after lockup: %v", err)
	}
	bal, _ = h.Ledger.BalanceOf(kp.PublicKey)
	if bal.Cmp(big.NewInt(3000)) != 0 {
		t.Fatalf("after finalize: got %v want 3000", bal)
	}
}

func TestStakingBondMinimumEnforced(t *testing.T) {
	h := newHarness(t)
	kp := makeKeypair(t)
	seedAccount(t, h.Ledger, kp.PublicKey, 5000)
	err := h.Staking.Bond(kp.PublicKey, big.NewInt(999), 1)
	assertCode(t, err, rgerrors.ErrBondBelowMinimum)
}

func TestGasAccrueClaimRoundTrip(t *testing.T) {
	h := newHarness(t)
	kp := makeKeypair(t)
	bondValidator(t, h, kp, 1000, 1)

	validators := [][32]byte{kp.PublicKey}
	bonds := []*big.Int{big.NewInt(1000)}
	if err := gas.Accrue(h.Ledger, big.NewInt(100), validators, bonds); err != nil {
		t.Fatalf("Accrue: %v", err)
	}
	claimable, _ := gas.Claimable(h.Ledger, kp.PublicKey)
	if claimable.Sign() == 0 {
		t.Fatal("claimable should be > 0")
	}
	balBefore, _ := h.Ledger.BalanceOf(kp.PublicKey)
	if err := gas.Claim(h.Ledger, kp.PublicKey); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	balAfter, _ := h.Ledger.BalanceOf(kp.PublicKey)
	if balAfter.Cmp(balBefore) <= 0 {
		t.Fatal("balance should increase after Claim")
	}
	claimable, _ = gas.Claimable(h.Ledger, kp.PublicKey)
	if claimable.Sign() != 0 {
		t.Fatalf("claimable after claim: got %v want 0", claimable)
	}
}

func TestGasProportionalDistribution(t *testing.T) {
	h := newHarness(t)
	kpA := makeKeypair(t)
	kpB := makeKeypair(t)
	validators := [][32]byte{kpA.PublicKey, kpB.PublicKey}
	bonds := []*big.Int{big.NewInt(3000), big.NewInt(1000)}

	// Seed for fees (Accrue doesn't check balance, but we need to bond them first)
	bondValidator(t, h, kpA, 3000, 1)
	bondValidator(t, h, kpB, 1000, 1)

	if err := gas.Accrue(h.Ledger, big.NewInt(800), validators, bonds); err != nil {
		t.Fatalf("Accrue: %v", err)
	}
	balA, _ := gas.Claimable(h.Ledger, kpA.PublicKey)
	balB, _ := gas.Claimable(h.Ledger, kpB.PublicKey)
	// A should have more than B (3:1 bond ratio)
	if balA.Cmp(balB) <= 0 {
		t.Fatalf("A (%v) should be > B (%v)", balA, balB)
	}
}

func TestBaseFeeAdjustment(t *testing.T) {
	base := big.NewInt(1000)
	// empty block → decrease
	down := gas.BaseFee(base, 0)
	if down.Cmp(base) >= 0 {
		t.Fatalf("empty block: fee should decrease, got %v", down)
	}
	// full block → increase
	up := gas.BaseFee(base, 65536)
	if up.Cmp(base) <= 0 {
		t.Fatalf("full block: fee should increase, got %v", up)
	}
	// at target → unchanged
	same := gas.BaseFee(base, 32768)
	if same.Cmp(base) != 0 {
		t.Fatalf("at target: fee should be unchanged, got %v", same)
	}
}

func TestMintEmissionAtZeroStaking(t *testing.T) {
	supply := new(big.Int).Mul(big.NewInt(400_000_000), new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil))
	staked := big.NewInt(0)
	result := mint.MintPerBlock(supply, staked)
	
	// MaxAnnualRatePct = 10%, BlocksPerYear = 5,256_000
	// TargetStakingRatioPct = 50%
	// deficit = targetStaked - totalStaked = 50% - 0% = 50%
	// reward = (maxPerBlock * deficit) / targetStaked = maxPerBlock * 0.5 / 0.5 = maxPerBlock
	
	expected := new(big.Int).Mul(supply, big.NewInt(mint.MaxAnnualRatePct))
	expected.Div(expected, big.NewInt(100))
	expected.Div(expected, big.NewInt(mint.BlocksPerYear))
	
	if result.Cmp(expected) != 0 {
		t.Fatalf("zero staking emission: got %v want %v", result, expected)
	}
}

func TestMintEmissionAtTarget(t *testing.T) {
	supply := new(big.Int).Mul(big.NewInt(400_000_000), new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil))
	staked := new(big.Int).Div(supply, big.NewInt(2)) // 50%
	result := mint.MintPerBlock(supply, staked)
	if result.Sign() != 0 {
		t.Fatalf("at target: expected 0 emission, got %v", result)
	}
}

func TestFullBlockRewardCycle(t *testing.T) {
	h := newHarness(t)
	kp := makeKeypair(t)
	bondValidator(t, h, kp, 1000, 1)

	sender := makeKeypair(t)
	receiver := makeKeypair(t)
	seedAccount(t, h.Ledger, sender.PublicKey, 10000)
	txs := []*tx.Tx{makeTx(t, sender, receiver, 100, 1)}
	buildBlock(t, 1, txs)

	baseFee := big.NewInt(10)
	totalFees := new(big.Int).Mul(baseFee, big.NewInt(int64(len(txs))))

	validators, bonds, _ := h.Staking.BondedAmounts()
	if err := gas.Accrue(h.Ledger, totalFees, validators, bonds); err != nil {
		t.Fatalf("Accrue: %v", err)
	}

	supply := new(big.Int).Mul(big.NewInt(400_000_000), new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil))
	totalStaked := big.NewInt(0)
	for _, b := range bonds {
		totalStaked.Add(totalStaked, b)
	}
	blockReward := mint.MintPerBlock(supply, totalStaked)
	shares := mint.SplitReward(blockReward, len(validators))
	for i, v := range validators {
		if err := mint.Mint(h.Ledger, v, shares[i]); err != nil {
			t.Fatalf("Mint: %v", err)
		}
	}

	claimable, _ := gas.Claimable(h.Ledger, kp.PublicKey)
	if claimable.Sign() == 0 {
		t.Fatal("validator should have claimable rewards")
	}
	if err := gas.Claim(h.Ledger, kp.PublicKey); err != nil {
		t.Fatalf("Claim: %v", err)
	}
}

func TestRewardsAccumulateAcrossBlocks(t *testing.T) {
	h := newHarness(t)
	kp := makeKeypair(t)
	bondValidator(t, h, kp, 1000, 1)
	validators := [][32]byte{kp.PublicKey}
	bonds := []*big.Int{big.NewInt(1000)}

	for i := 0; i < 3; i++ {
		_ = gas.Accrue(h.Ledger, big.NewInt(100), validators, bonds)
	}
	claimable, _ := gas.Claimable(h.Ledger, kp.PublicKey)
	if claimable.Cmp(big.NewInt(300)) != 0 {
		t.Fatalf("accumulated: got %v want 300", claimable)
	}
}
