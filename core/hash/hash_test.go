package hash_test

import (
	"encoding/hex"
	"testing"

	"github.com/imattau/frg/core/hash"
)

func TestDomainPrefixes(t *testing.T) {
	if got := []byte(hash.DomainTx); hex.EncodeToString(got) != "54585f563100" {
		t.Fatalf("DomainTx wrong: %x", got)
	}
	if got := []byte(hash.DomainRGNode); hex.EncodeToString(got) != "52475f4e4f44455f563100" {
		t.Fatalf("DomainRGNode wrong: %x", got)
	}
	if got := []byte(hash.DomainNullPad); hex.EncodeToString(got) != "4e554c4c5f5041445f563100" {
		t.Fatalf("DomainNullPad wrong: %x", got)
	}
	if got := []byte(hash.DomainEmptyBlock); hex.EncodeToString(got) != "454d5054595f424c4f434b5f563100" {
		t.Fatalf("DomainEmptyBlock wrong: %x", got)
	}
}

func TestHashOutput32Bytes(t *testing.T) {
	h := hash.Hash([]byte("test"))
	if len(h) != 32 {
		t.Fatalf("expected 32 bytes, got %d", len(h))
	}
}

func TestValidScale(t *testing.T) {
	valid := []uint32{1, 4, 16, 64, 256, 1024, 4096, 16384, 65536}
	for _, v := range valid {
		if !hash.ValidScale(v) {
			t.Errorf("expected %d to be valid", v)
		}
	}
	invalid := []uint32{0, 2, 3, 5, 65537, 131072}
	for _, v := range invalid {
		if hash.ValidScale(v) {
			t.Errorf("expected %d to be invalid", v)
		}
	}
}
