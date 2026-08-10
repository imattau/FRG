package denom

import (
	"fmt"
	"math/big"
	"strings"
)

const Decimals = 18

var QuantaPerFRG = new(big.Int).Exp(big.NewInt(10), big.NewInt(Decimals), nil)

// ParseFRG converts a user-facing FRG decimal amount into integer quanta.
func ParseFRG(raw string) (*big.Int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("amount is required")
	}
	if strings.HasPrefix(raw, "-") {
		return nil, fmt.Errorf("amount must be non-negative")
	}
	if strings.HasPrefix(raw, "+") {
		raw = raw[1:]
	}
	parts := strings.Split(raw, ".")
	if len(parts) > 2 {
		return nil, fmt.Errorf("amount must be a decimal FRG value")
	}
	whole := parts[0]
	frac := ""
	if len(parts) == 2 {
		frac = parts[1]
	}
	if whole == "" && frac == "" {
		return nil, fmt.Errorf("amount must be a decimal FRG value")
	}
	if whole == "" {
		whole = "0"
	}
	if !digitsOnly(whole) || (frac != "" && !digitsOnly(frac)) {
		return nil, fmt.Errorf("amount must be a decimal FRG value")
	}
	if len(frac) > Decimals {
		return nil, fmt.Errorf("amount has more than %d decimal places", Decimals)
	}
	wholeInt, ok := new(big.Int).SetString(whole, 10)
	if !ok {
		return nil, fmt.Errorf("amount must be a decimal FRG value")
	}
	amount := new(big.Int).Mul(wholeInt, QuantaPerFRG)
	if frac != "" {
		padded := frac + strings.Repeat("0", Decimals-len(frac))
		fracInt, ok := new(big.Int).SetString(padded, 10)
		if !ok {
			return nil, fmt.Errorf("amount must be a decimal FRG value")
		}
		amount.Add(amount, fracInt)
	}
	return amount, nil
}

func ParsePositiveFRG(raw string) (*big.Int, error) {
	amount, err := ParseFRG(raw)
	if err != nil {
		return nil, err
	}
	if amount.Sign() <= 0 {
		return nil, fmt.Errorf("amount must be positive")
	}
	return amount, nil
}

func ParseQuanta(raw string) (*big.Int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("amount is required")
	}
	amount, ok := new(big.Int).SetString(raw, 10)
	if !ok || amount.Sign() < 0 {
		return nil, fmt.Errorf("amount must be a non-negative base-10 quanta integer")
	}
	return amount, nil
}

func ParsePositiveQuanta(raw string) (*big.Int, error) {
	amount, err := ParseQuanta(raw)
	if err != nil {
		return nil, err
	}
	if amount.Sign() <= 0 {
		return nil, fmt.Errorf("amount must be positive")
	}
	return amount, nil
}

func FormatFRG(quanta *big.Int) string {
	if quanta == nil {
		return "0"
	}
	sign := ""
	value := new(big.Int).Set(quanta)
	if value.Sign() < 0 {
		sign = "-"
		value.Abs(value)
	}
	whole, frac := new(big.Int), new(big.Int)
	whole.QuoRem(value, QuantaPerFRG, frac)
	if frac.Sign() == 0 {
		return sign + whole.String()
	}
	fracStr := frac.String()
	if len(fracStr) < Decimals {
		fracStr = strings.Repeat("0", Decimals-len(fracStr)) + fracStr
	}
	fracStr = strings.TrimRight(fracStr, "0")
	return sign + whole.String() + "." + fracStr
}

func digitsOnly(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
