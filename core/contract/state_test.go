package contract_test

import (
	"bytes"
	"testing"

	"github.com/imattau/frg/core/contract"
)

func TestStateStoreEnforcesBounds(t *testing.T) {
	s := contract.NewStateStore()
	if err := s.Set(bytes.Repeat([]byte{'k'}, 33), []byte{1}); err == nil {
		t.Fatal("expected oversized key to be rejected")
	}
	if err := s.Set([]byte("key"), make([]byte, 64*1024+1)); err == nil {
		t.Fatal("expected oversized value to be rejected")
	}
}
