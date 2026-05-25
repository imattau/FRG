package staking

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"math/big"

	rgerrors "github.com/imattau/frg/core/errors"
	"github.com/imattau/frg/core/keys"
	"github.com/imattau/frg/core/ledger"
	bolt "go.etcd.io/bbolt"
)

const (
	MinBond         = int64(1000)
	UnbondingBlocks = uint64(1000)
)

const (
	stateBonded    = uint8(1)
	stateUnbonding = uint8(2)
)

var validatorsBucket = []byte("validators")
var minBondAmount = big.NewInt(MinBond)

const domainStakingEscrow = "FRG_STAKING_ESCROW_V1\x00"

// EquivocationProof holds two conflicting signed block headers from the same validator.
type EquivocationProof struct {
	ValidatorPubKey [32]byte
	HeaderA         []byte
	SigA            [64]byte
	HeaderB         []byte
	SigB            [64]byte
}

type record struct {
	State            uint8
	BondedAmount     *big.Int
	BondedAtBlock    uint64
	UnbondingAtBlock uint64
}

// Store is a bbolt-backed staking state store.
type Store struct {
	db     *bolt.DB
	ledger *ledger.Ledger
}

// Open opens or creates a staking database at path.
func Open(path string, l *ledger.Ledger) (*Store, error) {
	db, err := bolt.Open(path, 0600, nil)
	if err != nil {
		return nil, err
	}
	if err := db.Update(func(btx *bolt.Tx) error {
		_, err := btx.CreateBucketIfNotExists(validatorsBucket)
		return err
	}); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{db: db, ledger: l}, nil
}

// Close closes the underlying database.
func (s *Store) Close() error {
	return s.db.Close()
}

// Bond locks amount quanta from validator into their escrow account.
func (s *Store) Bond(validator [32]byte, amount *big.Int, currentBlock uint64) error {
	if amount == nil || amount.Cmp(minBondAmount) < 0 {
		return rgerrors.New(rgerrors.ErrBondBelowMinimum, "bond amount below minimum")
	}
	rec, err := s.readRecord(validator)
	if err != nil {
		return err
	}
	if rec != nil {
		return rgerrors.New(rgerrors.ErrAlreadyBonded, "validator already bonded")
	}

	escrow := escrowAccount(validator)
	if err := s.ledger.Move(validator, escrow, amount); err != nil {
		return err
	}

	return s.db.Update(func(btx *bolt.Tx) error {
		return putRecord(btx.Bucket(validatorsBucket), validator, record{
			State:            stateBonded,
			BondedAmount:     new(big.Int).Set(amount),
			BondedAtBlock:    currentBlock,
			UnbondingAtBlock: 0,
		})
	})
}

// Unbond begins the unbonding lockup for validator.
func (s *Store) Unbond(validator [32]byte, currentBlock uint64) error {
	rec, err := s.readRecord(validator)
	if err != nil {
		return err
	}
	if rec == nil || rec.State == 0 {
		return rgerrors.New(rgerrors.ErrNotBonded, "validator not bonded")
	}
	if rec.State == stateUnbonding {
		return rgerrors.New(rgerrors.ErrUnbondingPending, "unbonding already in progress")
	}
	rec.State = stateUnbonding
	rec.UnbondingAtBlock = currentBlock
	return s.db.Update(func(btx *bolt.Tx) error {
		return putRecord(btx.Bucket(validatorsBucket), validator, *rec)
	})
}

// Finalize releases unbonded funds back to validator if lockup has elapsed.
func (s *Store) Finalize(validator [32]byte, currentBlock uint64) error {
	rec, err := s.readRecord(validator)
	if err != nil {
		return err
	}
	if rec == nil || rec.State != stateUnbonding {
		return nil
	}
	if currentBlock < rec.UnbondingAtBlock+UnbondingBlocks {
		return nil
	}

	escrow := escrowAccount(validator)
	if err := s.ledger.Move(escrow, validator, rec.BondedAmount); err != nil {
		return err
	}
	return s.db.Update(func(btx *bolt.Tx) error {
		return btx.Bucket(validatorsBucket).Delete(validator[:])
	})
}

// Slash burns the full escrow balance of validator on equivocation proof.
func (s *Store) Slash(validator [32]byte, proof EquivocationProof) error {
	rec, err := s.readRecord(validator)
	if err != nil {
		return err
	}
	if rec == nil || rec.State != stateBonded {
		return rgerrors.New(rgerrors.ErrNotBonded, "validator not bonded")
	}
	if proof.ValidatorPubKey != validator {
		return rgerrors.New(rgerrors.ErrInvalidSignature, "proof validator mismatch")
	}
	if !keys.Verify(validator, proof.HeaderA, proof.SigA) || !keys.Verify(validator, proof.HeaderB, proof.SigB) {
		return rgerrors.New(rgerrors.ErrInvalidSignature, "invalid equivocation proof signatures")
	}
	if bytes.Equal(proof.HeaderA, proof.HeaderB) {
		return rgerrors.New(rgerrors.ErrCanonicalEncodingDistortion, "equivocation proof headers must differ")
	}

	escrow := escrowAccount(validator)
	if err := s.ledger.Burn(escrow, rec.BondedAmount); err != nil {
		return err
	}
	return s.db.Update(func(btx *bolt.Tx) error {
		return btx.Bucket(validatorsBucket).Delete(validator[:])
	})
}

// ValidatorSet returns all currently active (BONDED, not UNBONDING) validators.
func (s *Store) ValidatorSet() ([][32]byte, error) {
	var out [][32]byte
	err := s.db.View(func(btx *bolt.Tx) error {
		return btx.Bucket(validatorsBucket).ForEach(func(k, v []byte) error {
			rec := decodeRecord(v)
			if rec.State == stateBonded {
				var pub [32]byte
				copy(pub[:], k)
				out = append(out, pub)
			}
			return nil
		})
	})
	return out, err
}

func (s *Store) readRecord(validator [32]byte) (*record, error) {
	var rec *record
	err := s.db.View(func(btx *bolt.Tx) error {
		rec = getRecord(btx.Bucket(validatorsBucket), validator)
		return nil
	})
	return rec, err
}

func escrowAccount(validator [32]byte) [32]byte {
	h := sha256.New()
	h.Write([]byte(domainStakingEscrow))
	h.Write(validator[:])
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

func encodeRecord(r record) []byte {
	buf := make([]byte, 49)
	buf[0] = r.State
	amountBytes := r.BondedAmount.Bytes()
	copy(buf[1+32-len(amountBytes):33], amountBytes)
	binary.BigEndian.PutUint64(buf[33:41], r.BondedAtBlock)
	binary.BigEndian.PutUint64(buf[41:49], r.UnbondingAtBlock)
	return buf
}

func decodeRecord(buf []byte) record {
	return record{
		State:            buf[0],
		BondedAmount:     new(big.Int).SetBytes(buf[1:33]),
		BondedAtBlock:    binary.BigEndian.Uint64(buf[33:41]),
		UnbondingAtBlock: binary.BigEndian.Uint64(buf[41:49]),
	}
}

func getRecord(b *bolt.Bucket, validator [32]byte) *record {
	v := b.Get(validator[:])
	if v == nil {
		return nil
	}
	r := decodeRecord(v)
	return &r
}

func putRecord(b *bolt.Bucket, validator [32]byte, r record) error {
	return b.Put(validator[:], encodeRecord(r))
}
