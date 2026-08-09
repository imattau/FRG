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

// MaxSerializedBytes is the maximum accepted serialized transaction size.
const MaxSerializedBytes = maxTxBytes

const (
	maxBatchTxs   = 1024
	maxBatchBytes = 8 << 20
)

// MaxBatchBytes is the maximum accepted serialized transaction batch size.
const MaxBatchBytes = maxBatchBytes

type TxType uint8

const (
	TxTypeTransfer       TxType = 1
	TxTypeMissEvidence   TxType = 2
	TxTypeContractDeploy TxType = 3
	TxTypeContractCall   TxType = 4
)

const maxWasmBytes = 1 << 20 // 1 MB

type Tx struct {
	Type           TxType
	Sender         string
	Receiver       string
	Value          *big.Int
	Nonce          uint64
	SenderPubKey   [32]byte
	ReceiverPubKey [32]byte
	SenderSig      [64]byte
	ReceiverSig    [64]byte
	// MISS_EVIDENCE only; zero for TRANSFER
	MissedHeight   uint64
	MissedProposer [32]byte
	SkipIndex      uint32
	// CONTRACT_DEPLOY: WASM bytecode
	WasmBytes []byte
	// CONTRACT_DEPLOY: constructor arguments
	InitArgs []byte
	// CONTRACT_CALL: function call data
	CallData []byte
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

	// domain(6) + Type(1) + Len_Sender(2) + Sender + Len_Rcvr(2) + Receiver
	// + Value(32) + Nonce(8) + MissedHeight(8) + MissedProposer(32) + SkipIndex(4)
	size := 6 + 1 + 2 + len(sender) + 2 + len(receiver) + 32 + 8 + 8 + 32 + 4
	buf := make([]byte, size)
	pos := 0

	pos += copy(buf[pos:], hash.DomainTx)

	buf[pos] = byte(t.Type)
	pos++

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
	pos += 8

	binary.BigEndian.PutUint64(buf[pos:], t.MissedHeight)
	pos += 8

	copy(buf[pos:], t.MissedProposer[:])
	pos += 32

	binary.BigEndian.PutUint32(buf[pos:], t.SkipIndex)

	switch t.Type {
	case TxTypeContractDeploy:
		if len(t.WasmBytes) > maxWasmBytes {
			return nil, rgerrors.Newf(rgerrors.ErrContractBytecodeTooLarge, "wasm %d bytes exceeds %d", len(t.WasmBytes), maxWasmBytes)
		}
		ext := make([]byte, 4+len(t.WasmBytes))
		binary.BigEndian.PutUint32(ext, uint32(len(t.WasmBytes)))
		copy(ext[4:], t.WasmBytes)
		buf = append(buf, ext...)
	case TxTypeContractCall:
		if len(t.CallData) > 0xFFFF {
			return nil, rgerrors.New(rgerrors.ErrDosSizeExceeded, "calldata exceeds uint16")
		}
		ext := make([]byte, 2+len(t.CallData))
		binary.BigEndian.PutUint16(ext, uint16(len(t.CallData)))
		copy(ext[2:], t.CallData)
		buf = append(buf, ext...)
	}

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
	serialized, err := t.serializeUnsigned()
	if err != nil {
		return [32]byte{}, err
	}
	return hash.Hash(serialized), nil
}

