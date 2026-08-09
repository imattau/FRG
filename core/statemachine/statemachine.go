package statemachine

import (
	"encoding/binary"
	"math/big"

	"github.com/imattau/frg/core/contract"
	rgerrors "github.com/imattau/frg/core/errors"
	"github.com/imattau/frg/core/gas"
	"github.com/imattau/frg/core/ledger"
	"github.com/imattau/frg/core/mint"
	"github.com/imattau/frg/core/node"
	"github.com/imattau/frg/core/staking"
	"github.com/imattau/frg/core/tree"
	"github.com/imattau/frg/core/tx"
	bolt "go.etcd.io/bbolt"
)

var metaBucket = []byte("meta")
var genesisAppliedKey = []byte("genesisApplied")

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
		if _, err := btx.CreateBucketIfNotExists(metaBucket); err != nil {
			return err
		}
		if _, err := btx.CreateBucketIfNotExists([]byte("contract_bytecode")); err != nil {
			return err
		}
		_, err := btx.CreateBucketIfNotExists([]byte("contract_state"))
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

// CurrentStateRoot returns the state root of the last committed block. Returns zero value before genesis.
func (sm *StateMachine) CurrentStateRoot() ([32]byte, error) {
	var root [32]byte
	err := sm.db.View(func(btx *bolt.Tx) error {
		mb := btx.Bucket(metaBucket)
		if mb == nil {
			return nil
		}
		v := mb.Get([]byte("stateRoot"))
		if v != nil {
			copy(root[:], v)
		}
		return nil
	})
	return root, err
}

// GenesisApplied reports whether immutable genesis initialization completed.
func (sm *StateMachine) GenesisApplied() (bool, error) {
	var applied bool
	err := sm.db.View(func(btx *bolt.Tx) error {
		v := btx.Bucket(metaBucket).Get(genesisAppliedKey)
		applied = len(v) == 1 && v[0] == 1
		return nil
	})
	return applied, err
}

// MarkGenesisAppliedTx records completed genesis initialization atomically.
func (sm *StateMachine) MarkGenesisAppliedTx(btx *bolt.Tx) error {
	return btx.Bucket(metaBucket).Put(genesisAppliedKey, []byte{1})
}

