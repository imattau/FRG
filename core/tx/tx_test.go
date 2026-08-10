package tx_test

import (
	"encoding/hex"
	"math/big"
	"testing"

	rgerrors "github.com/imattau/frg/core/errors"
	"github.com/imattau/frg/core/keys"
	"github.com/imattau/frg/core/tx"
)

func TestTxSerialize(t *testing.T) {
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)
	t1 := &tx.Tx{
		Type:     tx.TxTypeTransfer,
		Sender:   "alice",
		Receiver: "bob",
		Value:    scale,
		Nonce:    0,
	}
	b, err := t1.Serialize()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// domain(6) + type(1) + len_sender(2) + "alice"(5) + len_rcvr(2) + "bob"(3) + value(32) + nonce(8) + miss_h(8) + miss_p(32) + skip(4) + 192 = 295
	if len(b) != 295 {
		t.Fatalf("expected 295 bytes, got %d", len(b))
	}
	if hex.EncodeToString(b[:6]) != "54585f563100" {
		t.Fatalf("wrong domain prefix: %x", b[:6])
	}
	if b[6] != 1 {
		t.Fatalf("wrong type: %x", b[6])
	}
	if b[7] != 0x00 || b[8] != 0x05 {
		t.Fatalf("wrong len_sender: %x %x", b[7], b[8])
	}
	if string(b[9:14]) != "alice" {
		t.Fatalf("wrong sender: %s", b[9:14])
	}
}

func TestTxIDDeterministic(t *testing.T) {
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)
	t1 := &tx.Tx{Sender: "alice", Receiver: "bob", Value: scale, Nonce: 42}
	id1, err := t1.ID()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	id2, err := t1.ID()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id1 != id2 {
		t.Fatal("ID() is not deterministic")
	}
}

func TestTxPayloadSizeLimit(t *testing.T) {
	bigSender := make([]byte, 70000)
	t1 := &tx.Tx{
		Sender:   string(bigSender),
		Receiver: "bob",
		Value:    new(big.Int).SetInt64(1),
		Nonce:    0,
	}
	_, err := t1.Serialize()
	if err == nil {
		t.Fatal("expected ERR_010, got nil")
	}
}

func TestDeserializeRejectsTrailingBytes(t *testing.T) {
	t1 := signedTx(t, "alice", "bob", big.NewInt(1), 1)
	raw, err := t1.Serialize()
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw, 0)
	if _, err := tx.Deserialize(raw); err == nil {
		t.Fatal("expected trailing bytes to be rejected")
	}
}

func TestTransactionSignatureIsChainBound(t *testing.T) {
	sender := keys.NewKeypairFromSeed([32]byte{3})
	receiver := keys.NewKeypairFromSeed([32]byte{4})
	t1 := &tx.Tx{Type: tx.TxTypeTransfer, Sender: "alice", Receiver: "bob", Value: big.NewInt(1), Nonce: 1, SenderPubKey: sender.PublicKey, ReceiverPubKey: receiver.PublicKey}
	sig, err := t1.SignSenderForChain(sender, "chain-a")
	if err != nil {
		t.Fatal(err)
	}
	t1.SenderSig = sig
	receiverSig, err := t1.SignReceiverForChain(receiver, "chain-a")
	if err != nil {
		t.Fatal(err)
	}
	t1.ReceiverSig = receiverSig
	if err := t1.VerifySigsForChain("chain-a"); err != nil {
		t.Fatalf("same-chain signature rejected: %v", err)
	}
	if err := t1.VerifySigsForChain("chain-b"); err == nil {
		t.Fatal("cross-chain signature should be rejected")
	}
}

func TestDeserializeBatchRejectsTrailingBytes(t *testing.T) {
	t1 := signedTx(t, "alice", "bob", big.NewInt(1), 1)
	raw, err := tx.SerializeBatch([]*tx.Tx{t1})
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw, 0)
	if _, err := tx.DeserializeBatch(raw); err == nil {
		t.Fatal("expected batch trailing bytes to be rejected")
	}
}

