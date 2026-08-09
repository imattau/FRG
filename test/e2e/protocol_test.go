package e2e_test

import (
	"bytes"
	"encoding/binary"
	"math/big"
	"testing"

	rgerrors "github.com/imattau/frg/core/errors"
	"github.com/imattau/frg/core/hash"
	"github.com/imattau/frg/core/node"
	"github.com/imattau/frg/core/tx"
	"github.com/imattau/frg/core/tree"
)

func TestSingleTxBlock(t *testing.T) {
	sender := makeKeypair(t)
	receiver := makeKeypair(t)
	tr := makeTx(t, sender, receiver, 100, 1)
	root := buildBlock(t, 1, []*tx.Tx{tr})
	if root == ([32]byte{}) {
		t.Fatal("root is zero")
	}
}

func TestFullBlock(t *testing.T) {
	sender := makeKeypair(t)
	receiver := makeKeypair(t)
	txs := make([]*tx.Tx, tree.TMax)
	for i := range txs {
		txs[i] = makeTx(t, sender, receiver, int64(i+1), uint64(i))
	}
	root := buildBlock(t, 1, txs)
	if root == ([32]byte{}) {
		t.Fatal("root is zero")
	}
}

func TestPaddingCorrectness(t *testing.T) {
	sender := makeKeypair(t)
	receiver := makeKeypair(t)
	// 5 txs: first chunk full (4), second chunk has 1 real + 3 padded
	txs := make([]*tx.Tx, 5)
	for i := range txs {
		txs[i] = makeTx(t, sender, receiver, int64(i+1), uint64(i))
	}
	// BuildRoot must not error — padding is handled internally
	root := buildBlock(t, 1, txs)
	if root == ([32]byte{}) {
		t.Fatal("root is zero")
	}
}

func TestEmptyBlockAnchor(t *testing.T) {
	root := buildBlock(t, 1, nil)
	expected := node.EmptyBlockRoot()
	if root != expected {
		t.Fatalf("empty block root mismatch: got %x want %x", root, expected)
	}
}

func TestTxSerialisation(t *testing.T) {
	sender := makeKeypair(t)
	receiver := makeKeypair(t)
	tr := &tx.Tx{
		Type:           tx.TxTypeTransfer,
		Sender:         "alice",
		Receiver:       "bob",
		Value:          big.NewInt(42),
		Nonce:          7,
		SenderPubKey:   sender.PublicKey,
		ReceiverPubKey: receiver.PublicKey,
	}
	b, err := tr.Serialize()
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}
	// domain prefix TX_V1\x00 (6 bytes)
	if !bytes.Equal(b[:6], []byte{0x54, 0x58, 0x5F, 0x56, 0x31, 0x00}) {
		t.Fatal("missing TX_V1 domain prefix")
	}
	// Type at offset 6
	if b[6] != byte(tx.TxTypeTransfer) {
		t.Fatalf("Type: got %d want %d", b[6], tx.TxTypeTransfer)
	}
	// Len_Sender at offset 7 (uint16)
	lenSender := binary.BigEndian.Uint16(b[7:9])
	if int(lenSender) != len("alice") {
		t.Fatalf("Len_Sender: got %d want %d", lenSender, len("alice"))
	}
}

func TestNodeSerialisation(t *testing.T) {
	n := &node.RGNode{
		Scale:    1,
		Volume:   node.Uint256ToBytes(big.NewInt(100)),
		Sig:      node.SigAtomic,
		Children: [][32]byte{hash.Hash([]byte("test"))},
	}
	b, err := n.Serialize()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	// domain prefix RG_NODE_V1\x00 (11 bytes)
	if !bytes.Equal(b[:11], []byte{0x52, 0x47, 0x5F, 0x4E, 0x4F, 0x44, 0x45, 0x5F, 0x56, 0x31, 0x00}) {
		t.Fatal("missing RG_NODE_V1 domain prefix")
	}
	// Scale at offset 11 (uint32 big-endian) = 1
	scale := binary.BigEndian.Uint32(b[11:15])
	if scale != 1 {
		t.Fatalf("Scale: got %d want 1", scale)
	}
}

func TestSignatureRecomputation(t *testing.T) {
	n := &node.RGNode{
		Scale:    4,
		Volume:   node.Uint256ToBytes(big.NewInt(500)),
		Sig:      node.SigVolatileShock, // wrong — will fail recompute
		Children: make([][32]byte, 4),
	}
	_, err := n.RecomputeSig()
	if err == nil {
		t.Fatal("expected ERR_007, got nil")
	}
	assertCode(t, err, rgerrors.ErrSignatureMisrepresentation)
}

func TestHashBoundary(t *testing.T) {
	h := hash.Hash([]byte("test"))
	if len(h) != 32 {
		t.Fatalf("hash length: got %d want 32", len(h))
	}
}

func TestScaleDomain(t *testing.T) {
	if hash.ValidScale(3) {
		t.Fatal("scale 3 should be invalid")
	}
	if !hash.ValidScale(4) {
		t.Fatal("scale 4 should be valid")
	}
	if hash.ValidScale(65537) {
		t.Fatal("scale 65537 should be invalid")
	}
}

func TestChildOrderPreserved(t *testing.T) {
	sender := makeKeypair(t)
	receiver := makeKeypair(t)
	txs := []*tx.Tx{
		makeTx(t, sender, receiver, 100, 1),
		makeTx(t, sender, receiver, 200, 2),
		makeTx(t, sender, receiver, 300, 3),
		makeTx(t, sender, receiver, 400, 4),
	}
	root1 := buildBlock(t, 1, txs)
	// Reverse order — same txs, different order → different root
	reversed := []*tx.Tx{txs[3], txs[2], txs[1], txs[0]}
	root2 := buildBlock(t, 1, reversed)
	if root1 == root2 {
		t.Fatal("child order not preserved — roots should differ")
	}
}