// Update executes fn in the state machine's shared database transaction.
func (sm *StateMachine) Update(fn func(*bolt.Tx) error) error {
	return sm.db.Update(fn)
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

	// Compute base fee and mint amounts (pure math, no state).
	baseFee, err := sm.currentBaseFee()
	if err != nil {
		return nil, err
	}

	totalSupply, totalStaked, err := sm.supplyAndStaked()
	if err != nil {
		return nil, err
	}
	mintAmount := mint.MintPerBlock(totalSupply, totalStaked)

	var stateRoot [32]byte
	totalGas := uint64(0)
	txFuels := make(map[int]uint64) // tx index → fuel consumed (contract txs only)

	// Single atomic write: contracts, transfers, state root, gas, miss-evidence, mint, meta.
	if err := sm.db.Update(func(btx *bolt.Tx) error {
		touchedContracts := make(map[[32]byte]struct{})

		// 0. Apply transactions in proposer order. All signed transaction
		// types consume the sender nonce exactly once.
		for i, t := range b.Txs {
			switch t.Type {
			case tx.TxTypeTransfer:
				if err := sm.ledger.TransferTx(btx, t); err != nil {
					return err
				}
			case tx.TxTypeContractDeploy:
				if err := sm.ledger.AdvanceNonceTx(btx, t.SenderPubKey, t.Nonce); err != nil {
					return err
				}
				gasLimit, err := sm.contractGasLimit(btx, t.SenderPubKey, baseFee)
				if err != nil {
					return err
				}
				_, fuelUsed, err := contract.Deploy(btx, sm.ledger, t, b.Height, gasLimit)
				if err != nil {
					return err
				}
				txFuels[i] = fuelUsed
				addr := contract.ContractAddr(t.SenderPubKey, t.Nonce)
				touchedContracts[addr] = struct{}{}
			case tx.TxTypeContractCall:
				if err := sm.ledger.AdvanceNonceTx(btx, t.SenderPubKey, t.Nonce); err != nil {
					return err
				}
				gasLimit, err := sm.contractGasLimit(btx, t.SenderPubKey, baseFee)
				if err != nil {
					return err
				}
				_, fuelUsed, err := contract.Call(btx, sm.ledger, t, b.Height, gasLimit)
				if err != nil {
					return err
				}
				txFuels[i] = fuelUsed
				touchedContracts[t.ReceiverPubKey] = struct{}{}
			case tx.TxTypeMissEvidence:
				if err := sm.ledger.AdvanceNonceTx(btx, t.SenderPubKey, t.Nonce); err != nil {
					return err
				}
			}
		}

		// 1. Build contract state RG nodes.
		contractNodes := make([]*node.RGNode, 0, len(touchedContracts))
		for addr := range touchedContracts {
			stateRoot := contract.LoadStateRoot(btx, addr)
			bal := sm.ledger.BalanceOfTx(btx, addr)
			sumSquares := new(big.Int).Mul(bal, bal)
			contractNodes = append(contractNodes, &node.RGNode{
				Scale:         1,
				Volume:        node.Uint256ToBytes(new(big.Int).Set(bal)),
				Sig:           node.SigAtomic,
				Children:      [][32]byte{stateRoot},
				SumSquares:    node.Uint256ToBytes(sumSquares),
				Count:         1,
				ContractCount: 1,
			})
		}

		// 2. Build the RG state root from txs + contract state nodes.
		root, err := tree.BuildTreeRoot(b.Txs, contractNodes)
		if err != nil {
			return err
		}
		stateRoot = root

		// 3. Charge gas per tx: base gas (1 unit) + contract compute gas (fuel/FuelUnitsPerGas).
		//    gas_price = baseFee. fee = gas_used * baseFee.
		for i, t := range b.Txs {
			if baseFee.Sign() == 0 {
				continue
			}
			gasUnits := uint64(1) // base gas per tx
			if fuelUsed, ok := txFuels[i]; ok {
				gasUnits += fuelUsed / contract.FuelUnitsPerGas
			}
			fee := new(big.Int).Mul(baseFee, new(big.Int).SetUint64(gasUnits))
			if err := sm.ledger.BurnTx(btx, t.SenderPubKey, fee); err != nil {
				return err
			}
			totalGas += gasUnits
		}

		// 4. Apply miss-evidence side effects.
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

		// 5. Mint block reward to proposer.
		if mintAmount.Sign() > 0 {
			if err := sm.ledger.CreditTx(btx, b.ProposerPubKey, mintAmount); err != nil {
				return err
			}
		}

		// 6. Persist meta. Base fee adjusts on total gas consumed, not tx count.
		mb := btx.Bucket(metaBucket)
		heightBuf := make([]byte, 8)
		binary.BigEndian.PutUint64(heightBuf, b.Height)
		if err := mb.Put([]byte("height"), heightBuf); err != nil {
			return err
		}
		if err := mb.Put([]byte("stateRoot"), stateRoot[:]); err != nil {
			return err
		}
		nextBaseFee := gas.BaseFee(baseFee, totalGas)
		nextFeeBuf := nextBaseFee.Bytes()
		return mb.Put([]byte("baseFee"), nextFeeBuf)
	}); err != nil {
		return nil, err
	}

	gasBurned := new(big.Int).Mul(baseFee, new(big.Int).SetUint64(totalGas))

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

func (sm *StateMachine) contractGasLimit(btx *bolt.Tx, sender [32]byte, baseFee *big.Int) (uint64, error) {
	if baseFee == nil || baseFee.Sign() <= 0 {
		return 0, nil
	}
	bal := sm.ledger.BalanceOfTx(btx, sender)
	affordableGas := new(big.Int).Div(bal, baseFee)
	if affordableGas.Cmp(big.NewInt(1)) <= 0 {
		return 0, rgerrors.New(rgerrors.ErrInsufficientFunds, "sender cannot afford contract gas")
	}
	if !affordableGas.IsUint64() {
		return ^uint64(0), nil
	}
	return affordableGas.Uint64() - 1, nil
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