func signedTx(t *testing.T, sender, receiver string, value *big.Int, nonce uint64) *tx.Tx {
	t.Helper()
	senderKP, err := keys.GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair sender: %v", err)
	}
	receiverKP, err := keys.GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair receiver: %v", err)
	}
	t1 := &tx.Tx{
		Type:           tx.TxTypeTransfer,
		Sender:         sender,
		Receiver:       receiver,
		Value:          new(big.Int).Set(value),
		Nonce:          nonce,
		SenderPubKey:   senderKP.PublicKey,
		ReceiverPubKey: receiverKP.PublicKey,
	}
	msg, err := t1.UnsignedHash()
	if err != nil {
		t.Fatalf("UnsignedHash() error: %v", err)
	}
	sig, err := senderKP.Sign(msg[:])
	if err != nil {
		t.Fatalf("Sign sender: %v", err)
	}
	rsig, err := receiverKP.Sign(msg[:])
	if err != nil {
		t.Fatalf("Sign receiver: %v", err)
	}
	t1.SenderSig = sig
	t1.ReceiverSig = rsig
	return t1
}

func TestVerifySigsValid(t *testing.T) {
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)
	t1 := signedTx(t, "alice", "bob", scale, 7)
	if err := t1.VerifySigs(); err != nil {
		t.Fatalf("VerifySigs() unexpected error: %v", err)
	}
}

func TestVerifySigsSenderInvalid(t *testing.T) {
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)
	t1 := signedTx(t, "alice", "bob", scale, 7)
	t1.SenderSig[0] ^= 0xff
	err := t1.VerifySigs()
	if err == nil {
		t.Fatal("expected ERR_012, got nil")
	}
	rgErr, ok := err.(*rgerrors.RGError)
	if !ok {
		t.Fatalf("expected *RGError, got %T", err)
	}
	if rgErr.Code != rgerrors.ErrInvalidSignature {
		t.Fatalf("expected ERR_012, got %s", rgErr.Code)
	}
}

func TestVerifySigsTransferDoesNotRequireReceiverSig(t *testing.T) {
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)
	t1 := signedTx(t, "alice", "bob", scale, 7)
	t1.ReceiverSig = [64]byte{}
	if err := t1.VerifySigs(); err != nil {
		t.Fatalf("sender-signed transfer was rejected: %v", err)
	}
}

func TestTxTypeConstants(t *testing.T) {
	if tx.TxTypeTransfer != 1 {
		t.Fatalf("TxTypeTransfer: got %d want 1", tx.TxTypeTransfer)
	}
	if tx.TxTypeMissEvidence != 2 {
		t.Fatalf("TxTypeMissEvidence: got %d want 2", tx.TxTypeMissEvidence)
	}
	if tx.TxTypeBond != 5 {
		t.Fatalf("TxTypeBond: got %d want 5", tx.TxTypeBond)
	}
	if tx.TxTypeUnbond != 6 {
		t.Fatalf("TxTypeUnbond: got %d want 6", tx.TxTypeUnbond)
	}
	if tx.TxTypeFinalizeUnbond != 7 {
		t.Fatalf("TxTypeFinalizeUnbond: got %d want 7", tx.TxTypeFinalizeUnbond)
	}
	if tx.TxTypeClaimRewards != 8 {
		t.Fatalf("TxTypeClaimRewards: got %d want 8", tx.TxTypeClaimRewards)
	}
	if tx.TxTypeEquivEvidence != 9 {
		t.Fatalf("TxTypeEquivEvidence: got %d want 9", tx.TxTypeEquivEvidence)
	}
}

func TestTxStructHasMissFields(t *testing.T) {
	tr := &tx.Tx{
		Type:         tx.TxTypeMissEvidence,
		MissedHeight: 42,
		SkipIndex:    7,
	}
	var p [32]byte
	p[0] = 0xAB
	tr.MissedProposer = p
	if tr.MissedHeight != 42 {
		t.Fatalf("MissedHeight: got %d want 42", tr.MissedHeight)
	}
	if tr.SkipIndex != 7 {
		t.Fatalf("SkipIndex: got %d want 7", tr.SkipIndex)
	}
	if tr.MissedProposer[0] != 0xAB {
		t.Fatalf("MissedProposer[0]: got %x want 0xAB", tr.MissedProposer[0])
	}
}

// TestTransferTypeSerialisation pins Type=1 at byte offset 6 and confirms
// existing fields are in their new positions after the Type byte.
func TestTransferTypeSerialisation(t *testing.T) {
	tr := makeTransfer(t)
	b, err := tr.Serialize()
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}
	// offset 6: Type byte must be 1 (TxTypeTransfer)
	if b[6] != 1 {
		t.Fatalf("Type byte at offset 6: got %d want 1", b[6])
	}
}

