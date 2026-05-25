package leader_test

import (
	"testing"

	rgerrors "github.com/imattau/frg/core/errors"
	"github.com/imattau/frg/core/leader"
)

func TestElectedProposerDeterministic(t *testing.T) {
	validators := [][32]byte{
		{1}, {2}, {3}, {4},
	}
	var prevRoot [32]byte
	copy(prevRoot[:], []byte("some state root here            "))

	a, err := leader.ElectedProposer(prevRoot, 100, validators)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	b, err := leader.ElectedProposer(prevRoot, 100, validators)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a != b {
		t.Fatalf("non-deterministic: got %x then %x", a, b)
	}
}

func TestEmptyValidatorSetError(t *testing.T) {
	var prevRoot [32]byte
	_, err := leader.ElectedProposer(prevRoot, 1, nil)
	if err == nil {
		t.Fatal("expected error for empty validator set")
	}
	rge, ok := err.(*rgerrors.RGError)
	if !ok {
		t.Fatalf("expected *RGError, got %T", err)
	}
	if rge.Code != rgerrors.ErrEmptyValidatorSet {
		t.Fatalf("expected ERR_020, got %s", rge.Code)
	}
}

func TestElectedProposerDistribution(t *testing.T) {
	validators := [][32]byte{
		{1}, {2}, {3}, {4},
	}
	counts := map[[32]byte]int{}
	total := 10000
	var prevRoot [32]byte

	for h := uint64(0); h < uint64(total); h++ {
		p, err := leader.ElectedProposer(prevRoot, h, validators)
		if err != nil {
			t.Fatalf("height %d: %v", h, err)
		}
		counts[p]++
	}

	for _, v := range validators {
		pct := float64(counts[v]) / float64(total)
		if pct < 0.20 || pct > 0.30 {
			t.Errorf("validator %x elected %.1f%% of the time (want ~25%%)", v, pct*100)
		}
	}
}

func TestElectedProposerSortOrder(t *testing.T) {
	// unsorted input must give same result as sorted input
	sorted := [][32]byte{{1}, {2}, {3}, {4}}
	unsorted := [][32]byte{{3}, {1}, {4}, {2}}

	var prevRoot [32]byte
	copy(prevRoot[:], []byte("determinism check               "))

	for h := uint64(0); h < 100; h++ {
		a, _ := leader.ElectedProposer(prevRoot, h, sorted)
		b, _ := leader.ElectedProposer(prevRoot, h, unsorted)
		if a != b {
			t.Fatalf("height %d: sorted=%x unsorted=%x", h, a, b)
		}
	}
}

func TestSkipProposerZeroEqualsElected(t *testing.T) {
	validators := [][32]byte{{10}, {20}, {30}, {40}}
	var prevRoot [32]byte
	copy(prevRoot[:], []byte("skip zero test                  "))

	for h := uint64(0); h < 50; h++ {
		elected, _ := leader.ElectedProposer(prevRoot, h, validators)
		skip0, _ := leader.SkipProposer(prevRoot, h, validators, 0)
		if elected != skip0 {
			t.Fatalf("height %d: ElectedProposer=%x SkipProposer(0)=%x", h, elected, skip0)
		}
	}
}

func TestSkipProposerIndex1(t *testing.T) {
	// With 4 validators sorted as {10},{20},{30},{40},
	// skip1 must differ from elected (unless set has 1 member).
	validators := [][32]byte{{10}, {20}, {30}, {40}}
	var prevRoot [32]byte

	differentCount := 0
	for h := uint64(0); h < 100; h++ {
		elected, _ := leader.ElectedProposer(prevRoot, h, validators)
		skip1, _ := leader.SkipProposer(prevRoot, h, validators, 1)
		if elected != skip1 {
			differentCount++
		}
	}
	// With 4 validators, skip1 should differ most of the time
	if differentCount < 70 {
		t.Fatalf("skip1 same as elected in %d/100 cases — expected mostly different", 100-differentCount)
	}
}

func TestSkipProposerWraparound(t *testing.T) {
	validators := [][32]byte{{10}, {20}, {30}, {40}}
	var prevRoot [32]byte

	for h := uint64(0); h < 100; h++ {
		elected, _ := leader.ElectedProposer(prevRoot, h, validators)
		skip4, _ := leader.SkipProposer(prevRoot, h, validators, 4)
		if elected != skip4 {
			t.Fatalf("height %d: skipCount=len(validators) should wrap back to elected", h)
		}
		
		skip8, _ := leader.SkipProposer(prevRoot, h, validators, 8)
		if elected != skip8 {
			t.Fatalf("height %d: skipCount=2*len(validators) should wrap back to elected", h)
		}
	}
}

func TestSingleValidator(t *testing.T) {
	validators := [][32]byte{{10}}
	var prevRoot [32]byte

	for h := uint64(0); h < 100; h++ {
		elected, _ := leader.ElectedProposer(prevRoot, h, validators)
		if elected != validators[0] {
			t.Fatalf("height %d: single validator not elected", h)
		}
		
		skip1, _ := leader.SkipProposer(prevRoot, h, validators, 1)
		if skip1 != validators[0] {
			t.Fatalf("height %d: single validator skip1 mismatch", h)
		}
	}
}
