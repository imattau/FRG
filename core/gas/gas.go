package gas

import (
	"crypto/sha256"
	"fmt"
	"math/big"

	"github.com/imattau/frg/core/ledger"
)

const (
	TargetTxCount            = uint64(32768)
	MaxAdjustmentNumerator   = uint64(1)
	MaxAdjustmentDenominator = uint64(8)
	ValidatorSharePct        = uint64(70)
	StakerSharePct           = uint64(30)
)

var MinBaseFee = big.NewInt(1)

const domainGasFee = "FRG_GAS_FEE_V1\x00"

// FeeAccount derives the per-validator fee accumulation account in core/ledger.
func FeeAccount(validator [32]byte) [32]byte {
	h := sha256.New()
	h.Write([]byte(domainGasFee))
	h.Write(validator[:])
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

// BaseFee computes the next block's base fee. Never falls below MinBaseFee.
func BaseFee(prevBaseFee *big.Int, txCount uint64) *big.Int {
	if prevBaseFee == nil {
		return new(big.Int).Set(MinBaseFee)
	}

	next := new(big.Int).Set(prevBaseFee)
	target := new(big.Int).SetUint64(TargetTxCount)
	denom := new(big.Int).SetUint64(MaxAdjustmentDenominator)

	if txCount > TargetTxCount {
		diff := new(big.Int).SetUint64(txCount - TargetTxCount)
		delta := new(big.Int).Mul(prevBaseFee, diff)
		delta.Div(delta, target)
		delta.Div(delta, denom)
		next.Add(next, delta)
	} else if txCount < TargetTxCount {
		diff := new(big.Int).SetUint64(TargetTxCount - txCount)
		delta := new(big.Int).Mul(prevBaseFee, diff)
		delta.Div(delta, target)
		delta.Div(delta, denom)
		next.Sub(next, delta)
	}

	if next.Cmp(MinBaseFee) < 0 {
		next.Set(MinBaseFee)
	}
	return next
}

// Accrue splits totalFees across validators, crediting each validator's fee account.
// 70% split equally; 30% split proportional to bondedAmounts.
// validatorSet and bondedAmounts must be the same length and order.
func Accrue(l *ledger.Ledger, totalFees *big.Int, validatorSet [][32]byte, bondedAmounts []*big.Int) error {
	if l == nil {
		return fmt.Errorf("ledger is nil")
	}
	if totalFees == nil {
		return fmt.Errorf("total fees is nil")
	}
	if totalFees.Sign() < 0 {
		return fmt.Errorf("total fees must be non-negative")
	}
	if len(validatorSet) != len(bondedAmounts) {
		return fmt.Errorf("validator set and bonded amounts length mismatch")
	}
	if len(validatorSet) == 0 || totalFees.Sign() == 0 {
		return nil
	}

	validatorPool := new(big.Int).Mul(totalFees, new(big.Int).SetUint64(ValidatorSharePct))
	validatorPool.Div(validatorPool, big.NewInt(100))
	stakerPool := new(big.Int).Sub(new(big.Int).Set(totalFees), validatorPool)

	equalShare := new(big.Int).Div(validatorPool, big.NewInt(int64(len(validatorSet))))
	remainder := new(big.Int).Sub(validatorPool, new(big.Int).Mul(equalShare, big.NewInt(int64(len(validatorSet)))))
	remainderCount := int(remainder.Int64())

	totalBonded := new(big.Int)
	for i, bonded := range bondedAmounts {
		if bonded == nil {
			return fmt.Errorf("bonded amount at index %d is nil", i)
		}
		totalBonded.Add(totalBonded, bonded)
	}

	for i, validator := range validatorSet {
		share := new(big.Int).Set(equalShare)
		if i < remainderCount {
			share.Add(share, big.NewInt(1))
		}

		if totalBonded.Sign() > 0 {
			stakerShare := new(big.Int).Mul(stakerPool, bondedAmounts[i])
			stakerShare.Div(stakerShare, totalBonded)
			share.Add(share, stakerShare)
		}

		feeAcc := FeeAccount(validator)
		current, err := l.BalanceOf(feeAcc)
		if err != nil {
			return err
		}
		current.Add(current, share)
		if err := l.Seed(feeAcc, current); err != nil {
			return err
		}
	}

	return nil
}

// Claim moves validator's full fee account balance to their main ledger account.
// No-op if fee account balance is zero.
func Claim(l *ledger.Ledger, validator [32]byte) error {
	if l == nil {
		return fmt.Errorf("ledger is nil")
	}
	feeAcc := FeeAccount(validator)
	bal, err := l.BalanceOf(feeAcc)
	if err != nil {
		return err
	}
	if bal.Sign() == 0 {
		return nil
	}
	return l.Move(feeAcc, validator, bal)
}

// Claimable returns the pending fee account balance for validator.
func Claimable(l *ledger.Ledger, validator [32]byte) (*big.Int, error) {
	if l == nil {
		return nil, fmt.Errorf("ledger is nil")
	}
	return l.BalanceOf(FeeAccount(validator))
}