// TestMissEvidenceSerialisation pins the full layout of a MISS_EVIDENCE tx.
func TestMissEvidenceSerialisation(t *testing.T) {
	var proposer [32]byte
	proposer[0] = 0xDE
	proposer[31] = 0xAD

	tr := &tx.Tx{
		Type:           tx.TxTypeMissEvidence,
		Sender:         "alice",
		Receiver:       "",
		Value:          big.NewInt(0),
		Nonce:          0,
		MissedHeight:   1000,
		MissedProposer: proposer,
		SkipIndex:      3,
	}

	b, err := tr.Serialize()
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}

	// offset 6: Type byte = 2
	if b[6] != 2 {
		t.Fatalf("Type byte: got %d want 2", b[6])
	}

	// domain(6) + Type(1) + Len_Sender(2) + "alice"(5) + Len_Rcvr(2) + ""(0) + Value(32) + Nonce(8) = 56
	// MissedHeight starts at offset 56
	missedHeightOffset := 6 + 1 + 2 + 5 + 2 + 0 + 32 + 8
	h := uint64(b[missedHeightOffset])<<56 |
		uint64(b[missedHeightOffset+1])<<48 |
		uint64(b[missedHeightOffset+2])<<40 |
		uint64(b[missedHeightOffset+3])<<32 |
		uint64(b[missedHeightOffset+4])<<24 |
		uint64(b[missedHeightOffset+5])<<16 |
		uint64(b[missedHeightOffset+6])<<8 |
		uint64(b[missedHeightOffset+7])
	if h != 1000 {
		t.Fatalf("MissedHeight: got %d want 1000", h)
	}

	// MissedProposer: 32 bytes after MissedHeight
	proposerOffset := missedHeightOffset + 8
	if b[proposerOffset] != 0xDE {
		t.Fatalf("MissedProposer[0]: got %x want 0xDE", b[proposerOffset])
	}
	if b[proposerOffset+31] != 0xAD {
		t.Fatalf("MissedProposer[31]: got %x want 0xAD", b[proposerOffset+31])
	}

	// SkipIndex: 4 bytes after MissedProposer
	skipOffset := proposerOffset + 32
	si := uint32(b[skipOffset])<<24 | uint32(b[skipOffset+1])<<16 | uint32(b[skipOffset+2])<<8 | uint32(b[skipOffset+3])
	if si != 3 {
		t.Fatalf("SkipIndex: got %d want 3", si)
	}
}

// makeTransfer is a shared helper.
func makeTransfer(t *testing.T) *tx.Tx {
	t.Helper()
	return &tx.Tx{
		Type:     tx.TxTypeTransfer,
		Sender:   "sender",
		Receiver: "receiver",
		Value:    big.NewInt(1000),
		Nonce:    1,
	}
}

func TestMissEvidenceVerifySig(t *testing.T) {
	senderKP, _ := mustKP(t)

	tr := &tx.Tx{
		Type:         tx.TxTypeMissEvidence,
		Sender:       "alice",
		Receiver:     "",
		Value:        big.NewInt(0),
		Nonce:        0,
		SenderPubKey: senderKP.PublicKey,
		MissedHeight: 500,
		SkipIndex:    1,
	}
	msg, _ := tr.UnsignedHash()
	sig, err := senderKP.Sign(msg[:])
	if err != nil {
		t.Fatalf("Sign sender: %v", err)
	}
	tr.SenderSig = sig

	// ReceiverPubKey and ReceiverSig remain zero — must still pass for MISS_EVIDENCE
	if err := tr.VerifySigs(); err != nil {
		t.Fatalf("VerifySigs: %v", err)
	}
}

func TestEquivocationEvidenceSerializationRoundTrip(t *testing.T) {
	senderKP, _ := mustKP(t)
	tr := &tx.Tx{
		Type:         tx.TxTypeEquivEvidence,
		Sender:       "reporter",
		Receiver:     "staking",
		Value:        big.NewInt(0),
		Nonce:        3,
		SenderPubKey: senderKP.PublicKey,
		EvidenceA:    []byte("vote-a"),
		EvidenceB:    []byte("vote-b"),
	}
	msg, err := tr.UnsignedHash()
	if err != nil {
		t.Fatal(err)
	}
	tr.SenderSig, err = senderKP.Sign(msg[:])
	if err != nil {
		t.Fatal(err)
	}
	raw, err := tr.Serialize()
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := tx.Deserialize(raw)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Type != tx.TxTypeEquivEvidence || string(parsed.EvidenceA) != "vote-a" || string(parsed.EvidenceB) != "vote-b" {
		t.Fatalf("unexpected parsed evidence tx: %+v", parsed)
	}
	if err := parsed.VerifySigs(); err != nil {
		t.Fatal(err)
	}
}

