package staking

import (
	"encoding/binary"
	"math/big"
	"path/filepath"
	"testing"

	"github.com/imattau/frg/core/denom"
	"github.com/imattau/frg/core/ledger"
)

func q(frg int64) *big.Int {
	return new(big.Int).Mul(big.NewInt(frg), denom.QuantaPerFRG)
}

func TestRecordSerialisation(t *testing.T) {
	// Old 49-byte record must decode with MissCount=0
	old := make([]byte, 49)
	old[0] = 1 // stateBonded
	// BondedAmount = 5000
	amt := new(big.Int).SetInt64(5000)
	amtBytes := amt.Bytes()
	copy(old[1+32-len(amtBytes):33], amtBytes)
	binary.BigEndian.PutUint64(old[33:41], 42) // BondedAtBlock
	binary.BigEndian.PutUint64(old[41:49], 0)  // UnbondingAtBlock

	rec := decodeRecord(old)
	if rec.MissCount != 0 {
		t.Fatalf("old record MissCount: got %d want 0", rec.MissCount)
	}
	if rec.BondedAmount.Int64() != 5000 {
		t.Fatalf("BondedAmount: got %d want 5000", rec.BondedAmount.Int64())
	}

	// New 57-byte round-trip
	rec.MissCount = 3
	encoded := encodeRecord(rec)
	if len(encoded) != 57 {
		t.Fatalf("encoded length: got %d want 57", len(encoded))
	}
	decoded := decodeRecord(encoded)
	if decoded.MissCount != 3 {
		t.Fatalf("round-trip MissCount: got %d want 3", decoded.MissCount)
	}
}

func TestRecordMiss(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "staking.db")
	l, _ := ledger.Open(filepath.Join(tmp, "ledger.db"))
	s, _ := Open(path, l)
	defer s.Close()

	val := [32]byte{0x01}
	amt := q(5000)
	s.ledger.Seed(val, amt)
	s.Bond(val, amt, 1)

	// Misses 1-4: just increment
	for i := uint64(1); i < MissThreshold; i++ {
		count, err := s.RecordMiss(val)
		if err != nil {
			t.Fatalf("RecordMiss %d: %v", i, err)
		}
		if count != i {
			t.Fatalf("RecordMiss %d: got count %d want %d", i, count, i)
		}

		storedCount, _ := s.MissCountOf(val)
		if storedCount != i {
			t.Fatalf("MissCountOf %d: got %d want %d", i, storedCount, i)
		}
	}

	// 5th miss: slash 10% and reset count to 0
	count, err := s.RecordMiss(val)
	if err != nil {
		t.Fatalf("RecordMiss 5: %v", err)
	}
	if count != 0 {
		t.Fatalf("RecordMiss 5: got count %d want 0 (reset)", count)
	}

	storedCount, _ := s.MissCountOf(val)
	if storedCount != 0 {
		t.Fatalf("MissCountOf 5: got %d want 0", storedCount)
	}

	// Check bond amount after slash (5000 FRG - 10% = 4500 FRG)
	_, amounts, _ := s.BondedAmounts()
	if amounts[0].Cmp(q(4500)) != 0 {
		t.Fatalf("BondedAmount after slash: got %s want %s", amounts[0], q(4500))
	}
}
