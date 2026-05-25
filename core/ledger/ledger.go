package ledger

import (
	"encoding/binary"
	"math/big"

	rgerrors "github.com/imattau/frg/core/errors"
	"github.com/imattau/frg/core/tx"
	bolt "go.etcd.io/bbolt"
)

var balancesBucket = []byte("balances")
var noncesBucket = []byte("nonces")
var maxUint256 = new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))

// Ledger is a bbolt-backed balance store. Keys are 32-byte Ed25519 public keys;
// values are 32-byte big-endian uint256 quanta balances.
type Ledger struct {
	db *bolt.DB
}

// New creates a Ledger using an existing shared *bolt.DB.
// The caller owns the DB lifecycle.
func New(db *bolt.DB) (*Ledger, error) {
	if err := db.Update(func(btx *bolt.Tx) error {
		if _, err := btx.CreateBucketIfNotExists(balancesBucket); err != nil {
			return err
		}
		_, err := btx.CreateBucketIfNotExists(noncesBucket)
		return err
	}); err != nil {
		return nil, err
	}
	return &Ledger{db: db}, nil
}

// Open opens or creates a ledger database at path.
func Open(path string) (*Ledger, error) {
	db, err := bolt.Open(path, 0600, nil)
	if err != nil {
		return nil, err
	}
	l, err := New(db)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	return l, nil
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

// NonceOf returns the last accepted nonce for account.
// Returns 0 for unknown accounts (no error).
func (l *Ledger) NonceOf(account [32]byte) (uint64, error) {
	var nonce uint64
	err := l.db.View(func(btx *bolt.Tx) error {
		v := btx.Bucket(noncesBucket).Get(account[:])
		if v != nil {
			nonce = binary.BigEndian.Uint64(v)
		}
		return nil
	})
	return nonce, err
}

// Transfer applies a validated Tx: verifies 2-of-2 sigs, checks sender has
// sufficient balance, debits SenderPubKey, credits ReceiverPubKey atomically.
// Returns ERR_012 on invalid sig, ERR_013 if sender balance < tx.Value,
// ERR_018 on sequence fault, ERR_001 on arithmetic overflow.
func (l *Ledger) Transfer(t *tx.Tx) error {
	if err := t.VerifySigs(); err != nil {
		return err
	}
	if err := validateUint256(t.Value, "transfer value"); err != nil {
		return err
	}
	return l.db.Update(func(btx *bolt.Tx) error {
		return l.TransferTx(btx, t)
	})
}

// TransferTx applies a transaction within an existing bolt transaction.
// Caller is responsible for signature verification.
func (l *Ledger) TransferTx(btx *bolt.Tx, t *tx.Tx) error {
	nb := btx.Bucket(noncesBucket)
	var storedNonce uint64
	v := nb.Get(t.SenderPubKey[:])
	if v != nil {
		storedNonce = binary.BigEndian.Uint64(v)
	}

	if t.Nonce != storedNonce+1 {
		return rgerrors.Newf(rgerrors.ErrSequenceFault, "expected nonce %d, got %d", storedNonce+1, t.Nonce)
	}

	bb := btx.Bucket(balancesBucket)
	senderBal := readBalance(bb, t.SenderPubKey)
	if senderBal.Cmp(t.Value) < 0 {
		return rgerrors.New(rgerrors.ErrInsufficientFunds, "sender balance insufficient")
	}
	senderBal.Sub(senderBal, t.Value)
	receiverBal := readBalance(bb, t.ReceiverPubKey)
	receiverBal.Add(receiverBal, t.Value)

	if err := writeBalance(bb, t.SenderPubKey, senderBal); err != nil {
		return err
	}
	if err := writeBalance(bb, t.ReceiverPubKey, receiverBal); err != nil {
		return err
	}

	nonceBuf := make([]byte, 8)
	binary.BigEndian.PutUint64(nonceBuf, t.Nonce)
	return nb.Put(t.SenderPubKey[:], nonceBuf)
}

// Burn destroys amount quanta from account's balance.
// Returns ERR_013 if balance < amount.
func (l *Ledger) Burn(account [32]byte, amount *big.Int) error {
	if err := validateUint256(amount, "burn amount"); err != nil {
		return err
	}
	return l.db.Update(func(btx *bolt.Tx) error {
		return l.BurnTx(btx, account, amount)
	})
}

// BurnTx destroys quanta within an existing bolt transaction.
func (l *Ledger) BurnTx(btx *bolt.Tx, account [32]byte, amount *big.Int) error {
	b := btx.Bucket(balancesBucket)
	bal := readBalance(b, account)
	if bal.Cmp(amount) < 0 {
		return rgerrors.New(rgerrors.ErrInsufficientFunds, "insufficient balance to burn")
	}
	bal.Sub(bal, amount)
	return writeBalance(b, account, bal)
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
		return l.SeedTx(btx, account, amount)
	})
}

// SeedTx sets an account balance within an existing bolt transaction.
func (l *Ledger) SeedTx(btx *bolt.Tx, account [32]byte, amount *big.Int) error {
	return writeBalance(btx.Bucket(balancesBucket), account, amount)
}

// CreditTx adds amount to account's existing balance within an existing bolt transaction.
func (l *Ledger) CreditTx(btx *bolt.Tx, account [32]byte, amount *big.Int) error {
	b := btx.Bucket(balancesBucket)
	cur := readBalance(b, account)
	next := new(big.Int).Add(cur, amount)
	return writeBalance(b, account, next)
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
