package hash

import (
	"crypto/sha256"
	"math/big"
)

const (
	DomainTx         = "\x54\x58\x5F\x56\x31\x00"
	DomainTxBatch    = "\x54\x58\x5F\x42\x41\x54\x43\x48\x5F\x56\x31\x00"
	DomainRGNode     = "\x52\x47\x5F\x4E\x4F\x44\x45\x5F\x56\x31\x00"
	DomainNullPad    = "\x4E\x55\x4C\x4C\x5F\x50\x41\x44\x5F\x56\x31\x00"
	DomainEmptyBlock = "\x45\x4D\x50\x54\x59\x5F\x42\x4C\x4F\x43\x4B\x5F\x56\x31\x00"
)

var UINT256_MAX = new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))

// Scale is the fixed-point denominator: 10^18.
var Scale = new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)

var validScales = map[uint32]struct{}{
	1:     {},
	4:     {},
	16:    {},
	64:    {},
	256:   {},
	1024:  {},
	4096:  {},
	16384: {},
	65536: {},
}

func Hash(data []byte) [32]byte {
	return sha256.Sum256(data)
}

func ValidScale(lambda uint32) bool {
	_, ok := validScales[lambda]
	return ok
}
