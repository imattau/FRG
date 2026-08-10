package contract

import (
	"fmt"

	"github.com/consensys/gnark-crypto/ecc/bn254"
)

const (
	Bn254PairingInputBytes  = 192
	Bn254PairingBaseFuel    = 100_000
	Bn254PairingPerPairFuel = 12_000_000
)

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

// Bn254PairingCheck checks whether the product of e(P_i, Q_i) over each
// 192-byte (G1 || G2) pair in input equals 1 in the target group -- the
// Groth16/EIP-197 multi-pairing verification primitive contracts use to
// verify a proof (see e.g. contracts/frg/settlement in the MEX repo).
//
// Uses github.com/consensys/gnark-crypto's real BN254/alt_bn128
// implementation. This function previously used golang.org/x/crypto/bn256,
// which despite the name is a DIFFERENT, older, differently-parameterized
// curve (prime modulus 65000549695646603732796438742359905742825358107623
// 003571877145026864184071783, not BN254/alt_bn128's
// 21888242871839275222246405745257275088696311157297823662689037894645226
// 208583) -- a well-known Go-ecosystem footgun. A proof from any standard
// BN254 toolchain (arkworks, snarkjs, gnark itself, the Ethereum alt_bn128
// precompile) could never verify against that package, regardless of byte
// encoding: the underlying curve arithmetic itself didn't match. Confirmed
// live against a real MEX-produced proof, which a golang.org/x/crypto/
// bn256-based build rejected outright (even G1.Unmarshal's on-curve check
// failed) while this implementation accepts it correctly.
//
// Point encoding is unchanged from before this fix and matches the EIP-197
// alt_bn128 precompile convention: G1 is 64 bytes, x then y, big-endian.
// G2 is 128 bytes, x.c1, x.c0, y.c1, y.c0 (imaginary component first),
// big-endian. Points are built field-element-by-field via
// SetBytesCanonical rather than gnark-crypto's own G1Affine.SetBytes/
// G2Affine.SetBytes, which expect gnark's RawBytes()/Bytes() wire format
// (a leading metadata/compression-flag byte, not present here) --
// gnark-crypto's own G2Affine.RawBytes() lays out X.A1, X.A0, Y.A1, Y.A0,
// confirming A1 is the imaginary component (matches EIP-197's "c1 first"
// and this function's chunk layout below) even though the raw wire framing
// differs.
func Bn254PairingCheck(input []byte) (bool, error) {
	if len(input)%Bn254PairingInputBytes != 0 {
		return false, fmt.Errorf("bn254 pairing input length must be a multiple of %d bytes", Bn254PairingInputBytes)
	}
	if len(input) == 0 {
		return true, nil
	}

	pairs := len(input) / Bn254PairingInputBytes
	g1s := make([]bn254.G1Affine, pairs)
	g2s := make([]bn254.G2Affine, pairs)

	for i := 0; i < pairs; i++ {
		chunk := input[i*Bn254PairingInputBytes : (i+1)*Bn254PairingInputBytes]

		if err := g1s[i].X.SetBytesCanonical(chunk[0:32]); err != nil {
			return false, fmt.Errorf("invalid bn254 G1 point: %w", err)
		}
		if err := g1s[i].Y.SetBytesCanonical(chunk[32:64]); err != nil {
			return false, fmt.Errorf("invalid bn254 G1 point: %w", err)
		}
		// (0, 0) is the EIP-197 encoding of the point at infinity, not a
		// point actually on the curve (y^2 = x^3 + 3 has no solution at
		// x=0) -- accept it directly rather than running curve/subgroup
		// checks that would legitimately reject it.
		if !(g1s[i].X.IsZero() && g1s[i].Y.IsZero()) {
			if !g1s[i].IsOnCurve() || !g1s[i].IsInSubGroup() {
				return false, fmt.Errorf("invalid bn254 G1 point")
			}
		}

		if err := g2s[i].X.A1.SetBytesCanonical(chunk[64:96]); err != nil {
			return false, fmt.Errorf("invalid bn254 G2 point: %w", err)
		}
		if err := g2s[i].X.A0.SetBytesCanonical(chunk[96:128]); err != nil {
			return false, fmt.Errorf("invalid bn254 G2 point: %w", err)
		}
		if err := g2s[i].Y.A1.SetBytesCanonical(chunk[128:160]); err != nil {
			return false, fmt.Errorf("invalid bn254 G2 point: %w", err)
		}
		if err := g2s[i].Y.A0.SetBytesCanonical(chunk[160:192]); err != nil {
			return false, fmt.Errorf("invalid bn254 G2 point: %w", err)
		}
		if !(g2s[i].X.IsZero() && g2s[i].Y.IsZero()) {
			if !g2s[i].IsOnCurve() || !g2s[i].IsInSubGroup() {
				return false, fmt.Errorf("invalid bn254 G2 point")
			}
		}
	}

	return bn254.PairingCheck(g1s, g2s)
}
