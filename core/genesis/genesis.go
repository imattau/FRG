package genesis

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"os"

	"github.com/imattau/frg/core/ledger"
	"github.com/imattau/frg/core/staking"
	"github.com/imattau/frg/core/statemachine"
	bolt "go.etcd.io/bbolt"
)

type ValidatorEntry struct {
	PubKey string `json:"pubkey"` // hex-encoded [32]byte
	Bond   string `json:"bond"`   // decimal quanta string
}

type BalanceEntry struct {
	Account string `json:"account"` // hex-encoded [32]byte
	Amount  string `json:"amount"`  // decimal quanta string
}

type Genesis struct {
	ChainID    string           `json:"chain_id"`
	Validators []ValidatorEntry `json:"validators"`
	Balances   []BalanceEntry   `json:"balances"`
}

// Load parses genesis.json from disk.
func Load(path string) (*Genesis, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read genesis file: %w", err)
	}
	var g Genesis
	if err := json.Unmarshal(data, &g); err != nil {
		return nil, fmt.Errorf("unmarshal genesis: %w", err)
	}
	return &g, nil
}

// Apply seeds balances and bonds validators.
// No-op if statemachine.CurrentHeight() > 0.
func Apply(sm *statemachine.StateMachine, l *ledger.Ledger, s *staking.Store, g *Genesis) error {
	applied, err := sm.GenesisApplied()
	if err != nil {
		return fmt.Errorf("genesis status: %w", err)
	}
	height, err := sm.CurrentHeight()
	if err != nil {
		return fmt.Errorf("current height: %w", err)
	}
	if applied || height > 0 {
		return nil
	}

	return sm.Update(func(btx *bolt.Tx) error {
		genesisBalances := make(map[[32]byte]*big.Int, len(g.Balances))
		for _, entry := range g.Balances {
			addr, err := decodeKey(entry.Account, "account")
			if err != nil {
				return err
			}
			amt, ok := new(big.Int).SetString(entry.Amount, 10)
			if !ok {
				return fmt.Errorf("invalid amount string %s", entry.Amount)
			}
			if err := l.SeedTx(btx, addr, amt); err != nil {
				return fmt.Errorf("seed balance for %s: %w", entry.Account, err)
			}
			genesisBalances[addr] = new(big.Int).Set(amt)
		}
		totalSupply := new(big.Int)
		for _, amt := range genesisBalances {
			totalSupply.Add(totalSupply, amt)
		}
		if err := sm.SetTotalSupplyTx(btx, totalSupply); err != nil {
			return fmt.Errorf("set genesis total supply: %w", err)
		}
		for _, entry := range g.Validators {
			pub, err := decodeKey(entry.PubKey, "validator pubkey")
			if err != nil {
				return err
			}
			amt, ok := new(big.Int).SetString(entry.Bond, 10)
			if !ok {
				return fmt.Errorf("invalid bond string %s", entry.Bond)
			}
			if err := s.BondTx(btx, pub, amt, 0); err != nil {
				return fmt.Errorf("bond validator %s: %w", entry.PubKey, err)
			}
		}
		return sm.MarkGenesisAppliedTx(btx)
	})
}

func decodeKey(value, label string) ([32]byte, error) {
	var out [32]byte
	b, err := hex.DecodeString(value)
	if err != nil || len(b) != len(out) {
		return out, fmt.Errorf("invalid %s: expected 32-byte hex", label)
	}
	copy(out[:], b)
	return out, nil
}

func (b BalanceEntry) PubKeyFromAccount(account string) string {
	return account
}
