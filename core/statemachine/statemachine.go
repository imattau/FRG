package statemachine

import (
	"encoding/binary"
	"math/big"

	rgerrors "github.com/imattau/frg/core/errors"
	"github.com/imattau/frg/core/gas"
	"github.com/imattau/frg/core/ledger"
	"github.com/imattau/frg/core/mint"
	"github.com/imattau/frg/core/node"
	"github.com/imattau/frg/core/staking"
	"github.com/imattau/frg/core/tree"
	"github.com/imattau/frg/core/tx"
	"github.com/imattau/frg/core/contract"
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
	transferGas := new(big.Int)
	contractGas := uint64(0)

	// Single atomic write: contracts, transfers, state root, gas, miss-evidence, mint, meta.
	if err := sm.db.Update(func(btx *bolt.Tx) error {
		touchedContracts := make(map[[32]byte]struct{})

		// 0. Execute contract deployments and calls.
		for _, t := range b.Txs {
			switch t.Type {
			case tx.TxTypeContractDeploy:
				_, fuelUsed, err := contract.Deploy(btx, sm.ledger, t, b.Height)
				if err != nil {
					return err
				}
				contractGas += fuelUsed
				addr := contract.ContractAddr(t.SenderPubKey, t.Nonce)
				touchedContracts[addr] = struct{}{}
			case tx.TxTypeContractCall:
				_, fuelUsed, err := contract.Call(btx, sm.ledger, t, b.Height)
				if err != nil {
					return err
				}
				contractGas += fuelUsed
				touchedContracts[t.ReceiverPubKey] = struct{}{}
			}
		}

		// 1. Apply transfers in proposer order.
		for _, t := range b.Txs {
			if t.Type == tx.TxTypeTransfer {
				if err := sm.ledger.TransferTx(btx, t); err != nil {
					return err
				}
			}
		}

		// 2. Build contract state RG nodes from ALL contracts with persisted state.
		contractNodes := make([]*node.RGNode, 0, len(touchedContracts))
		for addr := range touchedContracts {
			stateRoot := contract.LoadStateRoot(btx, addr)
			balance, _ := sm.ledger.BalanceOf(addr)
			bal := big.NewInt(0)
			if balance != nil {
				bal = new(big.Int).Set(balance)
			}
			sumSquares := new(big.Int).Mul(bal, bal)
			contractNodes = append(contractNodes, &node.RGNode{
				Scale:         1,
				Volume:        new(big.Int).Set(bal),
				Variance:      big.NewInt(0),
				Sig:           node.SigAtomic,
				Children:      [][32]byte{stateRoot},
				SumValues:     new(big.Int).Set(bal),
				SumSquares:    sumSquares,
				Count:         1,
				ContractCount: 1,
			})
		}

		// 3. Build the RG state root from txs + contract state nodes.
		root, err := tree.BuildTreeRoot(b.Txs, contractNodes)
		if err != nil {
			return err
		}
		stateRoot = root

		// 4. Burn gas: transfer gas + contract gas.
		for _, t := range b.Txs {
			if t.Type == tx.TxTypeTransfer || t.Type == tx.TxTypeContractDeploy || t.Type == tx.TxTypeContractCall {
				if baseFee.Sign() > 0 {
					if err := sm.ledger.BurnTx(btx, t.SenderPubKey, baseFee); err != nil {
						return err
					}
					transferGas.Add(transferGas, baseFee)
				}
			}
		}

		// Charge contract compute gas: fuel / FuelUnitsPerGas * baseFee (floor).
		if contractGas > 0 {
			gasUnits := contractGas / contract.FuelUnitsPerGas
			if gasUnits > 0 {
				contractFee := new(big.Int).Mul(baseFee, new(big.Int).SetUint64(gasUnits))
				for _, t := range b.Txs {
					if t.Type == tx.TxTypeContractDeploy || t.Type == tx.TxTypeContractCall {
						if err := sm.ledger.BurnTx(btx, t.SenderPubKey, contractFee); err != nil {
							return err
						}
					}
				}
			}
		}

		// 5. Apply miss-evidence side effects.
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

		// 6. Mint block reward to proposer.
		if mintAmount.Sign() > 0 {
			if err := sm.ledger.CreditTx(btx, b.ProposerPubKey, mintAmount); err != nil {
				return err
			}
		}

		// 7. Persist meta.
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

	// Compute total gas burned for result.
	gasBurned := new(big.Int).Set(transferGas)
	if contractGas > 0 {
		gasUnits := contractGas / contract.FuelUnitsPerGas
		contractFee := new(big.Int).Mul(baseFee, new(big.Int).SetUint64(gasUnits))
		gasBurned.Add(gasBurned, contractFee)
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
