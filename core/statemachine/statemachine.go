package statemachine

import (
	"encoding/binary"
	"math/big"

	rgerrors "github.com/imattau/frg/core/errors"
	"github.com/imattau/frg/core/gas"
	"github.com/imattau/frg/core/ledger"
	"github.com/imattau/frg/core/mint"
	"github.com/imattau/frg/core/staking"
	"github.com/imattau/frg/core/tree"
	"github.com/imattau/frg/core/tx"
	bolt "go.etcd.io/bbolt"
)

var metaBucket = []byte("meta")

// Block is the input to ApplyBlock.
type Block struct {
	Height         uint64
	Txs            []*tx.Tx
	ProposerPubKey [32]byte
}

// Result is returned by a successful ApplyBlock.
type Result struct {
	StateRoot  [32]byte
	Height     uint64
	TxsApplied int
	GasBurned  *big.Int
	MintAmount *big.Int
}

// StateMachine applies finalised blocks deterministically.
type StateMachine struct {
	db      *bolt.DB
	ledger  *ledger.Ledger
	staking *staking.Store
}

// New creates a StateMachine. db must be a shared bbolt database already opened
// and used to construct l and s.
func New(db *bolt.DB, l *ledger.Ledger, s *staking.Store) (*StateMachine, error) {
	if err := db.Update(func(btx *bolt.Tx) error {
		_, err := btx.CreateBucketIfNotExists(metaBucket)
		return err
	}); err != nil {
		return nil, err
	}
	return &StateMachine{db: db, ledger: l, staking: s}, nil
}

// CurrentHeight returns the last committed block height. Returns 0 before genesis.
func (sm *StateMachine) CurrentHeight() (uint64, error) {
	var h uint64
	err := sm.db.View(func(btx *bolt.Tx) error {
		mb := btx.Bucket(metaBucket)
		if mb == nil {
			return nil
		}
		v := mb.Get([]byte("height"))
		if v != nil {
			h = binary.BigEndian.Uint64(v)
		}
		return nil
	})
	return h, err
}

// ApplyBlock applies b to the state machine. All writes are atomic: any error
// rolls back the entire block and returns the error.
func (sm *StateMachine) ApplyBlock(b *Block) (*Result, error) {
	cur, err := sm.CurrentHeight()
	if err != nil {
		return nil, err
	}
	if b.Height != cur+1 {
		return nil, rgerrors.Newf(rgerrors.ErrBlockHeightSequenceFault,
			"expected height %d, got %d", cur+1, b.Height)
	}

	// Stateless validate all txs before touching state.
	for _, t := range b.Txs {
		if err := t.VerifySigs(); err != nil {
			return nil, err
		}
	}

	// Build RG state root from the tx set (pure, no state writes).
	rgBlock := &tree.Block{Height: b.Height, Txs: b.Txs}
	stateRoot, err := rgBlock.BuildRoot()
	if err != nil {
		return nil, err
	}

	// Compute gas and mint amounts before the transaction (pure math).
	baseFee, err := sm.currentBaseFee()
	if err != nil {
		return nil, err
	}
	gasBurned := new(big.Int)
	for range b.Txs {
		gasBurned.Add(gasBurned, baseFee)
	}

	totalSupply, totalStaked, err := sm.supplyAndStaked()
	if err != nil {
		return nil, err
	}
	mintAmount := mint.MintPerBlock(totalSupply, totalStaked)

	// Single atomic write: transfers, gas burn, miss-evidence, mint, meta.
	if err := sm.db.Update(func(btx *bolt.Tx) error {
		// 1. Apply transfers in proposer order.
		for _, t := range b.Txs {
			if t.Type == tx.TxTypeTransfer {
				if err := sm.ledger.TransferTx(btx, t); err != nil {
					return err
				}
			}
		}

		// 2. Burn gas per tx.
		for _, t := range b.Txs {
			if baseFee.Sign() > 0 {
				if err := sm.ledger.BurnTx(btx, t.SenderPubKey, baseFee); err != nil {
					return err
				}
			}
		}

		// 3. Apply miss-evidence side effects.
		for _, t := range b.Txs {
			if t.Type == tx.TxTypeMissEvidence {
				_, slashAmt, err := sm.staking.RecordMissTx(btx, t.MissedProposer)
				if err != nil {
					return err
				}
				if slashAmt.Sign() > 0 {
					escrow := staking.EscrowAccount(t.MissedProposer)
					if err := sm.ledger.BurnTx(btx, escrow, slashAmt); err != nil {
						return err
					}
				}
			}
		}

		// 4. Mint block reward to proposer.
		if mintAmount.Sign() > 0 {
			if err := sm.ledger.SeedTx(btx, b.ProposerPubKey, mintAmount); err != nil {
				return err
			}
		}

		// 5. Persist meta.
		mb := btx.Bucket(metaBucket)
		heightBuf := make([]byte, 8)
		binary.BigEndian.PutUint64(heightBuf, b.Height)
		if err := mb.Put([]byte("height"), heightBuf); err != nil {
			return err
		}
		if err := mb.Put([]byte("stateRoot"), stateRoot[:]); err != nil {
			return err
		}
		nextBaseFee := gas.BaseFee(baseFee, uint64(len(b.Txs)))
		nextFeeBuf := nextBaseFee.Bytes()
		return mb.Put([]byte("baseFee"), nextFeeBuf)
	}); err != nil {
		return nil, err
	}

	return &Result{
		StateRoot:  stateRoot,
		Height:     b.Height,
		TxsApplied: len(b.Txs),
		GasBurned:  gasBurned,
		MintAmount: mintAmount,
	}, nil
}

func (sm *StateMachine) currentBaseFee() (*big.Int, error) {
	var fee *big.Int
	err := sm.db.View(func(btx *bolt.Tx) error {
		mb := btx.Bucket(metaBucket)
		if mb == nil {
			fee = gas.BaseFee(nil, 0)
			return nil
		}
		v := mb.Get([]byte("baseFee"))
		if v == nil {
			fee = gas.BaseFee(nil, 0)
		} else {
			fee = new(big.Int).SetBytes(v)
		}
		return nil
	})
	return fee, err
}

func (sm *StateMachine) supplyAndStaked() (*big.Int, *big.Int, error) {
	_, amounts, err := sm.staking.BondedAmounts()
	if err != nil {
		return nil, nil, err
	}
	totalStaked := new(big.Int)
	for _, a := range amounts {
		totalStaked.Add(totalStaked, a)
	}
	// Total supply approximation: sum all ledger balances is expensive.
	// Use totalStaked * 2 as a conservative proxy until a supply tracker is added.
	totalSupply := new(big.Int).Mul(totalStaked, big.NewInt(2))
	if totalSupply.Sign() == 0 {
		totalSupply = big.NewInt(1_000_000_000)
	}
	return totalSupply, totalStaked, nil
}
