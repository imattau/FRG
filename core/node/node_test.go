package node_test

import (
	"encoding/hex"
	"math/big"
	"testing"

	"github.com/imattau/frg/core/node"
)

func TestNullNodeRootDeterministic(t *testing.T) {
	n1, err := node.NullNode(1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	r1, err := n1.Root()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	n2, err := node.NullNode(1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	r2, err := n2.Root()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r1 != r2 {
		t.Fatal("NullNode root is not deterministic")
	}
}

func TestNullNodeInvalidScale(t *testing.T) {
	_, err := node.NullNode(3)
	if err == nil {
		t.Fatal("expected error for invalid scale")
	}
}

func TestEmptyBlockRoot(t *testing.T) {
	r1 := node.EmptyBlockRoot()
	r2 := node.EmptyBlockRoot()
	if r1 != r2 {
		t.Fatal("EmptyBlockRoot is not stable")
	}
	var zero [32]byte
	if r1 == zero {
		t.Fatal("EmptyBlockRoot returned all-zero hash")
	}
}

func TestRecomputeSigAtomic(t *testing.T) {
	n := &node.RGNode{
		Scale:    1,
		Volume:   node.Uint256ToBytes(big.NewInt(1)),
		Sig:      node.SigAtomic,
		Children: [][32]byte{{}},
	}
	sig, err := n.RecomputeSig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sig != node.SigAtomic {
		t.Fatalf("expected SigAtomic, got %d", sig)
	}
}

func TestNodeSerializeRoundtrip(t *testing.T) {
	var child [32]byte
	copy(child[:], make([]byte, 32))
	n := &node.RGNode{
		Scale:    1,
		Sig:      node.SigStagnantState,
		Children: [][32]byte{child},
	}
	b, err := n.Serialize()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(b) != 114 {
		t.Fatalf("expected 114 bytes, got %d: %s", len(b), hex.EncodeToString(b))
	}
	if hex.EncodeToString(b[:11]) != "52475f4e4f44455f563100" {
		t.Fatalf("wrong domain prefix: %x", b[:11])
	}
}
