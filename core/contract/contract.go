package contract

import (
	"errors"
	"fmt"

	"github.com/imattau/frg/core/hash"
	"github.com/imattau/frg/core/ledger"
	"github.com/imattau/frg/core/tx"
	bolt "go.etcd.io/bbolt"
)

// ErrFunctionNotFound identifies a contract call whose four-byte selector
// does not name an exported function. The state machine treats this as a
// rejected transaction rather than a block-execution failure.
var ErrFunctionNotFound = errors.New("contract function not found")

var (
	bytecodeBucket = []byte("contract_bytecode")
	stateBucket    = []byte("contract_state")
)

func ContractAddr(deployerPubKey [32]byte, nonce uint64) [32]byte {
	state := make([]byte, 0, 56)
	state = append(state, []byte(hash.DomainContractDeploy)...)
	state = append(state, deployerPubKey[:]...)
	nonceBytes := make([]byte, 8)
	putUint64BE(nonceBytes, nonce)
	state = append(state, nonceBytes...)
	return hash.Hash(state)
}

func Deploy(btx *bolt.Tx, l *ledger.Ledger, t *tx.Tx, blockHeight uint64, gasLimit uint64) ([32]byte, uint64, error) {
	contractAddr := ContractAddr(t.SenderPubKey, t.Nonce)

	if len(t.WasmBytes) == 0 {
		return [32]byte{}, 0, fmt.Errorf("contract deploy requires wasm bytes")
	}

	if exists, _ := contractExists(btx, contractAddr); exists {
		return [32]byte{}, 0, fmt.Errorf("contract already deployed at %x", contractAddr)
	}

	if t.Value != nil && t.Value.Sign() > 0 {
		if err := l.MoveTx(btx, t.SenderPubKey, contractAddr, t.Value); err != nil {
			return [32]byte{}, 0, fmt.Errorf("endow contract: %w", err)
		}
	}

	state := loadState(btx, contractAddr)

	cfg := &RuntimeConfig{
		WasmBytes:   t.WasmBytes,
		Caller:      t.SenderPubKey,
		SelfAddr:    contractAddr,
		Value:       t.Value,
		BlockHeight: blockHeight,
		State:       state,
		Ledger:      l,
		BoltTx:      btx,
		GasLimit:    gasLimit,
	}
	rt, err := NewRuntime(cfg)
	if err != nil {
		return [32]byte{}, 0, err
	}

	if _, err := rt.Call("init"); err != nil {
		return [32]byte{}, 0, err
	}

	if err := storeBytecode(btx, contractAddr, t.WasmBytes); err != nil {
		return [32]byte{}, 0, err
	}

	saveState(btx, contractAddr, state)

	return state.StateRoot(), rt.FuelConsumed(), nil
}

func Call(btx *bolt.Tx, l *ledger.Ledger, t *tx.Tx, blockHeight uint64, gasLimit uint64) ([32]byte, uint64, error) {
	contractAddr := t.ReceiverPubKey

	wasmBytes, err := loadBytecode(btx, contractAddr)
	if err != nil {
		return [32]byte{}, 0, fmt.Errorf("contract %x not found", contractAddr)
	}

	state := loadState(btx, contractAddr)
	// The selector chooses the exported function; the remaining bytes are the
	// contract payload exposed through frg.calldata_len/calldata_copy.
	funcName := "call"
	callData := t.CallData
	if len(t.CallData) >= 4 {
		funcName = string(t.CallData[:4])
		callData = t.CallData[4:]
	}

	cfg := &RuntimeConfig{
		WasmBytes:   wasmBytes,
		Caller:      t.SenderPubKey,
		SelfAddr:    contractAddr,
		Value:       t.Value,
		BlockHeight: blockHeight,
		CallData:    append([]byte(nil), callData...),
		State:       state,
		Ledger:      l,
		BoltTx:      btx,
		GasLimit:    gasLimit,
	}
	rt, err := NewRuntime(cfg)
	if err != nil {
		return [32]byte{}, 0, err
	}
	if rt.instance.GetFunc(rt.store, funcName) == nil {
		return [32]byte{}, 0, fmt.Errorf("%w: %q", ErrFunctionNotFound, funcName)
	}

	if t.Value != nil && t.Value.Sign() > 0 {
		if err := l.MoveTx(btx, t.SenderPubKey, contractAddr, t.Value); err != nil {
			return [32]byte{}, 0, fmt.Errorf("fund contract call: %w", err)
		}
	}

	if _, err := rt.Call(funcName); err != nil {
		return [32]byte{}, 0, err
	}

	saveState(btx, contractAddr, state)

	return state.StateRoot(), rt.FuelConsumed(), nil
}

func EnsureBuckets(btx *bolt.Tx) error {
	if _, err := btx.CreateBucketIfNotExists(bytecodeBucket); err != nil {
		return err
	}
	_, err := btx.CreateBucketIfNotExists(stateBucket)
	return err
}

func storeBytecode(btx *bolt.Tx, addr [32]byte, wasm []byte) error {
	return btx.Bucket(bytecodeBucket).Put(addr[:], wasm)
}

func loadBytecode(btx *bolt.Tx, addr [32]byte) ([]byte, error) {
	data := btx.Bucket(bytecodeBucket).Get(addr[:])
	if data == nil {
		return nil, fmt.Errorf("no bytecode for %x", addr)
	}
	out := make([]byte, len(data))
	copy(out, data)
	return out, nil
}

func saveState(btx *bolt.Tx, addr [32]byte, s *StateStore) {
	data := s.Serialize()
	if data == nil {
		data = []byte{}
	}
	_ = btx.Bucket(stateBucket).Put(addr[:], data)
}

func loadState(btx *bolt.Tx, addr [32]byte) *StateStore {
	data := btx.Bucket(stateBucket).Get(addr[:])
	if data == nil {
		return NewStateStore()
	}
	return DeserializeState(data)
}

// LoadStateRoot computes the state root from persisted state without instantiating WASM.
func LoadStateRoot(btx *bolt.Tx, addr [32]byte) [32]byte {
	return loadState(btx, addr).StateRoot()
}

func LoadStateValue(btx *bolt.Tx, addr [32]byte, key []byte) (exists bool, found bool, value []byte, stateRoot [32]byte) {
	if !IsContract(btx, addr) {
		return false, false, nil, [32]byte{}
	}
	state := loadState(btx, addr)
	stateRoot = state.StateRoot()
	if len(key) == 0 {
		return true, false, nil, stateRoot
	}
	value, found = state.Get(key)
	return true, found, value, stateRoot
}

func IsContract(btx *bolt.Tx, addr [32]byte) bool {
	b := btx.Bucket(bytecodeBucket)
	return b != nil && b.Get(addr[:]) != nil
}

func contractExists(btx *bolt.Tx, addr [32]byte) (bool, error) {
	return btx.Bucket(bytecodeBucket).Get(addr[:]) != nil, nil
}

func putUint64BE(b []byte, v uint64) {
	b[0] = byte(v >> 56)
	b[1] = byte(v >> 48)
	b[2] = byte(v >> 40)
	b[3] = byte(v >> 32)
	b[4] = byte(v >> 24)
	b[5] = byte(v >> 16)
	b[6] = byte(v >> 8)
	b[7] = byte(v)
}
