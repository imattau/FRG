package contract

import (
	"fmt"

	"github.com/imattau/frg/core/hash"
	"github.com/imattau/frg/core/ledger"
	"github.com/imattau/frg/core/tx"
	bolt "go.etcd.io/bbolt"
)

var (
	bytecodeBucket = []byte("contract_bytecode")
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

func Deploy(btx *bolt.Tx, l *ledger.Ledger, t *tx.Tx, blockHeight uint64) ([32]byte, error) {
	contractAddr := ContractAddr(t.SenderPubKey, t.Nonce)

	if len(t.WasmBytes) == 0 {
		return [32]byte{}, fmt.Errorf("contract deploy requires wasm bytes")
	}

	if exists, _ := contractExists(btx, contractAddr); exists {
		return [32]byte{}, fmt.Errorf("contract already deployed at %x", contractAddr)
	}

	if t.Value != nil && t.Value.Sign() > 0 {
		if err := l.MoveTx(btx, t.SenderPubKey, contractAddr, t.Value); err != nil {
			return [32]byte{}, fmt.Errorf("endow contract: %w", err)
		}
	}

	state := NewStateStore()

	cfg := &RuntimeConfig{
		WasmBytes:   t.WasmBytes,
		Caller:      t.SenderPubKey,
		SelfAddr:    contractAddr,
		Value:       t.Value,
		BlockHeight: blockHeight,
		State:       state,
		Ledger:      l,
		BoltTx:      btx,
	}
	rt, err := NewRuntime(cfg)
	if err != nil {
		return [32]byte{}, err
	}

	if _, err := rt.Call("init"); err != nil {
		return [32]byte{}, err
	}

	if err := storeBytecode(btx, contractAddr, t.WasmBytes); err != nil {
		return [32]byte{}, err
	}

	return state.StateRoot(), nil
}

func Call(btx *bolt.Tx, l *ledger.Ledger, t *tx.Tx, blockHeight uint64) ([32]byte, error) {
	contractAddr := t.ReceiverPubKey

	wasmBytes, err := loadBytecode(btx, contractAddr)
	if err != nil {
		return [32]byte{}, fmt.Errorf("contract %x not found", contractAddr)
	}

	if t.Value != nil && t.Value.Sign() > 0 {
		if err := l.MoveTx(btx, t.SenderPubKey, contractAddr, t.Value); err != nil {
			return [32]byte{}, fmt.Errorf("fund contract call: %w", err)
		}
	}

	state := NewStateStore()

	cfg := &RuntimeConfig{
		WasmBytes:   wasmBytes,
		Caller:      t.SenderPubKey,
		SelfAddr:    contractAddr,
		Value:       t.Value,
		BlockHeight: blockHeight,
		State:       state,
		Ledger:      l,
		BoltTx:      btx,
	}
	rt, err := NewRuntime(cfg)
	if err != nil {
		return [32]byte{}, err
	}

	funcName := "call"
	if len(t.CallData) >= 4 {
		funcName = string(t.CallData[:4])
	}

	if _, err := rt.Call(funcName); err != nil {
		return [32]byte{}, err
	}

	return state.StateRoot(), nil
}

func EnsureBuckets(btx *bolt.Tx) error {
	if _, err := btx.CreateBucketIfNotExists(bytecodeBucket); err != nil {
		return err
	}
	return nil
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
