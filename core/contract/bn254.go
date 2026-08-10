package contract

import (
	"bytes"
	"fmt"
	"math/big"

	"golang.org/x/crypto/bn256"
)

const (
	Bn254PairingInputBytes  = 192
	Bn254PairingBaseFuel    = 100_000
	Bn254PairingPerPairFuel = 12_000_000
)

var bn254GTOneBytes = func() []byte {
	g1 := new(bn256.G1).ScalarBaseMult(big.NewInt(1))
	g2 := new(bn256.G2).ScalarBaseMult(big.NewInt(1))
	return new(bn256.GT).ScalarMult(bn256.Pair(g1, g2), bn256.Order).Marshal()
}()

func Bn254PairingFuel(inputLen int) (uint64, error) {
	if inputLen < 0 || inputLen%Bn254PairingInputBytes != 0 {
		return 0, fmt.Errorf("bn254 pairing input length must be a multiple of %d bytes", Bn254PairingInputBytes)
	}
	pairs := uint64(inputLen / Bn254PairingInputBytes)
	if pairs > (^uint64(0)-Bn254PairingBaseFuel)/Bn254PairingPerPairFuel {
		return 0, fmt.Errorf("bn254 pairing input too large")
	}
	return Bn254PairingBaseFuel + pairs*Bn254PairingPerPairFuel, nil
}

func Bn254PairingCheck(input []byte) (bool, error) {
	if len(input)%Bn254PairingInputBytes != 0 {
		return false, fmt.Errorf("bn254 pairing input length must be a multiple of %d bytes", Bn254PairingInputBytes)
	}
	if len(input) == 0 {
		return true, nil
	}

	var acc *bn256.GT
	for len(input) > 0 {
		g1, ok := new(bn256.G1).Unmarshal(input[:64])
		if !ok {
			return false, fmt.Errorf("invalid bn254 G1 point")
		}
		g2, ok := new(bn256.G2).Unmarshal(input[64:Bn254PairingInputBytes])
		if !ok {
			return false, fmt.Errorf("invalid bn254 G2 point")
		}
		gt := bn256.Pair(g1, g2)
		if acc == nil {
			acc = gt
		} else {
			acc.Add(acc, gt)
		}
		input = input[Bn254PairingInputBytes:]
	}
	return bytes.Equal(acc.Marshal(), bn254GTOneBytes), nil
}