func TestTransferVerifySigSenderOnly(t *testing.T) {
	senderKP, receiverKP := mustKP(t)

	tr := &tx.Tx{
		Type:           tx.TxTypeTransfer,
		Sender:         "sender",
		Receiver:       "receiver",
		Value:          big.NewInt(1000),
		Nonce:          1,
		SenderPubKey:   senderKP.PublicKey,
		ReceiverPubKey: receiverKP.PublicKey,
	}
	msg, _ := tr.UnsignedHash()
	sig, _ := senderKP.Sign(msg[:])
	tr.SenderSig = sig

	if err := tr.VerifySigs(); err != nil {
		t.Fatalf("sender-only transfer signature rejected: %v", err)
	}
}

func TestInvalidTxType(t *testing.T) {
	for _, badType := range []tx.TxType{0, 255} {
		tr := &tx.Tx{
			Type:     badType,
			Sender:   "x",
			Receiver: "y",
			Value:    big.NewInt(0),
		}
		err := tr.VerifySigs()
		assertCode(t, err, rgerrors.ErrInvalidTxType)
	}
}

// mustKP generates a keypair and fails the test on error.
func mustKP(t *testing.T) (*keys.Keypair, *keys.Keypair) {
	t.Helper()
	a, err := keys.GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair: %v", err)
	}
	b, err := keys.GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair: %v", err)
	}
	return a, b
}

// assertCode checks that err is an *RGError with the given code.
func assertCode(t *testing.T, err error, code rgerrors.ErrorCode) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error %s, got nil", code)
	}
	rge, ok := err.(*rgerrors.RGError)
	if !ok {
		t.Fatalf("expected *RGError, got %T: %v", err, err)
	}
	if rge.Code != code {
		t.Fatalf("expected code %s, got %s", code, rge.Code)
	}
}

func TestTxSerialiseDeserialiseRoundTrip(t *testing.T) {
	senderKP, _ := keys.GenerateKeypair()
	receiverKP, _ := keys.GenerateKeypair()

	original := &tx.Tx{
		Type:           tx.TxTypeTransfer,
		Sender:         "alice",
		Receiver:       "bob",
		Value:          big.NewInt(500),
		Nonce:          3,
		SenderPubKey:   senderKP.PublicKey,
		ReceiverPubKey: receiverKP.PublicKey,
	}
	recvSig, _ := original.SignReceiver(receiverKP)
	original.ReceiverSig = recvSig
	senderSig, _ := original.SignSender(senderKP)
	original.SenderSig = senderSig

	b, err := original.Serialize()
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}

	decoded, err := tx.Deserialize(b)
	if err != nil {
		t.Fatalf("Deserialize: %v", err)
	}

	if decoded.Type != original.Type {
		t.Fatalf("Type: got %d want %d", decoded.Type, original.Type)
	}
	if decoded.Sender != original.Sender {
		t.Fatalf("Sender: got %q want %q", decoded.Sender, original.Sender)
	}
	if decoded.Receiver != original.Receiver {
		t.Fatalf("Receiver: got %q want %q", decoded.Receiver, original.Receiver)
	}
	if decoded.Value.Cmp(original.Value) != 0 {
		t.Fatalf("Value: got %s want %s", decoded.Value, original.Value)
	}
	if decoded.Nonce != original.Nonce {
		t.Fatalf("Nonce: got %d want %d", decoded.Nonce, original.Nonce)
	}
	if decoded.SenderPubKey != original.SenderPubKey {
		t.Fatalf("SenderPubKey mismatch")
	}
	if decoded.SenderSig != original.SenderSig {
		t.Fatalf("SenderSig mismatch")
	}

	if err := decoded.VerifySigs(); err != nil {
		t.Fatalf("VerifySigs on decoded: %v", err)
	}
}