// Deserialize parses a fully serialised tx (output of Serialize).
// Layout: [unsigned bytes][SenderPubKey 32][ReceiverPubKey 32][SenderSig 64][ReceiverSig 64]
// Unsigned layout: [TX_V1\x00 6][Type 1][Len_Sender 2][Sender][Len_Rcvr 2][Receiver][Value 32][Nonce 8][MissedHeight 8][MissedProposer 32][SkipIndex 4]
func Deserialize(data []byte) (*Tx, error) {
	if len(data) > maxTxBytes {
		return nil, rgerrors.Newf(rgerrors.ErrDosSizeExceeded, "tx payload %d bytes exceeds %d", len(data), maxTxBytes)
	}
	if len(data) < 6+1+2+2+32+8+8+32+4+192 {
		return nil, rgerrors.New(rgerrors.ErrCanonicalEncodingDistortion, "tx too short")
	}

	pos := 0

	// domain prefix
	if string(data[pos:pos+6]) != string(hash.DomainTx) {
		return nil, rgerrors.New(rgerrors.ErrCanonicalEncodingDistortion, "invalid tx domain prefix")
	}
	pos += 6

	// Type
	txType := TxType(data[pos])
	pos++

	// Sender
	senderLen := int(binary.BigEndian.Uint16(data[pos:]))
	pos += 2
	if pos+senderLen > len(data) {
		return nil, rgerrors.New(rgerrors.ErrCanonicalEncodingDistortion, "sender length overflow")
	}
	sender := string(data[pos : pos+senderLen])
	pos += senderLen

	// Receiver
	receiverLen := int(binary.BigEndian.Uint16(data[pos:]))
	pos += 2
	if pos+receiverLen > len(data) {
		return nil, rgerrors.New(rgerrors.ErrCanonicalEncodingDistortion, "receiver length overflow")
	}
	receiver := string(data[pos : pos+receiverLen])
	pos += receiverLen

	// Value (32 bytes big-endian uint256)
	if pos+32 > len(data) {
		return nil, rgerrors.New(rgerrors.ErrCanonicalEncodingDistortion, "value truncated")
	}
	value := new(big.Int).SetBytes(data[pos : pos+32])
	pos += 32

	// Nonce
	if pos+8 > len(data) {
		return nil, rgerrors.New(rgerrors.ErrCanonicalEncodingDistortion, "nonce truncated")
	}
	nonce := binary.BigEndian.Uint64(data[pos:])
	pos += 8

	// MissedHeight
	if pos+8 > len(data) {
		return nil, rgerrors.New(rgerrors.ErrCanonicalEncodingDistortion, "missed height truncated")
	}
	missedHeight := binary.BigEndian.Uint64(data[pos:])
	pos += 8

	// MissedProposer
	if pos+32 > len(data) {
		return nil, rgerrors.New(rgerrors.ErrCanonicalEncodingDistortion, "missed proposer truncated")
	}
	var missedProposer [32]byte
	copy(missedProposer[:], data[pos:pos+32])
	pos += 32

	// SkipIndex
	if pos+4 > len(data) {
		return nil, rgerrors.New(rgerrors.ErrCanonicalEncodingDistortion, "skip index truncated")
	}
	skipIndex := binary.BigEndian.Uint32(data[pos:])
	pos += 4

	// Contract-specific extensions
	var wasmBytes []byte
	var callData []byte
	switch txType {
	case TxTypeContractDeploy:
		if pos+4 > len(data)-192 {
			return nil, rgerrors.New(rgerrors.ErrCanonicalEncodingDistortion, "wasmlen truncated")
		}
		wasmLen := binary.BigEndian.Uint32(data[pos:])
		pos += 4
		if int(wasmLen) > maxWasmBytes {
			return nil, rgerrors.Newf(rgerrors.ErrContractBytecodeTooLarge, "wasm %d bytes exceeds %d", wasmLen, maxWasmBytes)
		}
		if pos+int(wasmLen) > len(data)-192 {
			return nil, rgerrors.New(rgerrors.ErrCanonicalEncodingDistortion, "wasm bytes truncated")
		}
		wasmBytes = make([]byte, wasmLen)
		copy(wasmBytes, data[pos:pos+int(wasmLen)])
		pos += int(wasmLen)
	case TxTypeContractCall:
		if pos+2 > len(data)-192 {
			return nil, rgerrors.New(rgerrors.ErrCanonicalEncodingDistortion, "calldata len truncated")
		}
		callDataLen := binary.BigEndian.Uint16(data[pos:])
		pos += 2
		if pos+int(callDataLen) > len(data)-192 {
			return nil, rgerrors.New(rgerrors.ErrCanonicalEncodingDistortion, "calldata truncated")
		}
		callData = make([]byte, callDataLen)
		copy(callData, data[pos:pos+int(callDataLen)])
		pos += int(callDataLen)
	}
	if pos != len(data)-192 {
		return nil, rgerrors.New(rgerrors.ErrCanonicalEncodingDistortion, "non-canonical tx trailing bytes")
	}

	// Trailing 192 bytes: SenderPubKey(32) + ReceiverPubKey(32) + SenderSig(64) + ReceiverSig(64)
	// These are always at the end of the byte slice.
	pos = len(data) - 192
	var senderPubKey, receiverPubKey [32]byte
	var senderSig, receiverSig [64]byte
	copy(senderPubKey[:], data[pos:pos+32])
	pos += 32
	copy(receiverPubKey[:], data[pos:pos+32])
	pos += 32
	copy(senderSig[:], data[pos:pos+64])
	pos += 64
	copy(receiverSig[:], data[pos:pos+64])

	parsed := &Tx{
		Type:           txType,
		Sender:         sender,
		Receiver:       receiver,
		Value:          value,
		Nonce:          nonce,
		SenderPubKey:   senderPubKey,
		ReceiverPubKey: receiverPubKey,
		SenderSig:      senderSig,
		ReceiverSig:    receiverSig,
		MissedHeight:   missedHeight,
		MissedProposer: missedProposer,
		SkipIndex:      skipIndex,
		WasmBytes:      wasmBytes,
		CallData:       callData,
	}
	canonical, err := parsed.Serialize()
	if err != nil {
		return nil, err
	}
	if string(canonical) != string(data) {
		return nil, rgerrors.New(rgerrors.ErrCanonicalEncodingDistortion, "tx is not canonical serialization")
	}
	return parsed, nil
}

