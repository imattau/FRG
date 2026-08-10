package genesis_test

import (
	"encoding/hex"
	"math/big"
	"os"
	"path/filepath"
	"testing"

	"github.com/imattau/frg/core/denom"
	"github.com/imattau/frg/core/genesis"
	"github.com/imattau/frg/core/ledger"
	"github.com/imattau/frg/core/staking"
	"github.com/imattau/frg/core/statemachine"
	bolt "go.etcd.io/bbolt"
)

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return p
}

func q(frg int64) string {
	return new(big.Int).Mul(big.NewInt(frg), denom.QuantaPerFRG).String()
}

func TestLoadGenesis(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "genesis.json", `{
        "validators": [{"pubkey":"0000000000000000000000000000000000000000000000000000000000000001","bond":"2000"}],
        "balances":   [{"account":"0000000000000000000000000000000000000000000000000000000000000001","amount":"5000"}]
    }`)
	g, err := genesis.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Validators) != 1 || g.Validators[0].Bond != "2000" {
		t.Fatalf("unexpected validators: %+v", g.Validators)
	}
	if len(g.Balances) != 1 || g.Balances[0].Amount != "5000" {
		t.Fatalf("unexpected balances: %+v", g.Balances)
	}
}

func TestLoadGenesisMalformed(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "genesis.json", `not json`)
	_, err := genesis.Load(p)
	if err == nil {
		t.Fatal("expected error for malformed JSON")
	}
}

func TestApplyFreshDB(t *testing.T) {
	dir := t.TempDir()
	db, _ := bolt.Open(filepath.Join(dir, "frg.db"), 0600, nil)
	defer db.Close()

	l, _ := ledger.New(db)
	s, _ := staking.New(db, l)
	sm, _ := statemachine.New(db, l, s)

	g := &genesis.Genesis{
		Balances: []genesis.BalanceEntry{
			{Account: hex.EncodeToString(make([]byte, 32)), Amount: q(10000)},
		},
		Validators: []genesis.ValidatorEntry{
			{PubKey: hex.EncodeToString(make([]byte, 32)), Bond: q(5000)},
		},
	}

	if err := genesis.Apply(sm, l, s, g); err != nil {
		t.Fatal(err)
	}
	totalSupply, tracked, err := sm.CurrentTotalSupply()
	if err != nil {
		t.Fatal(err)
	}
	if !tracked {
		t.Fatal("genesis did not initialize total supply")
	}
	wantSupply := new(big.Int).Mul(big.NewInt(10000), denom.QuantaPerFRG)
	if totalSupply.Cmp(wantSupply) != 0 {
		t.Fatalf("expected total supply %s, got %s", wantSupply, totalSupply)
	}

	h, _ := sm.CurrentHeight()
	if h != 0 {
		t.Fatalf("height should be 0, got %d", h)
	}

	var addr [32]byte
	bal, _ := l.BalanceOf(addr)
	// 10000 - 5000 (bonded) = 5000
	wantBal := new(big.Int).Mul(big.NewInt(5000), denom.QuantaPerFRG)
	if bal.Cmp(wantBal) != 0 {
		t.Fatalf("expected %s balance, got %s", wantBal, bal)
	}

	vset, _ := s.ValidatorSet()
	if len(vset) != 1 {
		t.Fatal("expected 1 validator")
	}
	if err := genesis.Apply(sm, l, s, g); err != nil {
		t.Fatalf("second genesis apply should be idempotent: %v", err)
	}
}

func TestApplyIdempotent(t *testing.T) {
	dir := t.TempDir()
	db, _ := bolt.Open(filepath.Join(dir, "frg.db"), 0600, nil)
	defer db.Close()

	l, _ := ledger.New(db)
	s, _ := staking.New(db, l)
	sm, _ := statemachine.New(db, l, s)

	g := &genesis.Genesis{
		Balances: []genesis.BalanceEntry{
			{Account: hex.EncodeToString(make([]byte, 32)), Amount: "10000"},
		},
	}

	if err := genesis.Apply(sm, l, s, g); err != nil {
		t.Fatal(err)
	}
	if err := genesis.Apply(sm, l, s, g); err != nil {
		t.Fatal(err)
	}

	var addr [32]byte
	bal, _ := l.BalanceOf(addr)
	if bal.Int64() != 10000 {
		t.Fatalf("expected 10000 balance, got %d", bal.Int64())
	}
}
