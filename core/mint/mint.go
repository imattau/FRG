package mint

import (
	"math/big"

	"github.com/imattau/frg/core/gas"
	"github.com/imattau/frg/core/ledger"
)

const (
	TargetStakingRatioPct = int64(50)
	MaxAnnualRatePct      = int64(10)
	BlocksPerYear         = int64(5_256_000)
)

// MintPerBlock computes the per-block reward given current supply and staking.
// Returns 0 if totalStaked >= 50% of totalSupply.
func MintPerBlock(totalSupply, totalStaked *big.Int) *big.Int {
	if totalSupply == nil || totalStaked == nil || totalSupply.Sign() <= 0 {
		return big.NewInt(0)
	}

	targetStaked := new(big.Int).Mul(totalSupply, big.NewInt(TargetStakingRatioPct))
	targetStaked.Div(targetStaked, big.NewInt(100))
	if targetStaked.Sign() == 0 || totalStaked.Cmp(targetStaked) >= 0 {
		return big.NewInt(0)
	}

	deficit := new(big.Int).Sub(targetStaked, totalStaked)

	maxPerBlock := new(big.Int).Mul(totalSupply, big.NewInt(MaxAnnualRatePct))
	maxPerBlock.Div(maxPerBlock, big.NewInt(100))
	maxPerBlock.Div(maxPerBlock, big.NewInt(BlocksPerYear))

	reward := new(big.Int).Mul(maxPerBlock, deficit)
	reward.Div(reward, targetStaked)
	return reward
}

// SplitReward divides blockReward equally across validatorCount validators.
// Remainder (floor division) goes to the first validator.
func SplitReward(blockReward *big.Int, validatorCount int) []*big.Int {
	if validatorCount <= 0 {
		return nil
	}
	if blockReward == nil || blockReward.Sign() <= 0 {
		out := make([]*big.Int, validatorCount)
		for i := range out {
			out[i] = big.NewInt(0)
		}
		return out
	}

	n := big.NewInt(int64(validatorCount))
	share := new(big.Int).Div(blockReward, n)
	remainder := new(big.Int).Sub(blockReward, new(big.Int).Mul(share, n))

	shares := make([]*big.Int, validatorCount)
	for i := range shares {
		shares[i] = new(big.Int).Set(share)
	}
	shares[0].Add(shares[0], remainder)
	return shares
}

// Mint credits amount quanta to validator's fee account via ledger.Seed.
// Creates new quanta and increases total supply.
func Mint(l *ledger.Ledger, validator [32]byte, amount *big.Int) error {
	if l == nil {
		return nil
	}
	if amount == nil || amount.Sign() <= 0 {
		return nil
	}

	feeAcc := gas.FeeAccount(validator)
	cur, err := l.BalanceOf(feeAcc)
	if err != nil {
		return err
	}
	return l.Seed(feeAcc, new(big.Int).Add(cur, amount))
}