// VerifySigs verifies Ed25519 signatures against H(Tx_Bytes_unsigned).
// TRANSFER: verifies both SenderSig and ReceiverSig.
// MISS_EVIDENCE: verifies only SenderSig.
// CONTRACT_DEPLOY / CONTRACT_CALL: verifies only SenderSig (single-sig).
func (t *Tx) VerifySigs() error {
	msg, err := t.UnsignedHash()
	if err != nil {
		return err
	}

	switch t.Type {
	case TxTypeTransfer:
		if !keys.Verify(t.SenderPubKey, msg[:], t.SenderSig) {
			return rgerrors.New(rgerrors.ErrInvalidSignature, "sender signature verification failed")
		}
		if !keys.Verify(t.ReceiverPubKey, msg[:], t.ReceiverSig) {
			return rgerrors.New(rgerrors.ErrInvalidSignature, "receiver signature verification failed")
		}
	case TxTypeMissEvidence, TxTypeContractDeploy, TxTypeContractCall:
		if !keys.Verify(t.SenderPubKey, msg[:], t.SenderSig) {
			return rgerrors.New(rgerrors.ErrInvalidSignature, "sender signature verification failed")
		}
	default:
		return rgerrors.Newf(rgerrors.ErrInvalidTxType, "unknown tx type %d", t.Type)
	}
	return nil
}

func (t *Tx) SignSender(kp *keys.Keypair) ([64]byte, error) {
	msg, err := t.UnsignedHash()
	if err != nil {
		return [64]byte{}, err
	}
	return kp.Sign(msg[:])
}

func (t *Tx) SignReceiver(kp *keys.Keypair) ([64]byte, error) {
	msg, err := t.UnsignedHash()
	if err != nil {
		return [64]byte{}, err
	}
	return kp.Sign(msg[:])
}

// SerializeBatch bundles multiple transactions into a single byte slice.
// Layout: [DomainTxBatch 12][Count 2][Len_Tx1 2][Tx1 bytes][Len_Tx2 2][Tx2 bytes]...
func SerializeBatch(txs []*Tx) ([]byte, error) {
	if len(txs) == 0 {
		return nil, rgerrors.New(rgerrors.ErrCanonicalEncodingDistortion, "empty batch")
	}
	if len(txs) > maxBatchTxs {
		return nil, rgerrors.Newf(rgerrors.ErrDosSizeExceeded, "too many txs in batch: %d exceeds %d", len(txs), maxBatchTxs)
	}

	buf := make([]byte, 12+2)
	copy(buf[:12], hash.DomainTxBatch)
	binary.BigEndian.PutUint16(buf[12:14], uint16(len(txs)))

	for _, t := range txs {
		b, err := t.Serialize()
		if err != nil {
			return nil, err
		}
		if len(b) > 0xFFFF {
			return nil, rgerrors.New(rgerrors.ErrDosSizeExceeded, "tx size exceeds uint16")
		}
		lenBuf := make([]byte, 2)
		binary.BigEndian.PutUint16(lenBuf, uint16(len(b)))
		buf = append(buf, lenBuf...)
		buf = append(buf, b...)
		if len(buf) > maxBatchBytes {
			return nil, rgerrors.Newf(rgerrors.ErrDosSizeExceeded, "batch exceeds %d bytes", maxBatchBytes)
		}
	}

	return buf, nil
}

// DeserializeBatch parses a byte slice into multiple transactions.
func DeserializeBatch(data []byte) ([]*Tx, error) {
	if len(data) > maxBatchBytes {
		return nil, rgerrors.Newf(rgerrors.ErrDosSizeExceeded, "batch exceeds %d bytes", maxBatchBytes)
	}
	if len(data) < 12+2 {
		return nil, rgerrors.New(rgerrors.ErrCanonicalEncodingDistortion, "batch too short")
	}

	if string(data[:12]) != string(hash.DomainTxBatch) {
		return nil, rgerrors.New(rgerrors.ErrCanonicalEncodingDistortion, "invalid batch domain prefix")
	}

	count := int(binary.BigEndian.Uint16(data[12:14]))
	if count > maxBatchTxs {
		return nil, rgerrors.Newf(rgerrors.ErrDosSizeExceeded, "too many txs in batch: %d exceeds %d", count, maxBatchTxs)
	}
	pos := 14
	txs := make([]*Tx, 0, count)

	for i := 0; i < count; i++ {
		if pos+2 > len(data) {
			return nil, rgerrors.New(rgerrors.ErrCanonicalEncodingDistortion, "batch truncated (count mismatch)")
		}
		txLen := int(binary.BigEndian.Uint16(data[pos : pos+2]))
		pos += 2
		if pos+txLen > len(data) {
			return nil, rgerrors.New(rgerrors.ErrCanonicalEncodingDistortion, "batch truncated (tx size mismatch)")
		}
		t, err := Deserialize(data[pos : pos+txLen])
		if err != nil {
			return nil, err
		}
		txs = append(txs, t)
		pos += txLen
	}
	if pos != len(data) {
		return nil, rgerrors.New(rgerrors.ErrCanonicalEncodingDistortion, "non-canonical batch trailing bytes")
	}

	return txs, nil
}
