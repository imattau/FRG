package tx

import (
	"encoding/binary"
	"math/big"
	"unicode/utf8"

	rgerrors "github.com/imattau/frg/core/errors"
	"github.com/imattau/frg/core/hash"
	"github.com/imattau/frg/core/keys"
	"golang.org/x/text/unicode/norm"
)

const maxTxBytes = 70000

type Tx struct {
	Sender         string
	Receiver       string
	Value          *big.Int
	Nonce          uint64
	SenderPubKey   [32]byte
	ReceiverPubKey [32]byte
	SenderSig      [64]byte
	ReceiverSig    [64]byte
}

func (t *Tx) serializeUnsigned() ([]byte, error) {
	if !utf8.ValidString(t.Sender) || !utf8.ValidString(t.Receiver) {
		return nil, rgerrors.New(rgerrors.ErrCanonicalEncodingDistortion, "sender or receiver is not valid UTF-8")
	}

	sender := norm.NFC.String(t.Sender)
	receiver := norm.NFC.String(t.Receiver)

	if t.Value == nil || t.Value.Sign() < 0 || t.Value.Cmp(hash.UINT256_MAX) > 0 {
		return nil, rgerrors.New(rgerrors.ErrArithmeticOverflow, "value out of uint256 range")
	}

	if len(sender) > 0xFFFF || len(receiver) > 0xFFFF {
		return nil, rgerrors.New(rgerrors.ErrCanonicalEncodingDistortion, "sender or receiver exceeds uint16 length")
	}

	size := 6 + 2 + len(sender) + 2 + len(receiver) + 32 + 8
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

// UnsignedHash returns H(Tx_Bytes_unsigned), the message both parties sign.
func (t *Tx) UnsignedHash() ([32]byte, error) {
	serialized, err := t.serializeUnsigned()
	if err != nil {
		return [32]byte{}, err
	}
	return hash.Hash(serialized), nil
}

func (t *Tx) Serialize() ([]byte, error) {
	unsigned, err := t.serializeUnsigned()
	if err != nil {
		return nil, err
	}

	if len(unsigned)+192 > maxTxBytes {
		return nil, rgerrors.Newf(rgerrors.ErrDosSizeExceeded, "tx payload %d bytes exceeds %d", len(unsigned)+192, maxTxBytes)
	}

	full := make([]byte, len(unsigned)+192)
	pos := copy(full, unsigned)
	pos += copy(full[pos:], t.SenderPubKey[:])
	pos += copy(full[pos:], t.ReceiverPubKey[:])
	pos += copy(full[pos:], t.SenderSig[:])
	copy(full[pos:], t.ReceiverSig[:])

	return full, nil
}

func (t *Tx) ID() ([32]byte, error) {
	serialized, err := t.Serialize()
	if err != nil {
		return [32]byte{}, err
	}
	return hash.Hash(serialized), nil
}

// VerifySigs verifies both sender and receiver Ed25519 signatures against H(Tx_Bytes_unsigned).
func (t *Tx) VerifySigs() error {
	msg, err := t.UnsignedHash()
	if err != nil {
		return err
	}
	if !keys.Verify(t.SenderPubKey, msg[:], t.SenderSig) {
		return rgerrors.New(rgerrors.ErrInvalidSignature, "sender signature verification failed")
	}
	if !keys.Verify(t.ReceiverPubKey, msg[:], t.ReceiverSig) {
		return rgerrors.New(rgerrors.ErrInvalidSignature, "receiver signature verification failed")
	}
	return nil
}
