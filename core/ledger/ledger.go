package ledger

import (
	"math/big"

	rgerrors "github.com/imattau/frg/core/errors"
	"github.com/imattau/frg/core/tx"
	bolt "go.etcd.io/bbolt"
)

var balancesBucket = []byte("balances")
var maxUint256 = new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))

// Ledger is a bbolt-backed balance store. Keys are 32-byte Ed25519 public keys;
// values are 32-byte big-endian uint256 quanta balances.
type Ledger struct {
	db *bolt.DB
}

// Open opens or creates a ledger database at path.
func Open(path string) (*Ledger, error) {
	db, err := bolt.Open(path, 0600, nil)
	if err != nil {
		return nil, err
	}
	if err := db.Update(func(btx *bolt.Tx) error {
		_, err := btx.CreateBucketIfNotExists(balancesBucket)
		return err
	}); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Ledger{db: db}, nil
}

// Close closes the underlying database.
func (l *Ledger) Close() error {
	return l.db.Close()
}

// BalanceOf returns the quanta balance for account.
// Returns zero for unknown accounts (no error).
func (l *Ledger) BalanceOf(account [32]byte) (*big.Int, error) {
	var bal *big.Int
	err := l.db.View(func(btx *bolt.Tx) error {
		v := btx.Bucket(balancesBucket).Get(account[:])
		if v == nil {
			bal = big.NewInt(0)
			return nil
		}
		bal = new(big.Int).SetBytes(v)
		return nil
	})
	return bal, err
}

// Transfer applies a validated Tx: verifies 2-of-2 sigs, checks sender has
// sufficient balance, debits SenderPubKey, credits ReceiverPubKey atomically.
// Returns ERR_012 on invalid sig, ERR_013 if sender balance < tx.Value,
// ERR_001 on arithmetic overflow.
func (l *Ledger) Transfer(t *tx.Tx) error {
	if err := t.VerifySigs(); err != nil {
		return err
	}
	if err := validateUint256(t.Value, "transfer value"); err != nil {
		return err
	}
	return l.db.Update(func(btx *bolt.Tx) error {
		b := btx.Bucket(balancesBucket)
		senderBal := readBalance(b, t.SenderPubKey)
		if senderBal.Cmp(t.Value) < 0 {
			return rgerrors.New(rgerrors.ErrInsufficientFunds, "sender balance insufficient")
		}
		senderBal.Sub(senderBal, t.Value)
		receiverBal := readBalance(b, t.ReceiverPubKey)
		receiverBal.Add(receiverBal, t.Value)
		if err := writeBalance(b, t.SenderPubKey, senderBal); err != nil {
			return err
		}
		return writeBalance(b, t.ReceiverPubKey, receiverBal)
	})
}

// Burn destroys amount quanta from account's balance.
// Returns ERR_013 if balance < amount.
func (l *Ledger) Burn(account [32]byte, amount *big.Int) error {
	if err := validateUint256(amount, "burn amount"); err != nil {
		return err
	}
	return l.db.Update(func(btx *bolt.Tx) error {
		b := btx.Bucket(balancesBucket)
		bal := readBalance(b, account)
		if bal.Cmp(amount) < 0 {
			return rgerrors.New(rgerrors.ErrInsufficientFunds, "insufficient balance to burn")
		}
		bal.Sub(bal, amount)
		return writeBalance(b, account, bal)
	})
}

// Move atomically debits from and credits to by amount.
// For internal protocol use only - no sig verification.
// Returns ERR_013 if from balance < amount, ERR_001 on overflow.
func (l *Ledger) Move(from, to [32]byte, amount *big.Int) error {
	if err := validateUint256(amount, "move amount"); err != nil {
		return err
	}
	return l.db.Update(func(btx *bolt.Tx) error {
		b := btx.Bucket(balancesBucket)
		fromBal := readBalance(b, from)
		if fromBal.Cmp(amount) < 0 {
			return rgerrors.New(rgerrors.ErrInsufficientFunds, "insufficient balance to move")
		}
		fromBal.Sub(fromBal, amount)
		toBal := readBalance(b, to)
		toBal.Add(toBal, amount)
		if err := writeBalance(b, from, fromBal); err != nil {
			return err
		}
		return writeBalance(b, to, toBal)
	})
}

// Seed sets an account balance directly. Used for genesis and testing only.
// Does not enforce any supply constraints.
func (l *Ledger) Seed(account [32]byte, amount *big.Int) error {
	if err := validateUint256(amount, "seed amount"); err != nil {
		return err
	}
	return l.db.Update(func(btx *bolt.Tx) error {
		return writeBalance(btx.Bucket(balancesBucket), account, amount)
	})
}

func readBalance(b *bolt.Bucket, account [32]byte) *big.Int {
	v := b.Get(account[:])
	if v == nil {
		return big.NewInt(0)
	}
	return new(big.Int).SetBytes(v)
}

func writeBalance(b *bolt.Bucket, account [32]byte, bal *big.Int) error {
	if err := validateUint256(bal, "balance"); err != nil {
		return err
	}
	buf := make([]byte, 32)
	balBytes := bal.Bytes()
	if len(balBytes) > 32 {
		return rgerrors.New(rgerrors.ErrArithmeticOverflow, "balance exceeds uint256")
	}
	copy(buf[32-len(balBytes):], balBytes)
	return b.Put(account[:], buf)
}

func validateUint256(v *big.Int, label string) error {
	if v == nil || v.Sign() < 0 {
		return rgerrors.New(rgerrors.ErrArithmeticOverflow, label+" is out of uint256 range")
	}
	if v.Cmp(maxUint256) > 0 {
		return rgerrors.New(rgerrors.ErrArithmeticOverflow, label+" exceeds uint256")
	}
	return nil
}
