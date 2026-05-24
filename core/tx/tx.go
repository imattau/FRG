package tx

import (
	"encoding/binary"
	"math/big"
	"unicode/utf8"

	rgerrors "github.com/imattau/frg/core/errors"
	"github.com/imattau/frg/core/hash"
	"golang.org/x/text/unicode/norm"
)

const maxTxBytes = 70000

type Tx struct {
	Sender   string
	Receiver string
	Value    *big.Int
	Nonce    uint64
}

func (t *Tx) Serialize() ([]byte, error) {
	if !utf8.ValidString(t.Sender) || !utf8.ValidString(t.Receiver) {
		return nil, rgerrors.New(rgerrors.ErrCanonicalEncodingDistortion, "sender or receiver is not valid UTF-8")
	}

	sender := norm.NFC.String(t.Sender)
	receiver := norm.NFC.String(t.Receiver)

	if t.Value == nil || t.Value.Sign() < 0 || t.Value.Cmp(hash.UINT256_MAX) > 0 {
		return nil, rgerrors.New(rgerrors.ErrArithmeticOverflow, "value out of uint256 range")
	}

	size := 6 + 2 + len(sender) + 2 + len(receiver) + 32 + 8
	if size > maxTxBytes {
		return nil, rgerrors.Newf(rgerrors.ErrDosSizeExceeded, "tx payload %d bytes exceeds %d", size, maxTxBytes)
	}

	if len(sender) > 0xFFFF || len(receiver) > 0xFFFF {
		return nil, rgerrors.New(rgerrors.ErrCanonicalEncodingDistortion, "sender or receiver exceeds uint16 length")
	}

	buf := make([]byte, size)
	pos := 0

	pos += copy(buf[pos:], hash.DomainTx)

	binary.BigEndian.PutUint16(buf[pos:], uint16(len(sender)))
	pos += 2
	pos += copy(buf[pos:], sender)

	binary.BigEndian.PutUint16(buf[pos:], uint16(len(receiver)))
	pos += 2
	pos += copy(buf[pos:], receiver)

	valueBytes := t.Value.Bytes()
	if len(valueBytes) > 32 {
		return nil, rgerrors.New(rgerrors.ErrArithmeticOverflow, "value exceeds uint256")
	}
	pos += 32 - len(valueBytes)
	copy(buf[pos:], valueBytes)
	pos += len(valueBytes)

	binary.BigEndian.PutUint64(buf[pos:], t.Nonce)

	return buf, nil
}

func (t *Tx) ID() ([32]byte, error) {
	serialized, err := t.Serialize()
	if err != nil {
		return [32]byte{}, err
	}
	return hash.Hash(serialized), nil
}
