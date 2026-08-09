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
)

type ValidatorEntry struct {
    PubKey string `json:"pubkey"` // hex-encoded [32]byte
    Bond   string `json:"bond"`   // decimal string
}

type BalanceEntry struct {
    Account string `json:"account"` // hex-encoded [32]byte
    Amount  string `json:"amount"`  // decimal string
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
    height, err := sm.CurrentHeight()
    if err != nil {
        return fmt.Errorf("current height: %w", err)
    }
    if height > 0 {
        return nil
    }

    // Seed balances first
    for _, entry := range g.Balances {
        var addr [32]byte
        b, err := hex.DecodeString(entry.PubKeyFromAccount(entry.Account))
        if err != nil {
            return fmt.Errorf("invalid account hex %s: %w", entry.Account, err)
        }
        copy(addr[:], b)
        
        amt, ok := new(big.Int).SetString(entry.Amount, 10)
        if !ok {
            return fmt.Errorf("invalid amount string %s", entry.Amount)
        }
        if err := l.Seed(addr, amt); err != nil {
            return fmt.Errorf("seed balance for %s: %w", entry.Account, err)
        }
    }

    // Bond validators
    for _, entry := range g.Validators {
        var pub [32]byte
        b, err := hex.DecodeString(entry.PubKey)
        if err != nil {
            return fmt.Errorf("invalid validator pubkey hex %s: %w", entry.PubKey, err)
        }
        copy(pub[:], b)

        amt, ok := new(big.Int).SetString(entry.Bond, 10)
        if !ok {
            return fmt.Errorf("invalid bond string %s", entry.Bond)
        }
        if err := s.Bond(pub, amt, 0); err != nil {
            return fmt.Errorf("bond validator %s: %w", entry.PubKey, err)
        }
    }

    return nil
}

func (b BalanceEntry) PubKeyFromAccount(account string) string {
    return account
}
