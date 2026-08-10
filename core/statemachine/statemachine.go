package statemachine

import (
	"encoding/binary"
	"errors"
	"math/big"
	"runtime"
	"sync"

	"github.com/imattau/frg/core/contract"
	rgerrors "github.com/imattau/frg/core/errors"
	"github.com/imattau/frg/core/gas"
	"github.com/imattau/frg/core/keys"
	"github.com/imattau/frg/core/leader"
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
var consensusVotePrefix = []byte("consensusVote/")
var consensusStateKey = []byte("consensusState")
var totalSupplyKey = []byte("totalSupply")

// Block is the input to ApplyBlock.
type Block struct {
	Height         uint64
	Txs            []*tx.Tx
	ProposerPubKey [32]byte
	PrevStateRoot  [32]byte
	StateRoot      [32]byte
	ProposalBytes  []byte
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
		mb, err := btx.CreateBucketIfNotExists(metaBucket)
		if err != nil {
			return err
		}
		if mb.Get(totalSupplyKey) == nil {
			if err := mb.Put(totalSupplyKey, nil); err != nil {
				return err
			}
		}
		if _, err := btx.CreateBucketIfNotExists([]byte("contract_bytecode")); err != nil {
			return err
		}
		if _, err := btx.CreateBucketIfNotExists([]byte("contract_state")); err != nil {
			return err
		}
		if _, err := btx.CreateBucketIfNotExists(blocksBucket); err != nil {
			return err
		}
		_, err = btx.CreateBucketIfNotExists(blockTelemetryBucket)
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

func (sm *StateMachine) ContractState(contractAddr [32]byte, key []byte) (exists bool, found bool, value []byte, stateRoot [32]byte, err error) {
	err = sm.db.View(func(btx *bolt.Tx) error {
		exists, found, value, stateRoot = contract.LoadStateValue(btx, contractAddr, key)
		return nil
	})
	return exists, found, value, stateRoot, err
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

// SetTotalSupplyTx records the canonical total token supply inside an existing transaction.
func (sm *StateMachine) SetTotalSupplyTx(btx *bolt.Tx, total *big.Int) error {
	if total == nil || total.Sign() < 0 {
		return rgerrors.New(rgerrors.ErrArithmeticOverflow, "total supply must be non-negative")
	}
	return btx.Bucket(metaBucket).Put(totalSupplyKey, total.Bytes())
}

// CurrentTotalSupply returns the tracked total token supply.
// The second return value is false when supply metadata has not been initialized.
func (sm *StateMachine) CurrentTotalSupply() (*big.Int, bool, error) {
	var total *big.Int
	var tracked bool
	err := sm.db.View(func(btx *bolt.Tx) error {
		v := btx.Bucket(metaBucket).Get(totalSupplyKey)
		if v == nil {
			return nil
		}
		tracked = true
		total = new(big.Int).SetBytes(v)
		return nil
	})
	if total == nil {
		total = big.NewInt(0)
	}
	return total, tracked, err
}

// Update executes fn in the state machine's shared database transaction.
func (sm *StateMachine) Update(fn func(*bolt.Tx) error) error {
	return sm.db.Update(fn)
}

// RecordConsensusVote durably reserves a validator vote slot. It returns true
// only when this is the first vote for (height, round, type), preventing a
// restarted process from signing a conflicting vote for the same slot.
func (sm *StateMachine) RecordConsensusVote(height uint64, round uint32, voteType uint8, blockHash [32]byte) bool {
	key := make([]byte, len(consensusVotePrefix)+8+4+1)
	copy(key, consensusVotePrefix)
	binary.BigEndian.PutUint64(key[len(consensusVotePrefix):], height)
	binary.BigEndian.PutUint32(key[len(consensusVotePrefix)+8:], round)
	key[len(consensusVotePrefix)+12] = voteType
	accepted := false
	_ = sm.db.Update(func(btx *bolt.Tx) error {
		mb := btx.Bucket(metaBucket)
		if existing := mb.Get(key); existing != nil {
			return nil
		}
		if err := mb.Put(key, blockHash[:]); err != nil {
			return err
		}
		accepted = true
		return nil
	})
	return accepted
}

// LoadConsensusState returns the last durable consensus snapshot.
func (sm *StateMachine) LoadConsensusState() ([]byte, error) {
	var state []byte
	err := sm.db.View(func(btx *bolt.Tx) error {
		v := btx.Bucket(metaBucket).Get(consensusStateKey)
		state = append([]byte(nil), v...)
		return nil
	})
	return state, err
}

// SaveConsensusState replaces the durable consensus snapshot.
func (sm *StateMachine) SaveConsensusState(state []byte) error {
	return sm.db.Update(func(btx *bolt.Tx) error {
		return btx.Bucket(metaBucket).Put(consensusStateKey, state)
	})
}

// ClearConsensusState removes a snapshot after a committed height advances.
func (sm *StateMachine) ClearConsensusState() error {
	return sm.db.Update(func(btx *bolt.Tx) error {
		return btx.Bucket(metaBucket).Delete(consensusStateKey)
	})
}

// ApplyBlock applies a mainnet-domain block to the state machine.
func (sm *StateMachine) ApplyBlock(b *Block) (*Result, error) {
	return sm.ApplyBlockForChain(b, tx.DefaultChainID)
}

// verifyTxSigsParallel runs VerifySigsForChain across txs on a worker pool.
// Each call only reads its own *Tx and returns a value -- no shared state,
// no mutation -- so splitting the work across goroutines is safe and the
// pass/fail outcome (a block is invalid iff any single tx has a bad
// signature) is unaffected by scheduling order. Profiling a full 65536-tx
// block found this serial loop cost ~58.7us/tx and accounted for ~90% of
// total block-apply time (3.85s of ~4.25s), dwarfing the atomic bolt
// transaction that follows it (~6us/tx combined for tx-apply, RG tree
// build, and gas/mint/meta/persist) -- by far the highest-leverage part of
// ApplyBlockForChain to parallelize.
func verifyTxSigsParallel(txs []*tx.Tx, chainID string) error {
	if len(txs) == 0 {
		return nil
	}
	workers := runtime.NumCPU()
	if workers > len(txs) {
		workers = len(txs)
	}
	if workers <= 1 {
		for _, t := range txs {
			if err := t.VerifySigsForChain(chainID); err != nil {
				return err
			}
		}
		return nil
	}

	chunk := (len(txs) + workers - 1) / workers
	errCh := make(chan error, workers)
	var wg sync.WaitGroup
	for start := 0; start < len(txs); start += chunk {
		end := start + chunk
		if end > len(txs) {
			end = len(txs)
		}
		wg.Add(1)
		go func(slice []*tx.Tx) {
			defer wg.Done()
			for _, t := range slice {
				if err := t.VerifySigsForChain(chainID); err != nil {
					errCh <- err
					return
				}
			}
		}(txs[start:end])
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			return err
		}
	}
	return nil
}

// isSequenceFault reports whether err is ErrSequenceFault (a tx whose nonce
// doesn't match the account's next expected nonce). AdvanceNonceTx and
// TransferTx check-then-write with no partial mutation, so this always fires
// before any state for that specific tx has been touched -- skipping it is
// safe and leaves every other tx in the block (including ones already
// applied earlier in this same pass) untouched. Treating it as fatal instead
// (the previous behavior) meant a single out-of-order tx -- an expected,
// routine occurrence when many independent accounts pipeline transactions
// concurrently -- rolled back and evicted every other transaction in the
// proposal, including from unrelated accounts. Under concurrent multi
// -account load this cascaded: any of those collaterally evicted txs that
// happened to be some account's lowest pending nonce permanently stranded
// that account too, since nothing else would ever resubmit the missing
// nonce, and its still-mempooled higher nonces would trigger the same
// failure in every future proposal that included them. Confirmed live: at
// 128 pooled connections / 512 accounts, this produced 381 failed block
// applies in 63s with height barely advancing. Mirrors the existing
// ErrFunctionNotFound handling below for TxTypeContractCall, which already
// establishes this exact precedent ("must not invalidate the containing
// block or stall height") for a different deterministic per-tx rejection.
func isSequenceFault(err error) bool {
	var rgErr *rgerrors.RGError
	return errors.As(err, &rgErr) && rgErr.Code == rgerrors.ErrSequenceFault
}

// ApplyBlockForChain applies b using signatures bound to chainID. All writes
// are atomic: any error rolls back the entire block and returns the error --
// except ErrSequenceFault and TxTypeContractCall's ErrFunctionNotFound,
// which are deterministic per-tx rejections that skip just that tx instead
// (see isSequenceFault).
func (sm *StateMachine) ApplyBlockForChain(b *Block, chainID string) (*Result, error) {
	cur, err := sm.CurrentHeight()
	if err != nil {
		return nil, err
	}
	if b.Height != cur+1 {
		return nil, rgerrors.Newf(rgerrors.ErrBlockHeightSequenceFault,
			"expected height %d, got %d", cur+1, b.Height)
	}
	previousRoot, err := sm.CurrentStateRoot()
	if err != nil {
		return nil, err
	}
	if b.PrevStateRoot != [32]byte{} {
		if previousRoot != b.PrevStateRoot {
			return nil, rgerrors.Newf(rgerrors.ErrBlockHeightSequenceFault,
				"block parent state root does not match current state")
		}
	}

	// Stateless validate all txs before touching state.
	if err := verifyTxSigsParallel(b.Txs, chainID); err != nil {
		return nil, err
	}

	// Compute base fee and mint amounts (pure math, no state).
	baseFee, err := sm.currentBaseFee()
	if err != nil {
		return nil, err
	}

	validators, _, totalSupply, totalStaked, supplyTracked, err := sm.supplyAndStaked()
	if err != nil {
		return nil, err
	}
	seenMissEvidence := make(map[string]struct{})
	for _, t := range b.Txs {
		if t.Type == tx.TxTypeMissEvidence {
			if err := validateMissEvidenceTx(t, b.Height, previousRoot, validators); err != nil {
				return nil, err
			}
			key := missEvidenceKey(t)
			if _, ok := seenMissEvidence[key]; ok {
				return nil, rgerrors.New(rgerrors.ErrCanonicalEncodingDistortion, "duplicate miss evidence")
			}
			seenMissEvidence[key] = struct{}{}
		}
	}
	mintAmount := mint.MintPerBlock(totalSupply, totalStaked)
	if len(validators) == 0 {
		mintAmount = big.NewInt(0)
	}
	mintShares := mint.SplitReward(mintAmount, len(validators))

	var stateRoot [32]byte
	totalGas := uint64(0)
	totalSlashed := big.NewInt(0)
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
					if isSequenceFault(err) {
						continue
					}
					return err
				}
			case tx.TxTypeContractDeploy:
				if err := sm.ledger.AdvanceNonceTx(btx, t.SenderPubKey, t.Nonce); err != nil {
					if isSequenceFault(err) {
						continue
					}
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
					if isSequenceFault(err) {
						continue
					}
					return err
				}
				gasLimit, err := sm.contractGasLimit(btx, t.SenderPubKey, baseFee)
				if err != nil {
					return err
				}
				_, fuelUsed, err := contract.Call(btx, sm.ledger, t, b.Height, gasLimit)
				if err != nil {
					// An unknown selector is a deterministic transaction rejection.
					// It must not invalidate the containing block or stall height.
					if errors.Is(err, contract.ErrFunctionNotFound) {
						continue
					}
					return err
				}
				txFuels[i] = fuelUsed
				touchedContracts[t.ReceiverPubKey] = struct{}{}
			case tx.TxTypeMissEvidence:
				if err := sm.ledger.AdvanceNonceTx(btx, t.SenderPubKey, t.Nonce); err != nil {
					if isSequenceFault(err) {
						continue
					}
					return err
				}
			case tx.TxTypeBond:
				if err := validateBondTx(t); err != nil {
					return err
				}
				if err := sm.ledger.AdvanceNonceTx(btx, t.SenderPubKey, t.Nonce); err != nil {
					if isSequenceFault(err) {
						continue
					}
					return err
				}
				if err := sm.staking.BondTx(btx, t.SenderPubKey, t.Value, b.Height); err != nil {
					return err
				}
			case tx.TxTypeUnbond:
				if err := validateZeroValueSelfTx(t); err != nil {
					return err
				}
				if err := sm.ledger.AdvanceNonceTx(btx, t.SenderPubKey, t.Nonce); err != nil {
					if isSequenceFault(err) {
						continue
					}
					return err
				}
				if err := sm.staking.UnbondTx(btx, t.SenderPubKey, b.Height); err != nil {
					return err
				}
			case tx.TxTypeFinalizeUnbond:
				if err := validateZeroValueSelfTx(t); err != nil {
					return err
				}
				if err := sm.ledger.AdvanceNonceTx(btx, t.SenderPubKey, t.Nonce); err != nil {
					if isSequenceFault(err) {
						continue
					}
					return err
				}
				if err := sm.staking.FinalizeTx(btx, t.SenderPubKey, b.Height); err != nil {
					return err
				}
			case tx.TxTypeClaimRewards:
				if err := validateZeroValueSelfTx(t); err != nil {
					return err
				}
				if err := sm.ledger.AdvanceNonceTx(btx, t.SenderPubKey, t.Nonce); err != nil {
					if isSequenceFault(err) {
						continue
					}
					return err
				}
				if err := gas.ClaimTx(btx, sm.ledger, t.SenderPubKey); err != nil {
					return err
				}
			case tx.TxTypeEquivEvidence:
				if err := validateZeroValueTx(t); err != nil {
					return err
				}
				if err := sm.ledger.AdvanceNonceTx(btx, t.SenderPubKey, t.Nonce); err != nil {
					if isSequenceFault(err) {
						continue
					}
					return err
				}
				validator, err := validateEquivocationEvidenceTx(t, chainID)
				if err != nil {
					return err
				}
				slashAmt, err := sm.staking.SlashTx(btx, validator)
				if err != nil {
					return err
				}
				totalSlashed.Add(totalSlashed, slashAmt)
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
		rgTree, err := tree.BuildTree(b.Txs, contractNodes)
		if err != nil {
			return err
		}
		root := rgTree.Root()
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
					totalSlashed.Add(totalSlashed, slashAmt)
				}
			}
		}

		// 5. Mint block rewards into validator reward accounts.
		for i, share := range mintShares {
			if share.Sign() == 0 {
				continue
			}
			rewardAccount := gas.FeeAccount(validators[i])
			if err := sm.ledger.CreditTx(btx, rewardAccount, share); err != nil {
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
		if err := mb.Put([]byte("baseFee"), nextFeeBuf); err != nil {
			return err
		}
		if supplyTracked {
			nextSupply := new(big.Int).Set(totalSupply)
			nextSupply.Sub(nextSupply, new(big.Int).Mul(baseFee, new(big.Int).SetUint64(totalGas)))
			nextSupply.Sub(nextSupply, totalSlashed)
			nextSupply.Add(nextSupply, mintAmount)
			if err := sm.SetTotalSupplyTx(btx, nextSupply); err != nil {
				return err
			}
		}
		if b.StateRoot != [32]byte{} && b.StateRoot != stateRoot {
			return rgerrors.Newf(rgerrors.ErrRootMismatch,
				"committed block state root does not match computed state root")
		}
		b.StateRoot = stateRoot
		telemetry := buildBlockTelemetryFromTree(b, rgTree, true)
		if err := putBlockTelemetryTx(btx, telemetry); err != nil {
			return err
		}
		return putBlockTx(btx, b)
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

func (sm *StateMachine) supplyAndStaked() ([][32]byte, []*big.Int, *big.Int, *big.Int, bool, error) {
	validators, amounts, err := sm.staking.BondedAmounts()
	if err != nil {
		return nil, nil, nil, nil, false, err
	}
	totalStaked := new(big.Int)
	for _, a := range amounts {
		totalStaked.Add(totalStaked, a)
	}

	totalSupply, tracked, err := sm.CurrentTotalSupply()
	if err != nil {
		return nil, nil, nil, nil, false, err
	}
	if !tracked {
		totalSupply = big.NewInt(0)
	}
	return validators, amounts, totalSupply, totalStaked, tracked, nil
}

func validateMissEvidenceTx(t *tx.Tx, blockHeight uint64, prevStateRoot [32]byte, validators [][32]byte) error {
	if t.Value == nil || t.Value.Sign() != 0 {
		return rgerrors.New(rgerrors.ErrCanonicalEncodingDistortion, "miss evidence value must be zero")
	}
	if t.MissedHeight != blockHeight {
		return rgerrors.New(rgerrors.ErrCanonicalEncodingDistortion, "miss evidence height must match block height")
	}
	if t.ReceiverPubKey != t.MissedProposer {
		return rgerrors.New(rgerrors.ErrCanonicalEncodingDistortion, "miss evidence receiver must match missed proposer")
	}
	if t.SkipIndex == ^uint32(0) {
		return rgerrors.New(rgerrors.ErrCanonicalEncodingDistortion, "miss evidence skip index overflows")
	}
	expectedMissed, err := leader.SkipProposer(prevStateRoot, blockHeight, validators, t.SkipIndex)
	if err != nil {
		return err
	}
	if t.MissedProposer != expectedMissed {
		return rgerrors.New(rgerrors.ErrInvalidSignature, "miss evidence targets unexpected proposer")
	}
	expectedReporter, err := leader.SkipProposer(prevStateRoot, blockHeight, validators, t.SkipIndex+1)
	if err != nil {
		return err
	}
	if t.SenderPubKey != expectedReporter {
		return rgerrors.New(rgerrors.ErrInvalidSignature, "miss evidence reporter is not next proposer")
	}
	return nil
}

func missEvidenceKey(t *tx.Tx) string {
	buf := make([]byte, 8+4+32)
	binary.BigEndian.PutUint64(buf[:8], t.MissedHeight)
	binary.BigEndian.PutUint32(buf[8:12], t.SkipIndex)
	copy(buf[12:], t.MissedProposer[:])
	return string(buf)
}

func validateBondTx(t *tx.Tx) error {
	if t.Value == nil || t.Value.Sign() <= 0 {
		return rgerrors.New(rgerrors.ErrCanonicalEncodingDistortion, "bond value must be positive")
	}
	if t.ReceiverPubKey != t.SenderPubKey {
		return rgerrors.New(rgerrors.ErrCanonicalEncodingDistortion, "bond receiver must match validator pubkey")
	}
	if t.MissedHeight != 0 || t.MissedProposer != [32]byte{} || t.SkipIndex != 0 {
		return rgerrors.New(rgerrors.ErrCanonicalEncodingDistortion, "bond transaction contains non-zero evidence fields")
	}
	return nil
}

func validateZeroValueSelfTx(t *tx.Tx) error {
	if err := validateZeroValueTx(t); err != nil {
		return err
	}
	if t.ReceiverPubKey != [32]byte{} && t.ReceiverPubKey != t.SenderPubKey {
		return rgerrors.New(rgerrors.ErrCanonicalEncodingDistortion, "receiver pubkey must be empty or sender pubkey")
	}
	if t.MissedHeight != 0 || t.MissedProposer != [32]byte{} || t.SkipIndex != 0 {
		return rgerrors.New(rgerrors.ErrCanonicalEncodingDistortion, "transaction contains non-zero evidence fields")
	}
	if len(t.WasmBytes) != 0 || len(t.InitArgs) != 0 || len(t.CallData) != 0 || len(t.EvidenceA) != 0 || len(t.EvidenceB) != 0 {
		return rgerrors.New(rgerrors.ErrCanonicalEncodingDistortion, "transaction contains unused payload fields")
	}
	return nil
}

func validateZeroValueTx(t *tx.Tx) error {
	if t.Value == nil || t.Value.Sign() != 0 {
		return rgerrors.New(rgerrors.ErrCanonicalEncodingDistortion, "transaction value must be zero")
	}
	return nil
}

func validateEquivocationEvidenceTx(t *tx.Tx, chainID string) ([32]byte, error) {
	var empty [32]byte
	if t.MissedHeight != 0 || t.MissedProposer != [32]byte{} || t.SkipIndex != 0 {
		return empty, rgerrors.New(rgerrors.ErrCanonicalEncodingDistortion, "equivocation transaction contains non-zero miss fields")
	}
	if len(t.WasmBytes) != 0 || len(t.InitArgs) != 0 || len(t.CallData) != 0 {
		return empty, rgerrors.New(rgerrors.ErrCanonicalEncodingDistortion, "equivocation transaction contains unused payload fields")
	}
	a, err := deserializeEvidenceVote(t.EvidenceA)
	if err != nil {
		return empty, rgerrors.Newf(rgerrors.ErrCanonicalEncodingDistortion, "invalid evidence A: %v", err)
	}
	b, err := deserializeEvidenceVote(t.EvidenceB)
	if err != nil {
		return empty, rgerrors.Newf(rgerrors.ErrCanonicalEncodingDistortion, "invalid evidence B: %v", err)
	}
	if !verifyEvidenceVote(a, chainID) || !verifyEvidenceVote(b, chainID) {
		return empty, rgerrors.New(rgerrors.ErrInvalidSignature, "invalid equivocation vote signature")
	}
	if a.ValidatorPK != b.ValidatorPK {
		return empty, rgerrors.New(rgerrors.ErrCanonicalEncodingDistortion, "equivocation votes must have same validator")
	}
	if a.Type != b.Type || a.Height != b.Height || a.Round != b.Round {
		return empty, rgerrors.New(rgerrors.ErrCanonicalEncodingDistortion, "equivocation votes must target same slot")
	}
	if a.BlockHash == b.BlockHash {
		return empty, rgerrors.New(rgerrors.ErrCanonicalEncodingDistortion, "equivocation votes must conflict")
	}
	if a.BlockHash == [32]byte{} || b.BlockHash == [32]byte{} {
		return empty, rgerrors.New(rgerrors.ErrCanonicalEncodingDistortion, "nil votes cannot prove equivocation")
	}
	return a.ValidatorPK, nil
}

type evidenceVote struct {
	Type        uint8
	Height      uint64
	Round       uint32
	BlockHash   [32]byte
	ValidatorPK [32]byte
	Sig         [64]byte
}

func deserializeEvidenceVote(data []byte) (*evidenceVote, error) {
	if len(data) != 141 {
		return nil, rgerrors.Newf(rgerrors.ErrCanonicalEncodingDistortion, "invalid vote length %d", len(data))
	}
	v := &evidenceVote{Type: data[0]}
	v.Height = binary.BigEndian.Uint64(data[1:9])
	v.Round = binary.BigEndian.Uint32(data[9:13])
	copy(v.BlockHash[:], data[13:45])
	copy(v.ValidatorPK[:], data[45:77])
	copy(v.Sig[:], data[77:141])
	return v, nil
}

func verifyEvidenceVote(v *evidenceVote, chainID string) bool {
	if v == nil || (v.Type != 1 && v.Type != 2) {
		return false
	}
	if chainID == "" {
		chainID = tx.DefaultChainID
	}
	buf := make([]byte, 12+2+len(chainID)+1+8+4+32)
	copy(buf[0:12], "FRG_VOTE_V1\x00")
	binary.BigEndian.PutUint16(buf[12:14], uint16(len(chainID)))
	copy(buf[14:14+len(chainID)], chainID)
	off := 14 + len(chainID)
	buf[off] = v.Type
	off++
	binary.BigEndian.PutUint64(buf[off:off+8], v.Height)
	off += 8
	binary.BigEndian.PutUint32(buf[off:off+4], v.Round)
	off += 4
	copy(buf[off:off+32], v.BlockHash[:])
	return keys.Verify(v.ValidatorPK, buf, v.Sig)
}
