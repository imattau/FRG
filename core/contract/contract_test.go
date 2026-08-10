package contract_test

import (
	"errors"
	"fmt"
	"math/big"
	"strings"
	"testing"

	"github.com/bytecodealliance/wasmtime-go/v28"
	"github.com/imattau/frg/core/contract"
	rgerrors "github.com/imattau/frg/core/errors"
	"github.com/imattau/frg/core/keys"
	"github.com/imattau/frg/core/ledger"
	"github.com/imattau/frg/core/tx"
	bolt "go.etcd.io/bbolt"
	"golang.org/x/crypto/bn256"
)

// minimalWasm builds a valid WASM module that exports "init" (no-op) and "call" (no-op).
// This is a hand-crafted binary with: 1 type, 1 func, 2 exports, 2 code bodies.
var minimalWasm = []byte{
	0x00, 0x61, 0x73, 0x6D, // magic
	0x01, 0x00, 0x00, 0x00, // version

	// Type section (id=1): 1 type: () -> ()
	0x01,             // section id
	0x04,             // section size (payload: 1 byte count + 3 byte functype)
	0x01,             // type count
	0x60, 0x00, 0x00, // functype: 0 params, 0 results

	// Function section (id=3): 2 functions, both type 0
	0x03,       // section id
	0x03,       // section size
	0x02,       // function count
	0x00, 0x00, // type indices

	// Export section (id=7): export "init" (func 0), "call" (func 1)
	0x07, // section id
	0x0F, // section size (fixed: 1+7+7=15)
	0x02, // export count
	// export "init"
	0x04, 0x69, 0x6E, 0x69, 0x74, // name "init"
	0x00, // func
	0x00, // index 0
	// export "call"
	0x04, 0x63, 0x61, 0x6C, 0x6C, // name "call"
	0x00, // func
	0x01, // index 1

	// Code section (id=10): 2 function bodies
	0x0A, // section id
	0x07, // section size (fixed: 1+3+3=7)
	0x02, // body count
	// body 0
	0x02, 0x00, 0x0B, // size=2, 0 locals, end
	// body 1
	0x02, 0x00, 0x0B, // size=2, 0 locals, end
}

func TestContractDeployRoundTrip(t *testing.T) {
	kp, _ := keys.GenerateKeypair()

	tr := &tx.Tx{
		Type:         tx.TxTypeContractDeploy,
		Sender:       "alice",
		Receiver:     "contract",
		Value:        big.NewInt(0),
		Nonce:        1,
		SenderPubKey: kp.PublicKey,
		WasmBytes:    minimalWasm,
	}
	sig, _ := tr.SignSender(kp)
	tr.SenderSig = sig

	raw, err := tr.Serialize()
	if err != nil {
		t.Fatalf("serialize: %v", err)
	}

	parsed, err := tx.Deserialize(raw)
	if err != nil {
		t.Fatalf("deserialize: %v", err)
	}

	if parsed.Type != tx.TxTypeContractDeploy {
		t.Fatalf("expected type 3, got %d", parsed.Type)
	}
	if len(parsed.WasmBytes) != len(minimalWasm) {
		t.Fatalf("wasm len mismatch: %d vs %d", len(parsed.WasmBytes), len(minimalWasm))
	}
}

func TestContractDeployAndCall(t *testing.T) {
	db, err := bolt.Open(t.TempDir()+"/test.db", 0600, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	l, err := ledger.New(db)
	if err != nil {
		t.Fatal(err)
	}

	// Seed deployer with funds
	kp, _ := keys.GenerateKeypair()
	if err := l.Seed(kp.PublicKey, big.NewInt(10000)); err != nil {
		t.Fatal(err)
	}

	// Create contract buckets
	if err := db.Update(func(btx *bolt.Tx) error {
		_, err := btx.CreateBucketIfNotExists([]byte("contract_bytecode"))
		if err != nil {
			return err
		}
		_, err = btx.CreateBucketIfNotExists([]byte("contract_state"))
		return err
	}); err != nil {
		t.Fatal(err)
	}

	// Deploy
	deployTx := &tx.Tx{
		Type:         tx.TxTypeContractDeploy,
		Sender:       "alice",
		Receiver:     "contract",
		Value:        big.NewInt(0),
		Nonce:        1,
		SenderPubKey: kp.PublicKey,
		WasmBytes:    minimalWasm,
	}
	sig, _ := deployTx.SignSender(kp)
	deployTx.SenderSig = sig

	var stateRoot [32]byte
	if err := db.Update(func(btx *bolt.Tx) error {
		var err error
		stateRoot, _, err = contract.Deploy(btx, l, deployTx, 1, 1000000)
		return err
	}); err != nil {
		t.Fatalf("deploy: %v", err)
	}

	contractAddr := contract.ContractAddr(kp.PublicKey, 1)
	t.Logf("contract addr: %x", contractAddr)

	// Verify bytecode was stored
	if err := db.View(func(btx *bolt.Tx) error {
		data := btx.Bucket([]byte("contract_bytecode")).Get(contractAddr[:])
		if data == nil {
			t.Fatal("bytecode not stored")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// Call
	callTx := &tx.Tx{
		Type:           tx.TxTypeContractCall,
		Sender:         "alice",
		Receiver:       "contract",
		Value:          big.NewInt(0),
		Nonce:          2,
		SenderPubKey:   kp.PublicKey,
		ReceiverPubKey: contractAddr,
	}
	sig2, _ := callTx.SignSender(kp)
	callTx.SenderSig = sig2

	var callRoot [32]byte
	if err := db.Update(func(btx *bolt.Tx) error {
		var err error
		callRoot, _, err = contract.Call(btx, l, callTx, 2, 1000000)
		return err
	}); err != nil {
		t.Fatalf("call: %v", err)
	}

	t.Logf("stateRoot after deploy: %x", stateRoot)
	t.Logf("stateRoot after call:   %x", callRoot)
}

func TestRuntimeConsumesFuel(t *testing.T) {
	db, err := bolt.Open(t.TempDir()+"/test.db", 0600, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	l, err := ledger.New(db)
	if err != nil {
		t.Fatal(err)
	}
	kp, _ := keys.GenerateKeypair()
	if err := l.Seed(kp.PublicKey, big.NewInt(10000)); err != nil {
		t.Fatal(err)
	}

	store := contract.NewStateStore()
	var fuelUsed uint64
	if err := db.Update(func(btx *bolt.Tx) error {
		rt, err := contract.NewRuntime(&contract.RuntimeConfig{
			WasmBytes:   minimalWasm,
			Caller:      kp.PublicKey,
			SelfAddr:    kp.PublicKey,
			Value:       big.NewInt(0),
			BlockHeight: 1,
			State:       store,
			Ledger:      l,
			BoltTx:      btx,
			GasLimit:    1000,
		})
		if err != nil {
			return err
		}
		if _, err := rt.Call("call"); err != nil {
			return err
		}
		fuelUsed = rt.FuelConsumed()
		return nil
	}); err != nil {
		t.Fatalf("runtime call: %v", err)
	}
	if fuelUsed == 0 {
		t.Fatal("expected WASM execution to consume fuel")
	}
}

func TestRuntimeOutOfFuelUsesContractGasError(t *testing.T) {
	loopWasm, err := wasmtime.Wat2Wasm(`
		(module
		  (func (export "call")
		    (loop $again
		      br $again
		    )
		  )
		)
	`)
	if err != nil {
		t.Fatalf("Wat2Wasm: %v", err)
	}

	db, err := bolt.Open(t.TempDir()+"/test.db", 0600, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	l, err := ledger.New(db)
	if err != nil {
		t.Fatal(err)
	}
	kp, _ := keys.GenerateKeypair()
	if err := l.Seed(kp.PublicKey, big.NewInt(10000)); err != nil {
		t.Fatal(err)
	}

	store := contract.NewStateStore()
	if err := db.Update(func(btx *bolt.Tx) error {
		rt, err := contract.NewRuntime(&contract.RuntimeConfig{
			WasmBytes:   loopWasm,
			Caller:      kp.PublicKey,
			SelfAddr:    kp.PublicKey,
			Value:       big.NewInt(0),
			BlockHeight: 1,
			State:       store,
			Ledger:      l,
			BoltTx:      btx,
			GasLimit:    1,
		})
		if err != nil {
			return err
		}
		_, err = rt.Call("call")
		var rgErr *rgerrors.RGError
		if !errors.As(err, &rgErr) || rgErr.Code != rgerrors.ErrContractOutOfGas {
			t.Fatalf("expected %s, got %v", rgerrors.ErrContractOutOfGas, err)
		}
		return nil
	}); err != nil {
		t.Fatalf("runtime call: %v", err)
	}
}

func TestBn254PairingCheck(t *testing.T) {
	g1 := new(bn256.G1).ScalarBaseMult(big.NewInt(1))
	negG1 := new(bn256.G1).Neg(g1)
	g2 := new(bn256.G2).ScalarBaseMult(big.NewInt(1))

	input := append(append([]byte{}, g1.Marshal()...), g2.Marshal()...)
	input = append(input, negG1.Marshal()...)
	input = append(input, g2.Marshal()...)

	ok, err := contract.Bn254PairingCheck(input)
	if err != nil {
		t.Fatalf("pairing check: %v", err)
	}
	if !ok {
		t.Fatal("expected pairing product to equal one")
	}

	ok, err = contract.Bn254PairingCheck(append(append([]byte{}, g1.Marshal()...), g2.Marshal()...))
	if err != nil {
		t.Fatalf("single pairing check: %v", err)
	}
	if ok {
		t.Fatal("expected single generator pairing not to equal one")
	}

	if _, err := contract.Bn254PairingCheck([]byte{1, 2, 3}); err == nil {
		t.Fatal("expected malformed input error")
	}
}

func TestRuntimeCalldataLengthAndCopy(t *testing.T) {
	wasmBytes, err := wasmtime.Wat2Wasm(`
		(module
		  (import "frg" "calldata_len" (func $calldata_len (result i32)))
		  (import "frg" "calldata_copy" (func $calldata_copy (param i32 i32 i32) (result i32)))
		  (import "frg" "state_set" (func $state_set (param i32 i32 i32 i32) (result i32)))
		  (memory (export "memory") 1)
		  (data (i32.const 0) "out")
		  (func (export "init"))
		  (func (export "call")
		    (call $calldata_len)
		    (drop)
		    (call $calldata_copy (i32.const 16) (i32.const 0) (i32.const 3))
		    (drop)
		    (call $state_set (i32.const 0) (i32.const 3) (i32.const 16) (i32.const 3))
		    (drop)
		  )
		)
	`)
	if err != nil {
		t.Fatalf("Wat2Wasm: %v", err)
	}

	db, err := bolt.Open(t.TempDir()+"/test.db", 0600, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	l, err := ledger.New(db)
	if err != nil {
		t.Fatal(err)
	}
	kp, _ := keys.GenerateKeypair()
	store := contract.NewStateStore()
	if err := db.Update(func(btx *bolt.Tx) error {
		rt, err := contract.NewRuntime(&contract.RuntimeConfig{
			WasmBytes: wasmBytes,
			Caller:    kp.PublicKey,
			SelfAddr:  kp.PublicKey,
			Value:     big.NewInt(0),
			CallData:  []byte("abc"),
			State:     store,
			Ledger:    l,
			BoltTx:    btx,
			GasLimit:  1000,
		})
		if err != nil {
			return err
		}
		_, err = rt.Call("call")
		return err
	}); err != nil {
		t.Fatalf("runtime call: %v", err)
	}
	value, found := store.Get([]byte("out"))
	if !found || string(value) != "abc" {
		t.Fatalf("calldata payload = %q, found=%v; want %q", value, found, "abc")
	}
}

func TestRuntimeBn254PairingPrecompile(t *testing.T) {
	g1 := new(bn256.G1).ScalarBaseMult(big.NewInt(1))
	negG1 := new(bn256.G1).Neg(g1)
	g2 := new(bn256.G2).ScalarBaseMult(big.NewInt(1))

	input := append(append([]byte{}, g1.Marshal()...), g2.Marshal()...)
	input = append(input, negG1.Marshal()...)
	input = append(input, g2.Marshal()...)

	wasmBytes, err := wasmtime.Wat2Wasm(fmt.Sprintf(`
		(module
		  (import "frg" "bn254_pairing_check" (func $bn254_pairing_check (param i32 i32) (result i32)))
		  (memory (export "memory") 1)
		  (data (i32.const 0) "%s")
		  (func (export "init"))
		  (func (export "call")
		    (call $bn254_pairing_check (i32.const 0) (i32.const %d))
		    (drop)
		  )
		)
	`, watDataString(input), len(input)))
	if err != nil {
		t.Fatalf("Wat2Wasm: %v", err)
	}

	db, err := bolt.Open(t.TempDir()+"/test.db", 0600, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	l, err := ledger.New(db)
	if err != nil {
		t.Fatal(err)
	}
	kp, _ := keys.GenerateKeypair()
	if err := l.Seed(kp.PublicKey, big.NewInt(10000)); err != nil {
		t.Fatal(err)
	}

	store := contract.NewStateStore()
	var fuelUsed uint64
	if err := db.Update(func(btx *bolt.Tx) error {
		cost, err := contract.Bn254PairingFuel(len(input))
		if err != nil {
			return err
		}
		rt, err := contract.NewRuntime(&contract.RuntimeConfig{
			WasmBytes:   wasmBytes,
			Caller:      kp.PublicKey,
			SelfAddr:    kp.PublicKey,
			Value:       big.NewInt(0),
			BlockHeight: 1,
			State:       store,
			Ledger:      l,
			BoltTx:      btx,
			GasLimit:    cost/contract.FuelUnitsPerGas + 100,
		})
		if err != nil {
			return err
		}
		if _, err := rt.Call("call"); err != nil {
			return err
		}
		fuelUsed = rt.FuelConsumed()
		return nil
	}); err != nil {
		t.Fatalf("runtime call: %v", err)
	}

	expected, err := contract.Bn254PairingFuel(len(input))
	if err != nil {
		t.Fatal(err)
	}
	if fuelUsed < expected {
		t.Fatalf("fuelUsed=%d, want at least precompile charge %d", fuelUsed, expected)
	}
}

func TestRuntimeBn254PairingPrecompileOutOfGas(t *testing.T) {
	wasmBytes, err := wasmtime.Wat2Wasm(`
		(module
		  (import "frg" "bn254_pairing_check" (func $bn254_pairing_check (param i32 i32) (result i32)))
		  (memory (export "memory") 1)
		  (func (export "init"))
		  (func (export "call")
		    (call $bn254_pairing_check (i32.const 0) (i32.const 0))
		    (drop)
		  )
		)
	`)
	if err != nil {
		t.Fatalf("Wat2Wasm: %v", err)
	}

	db, err := bolt.Open(t.TempDir()+"/test.db", 0600, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	l, err := ledger.New(db)
	if err != nil {
		t.Fatal(err)
	}
	kp, _ := keys.GenerateKeypair()
	if err := l.Seed(kp.PublicKey, big.NewInt(10000)); err != nil {
		t.Fatal(err)
	}

	store := contract.NewStateStore()
	if err := db.Update(func(btx *bolt.Tx) error {
		rt, err := contract.NewRuntime(&contract.RuntimeConfig{
			WasmBytes:   wasmBytes,
			Caller:      kp.PublicKey,
			SelfAddr:    kp.PublicKey,
			Value:       big.NewInt(0),
			BlockHeight: 1,
			State:       store,
			Ledger:      l,
			BoltTx:      btx,
			GasLimit:    1,
		})
		if err != nil {
			return err
		}
		_, err = rt.Call("call")
		var rgErr *rgerrors.RGError
		if !errors.As(err, &rgErr) || rgErr.Code != rgerrors.ErrContractOutOfGas {
			t.Fatalf("expected %s, got %v", rgerrors.ErrContractOutOfGas, err)
		}
		return nil
	}); err != nil {
		t.Fatalf("runtime call: %v", err)
	}
}

func TestRuntimeStateSetHostChargeOutOfGas(t *testing.T) {
	wasmBytes, err := wasmtime.Wat2Wasm(`
		(module
		  (import "frg" "state_set" (func $state_set (param i32 i32 i32 i32) (result i32)))
		  (memory (export "memory") 1)
		  (data (i32.const 0) "key-value")
		  (func (export "init"))
		  (func (export "call")
		    (call $state_set (i32.const 0) (i32.const 3) (i32.const 4) (i32.const 5))
		    (drop)
		  )
		)
	`)
	if err != nil {
		t.Fatalf("Wat2Wasm: %v", err)
	}

	db, err := bolt.Open(t.TempDir()+"/test.db", 0600, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	l, err := ledger.New(db)
	if err != nil {
		t.Fatal(err)
	}
	kp, _ := keys.GenerateKeypair()
	if err := l.Seed(kp.PublicKey, big.NewInt(10000)); err != nil {
		t.Fatal(err)
	}

	store := contract.NewStateStore()
	if err := db.Update(func(btx *bolt.Tx) error {
		rt, err := contract.NewRuntime(&contract.RuntimeConfig{
			WasmBytes:   wasmBytes,
			Caller:      kp.PublicKey,
			SelfAddr:    kp.PublicKey,
			Value:       big.NewInt(0),
			BlockHeight: 1,
			State:       store,
			Ledger:      l,
			BoltTx:      btx,
			GasLimit:    1,
		})
		if err != nil {
			return err
		}
		_, err = rt.Call("call")
		var rgErr *rgerrors.RGError
		if !errors.As(err, &rgErr) || rgErr.Code != rgerrors.ErrContractOutOfGas {
			t.Fatalf("expected %s, got %v", rgerrors.ErrContractOutOfGas, err)
		}
		return nil
	}); err != nil {
		t.Fatalf("runtime call: %v", err)
	}
}

func TestRuntimeHostFunctionFuelCharges(t *testing.T) {
	wasmBytes, err := wasmtime.Wat2Wasm(`
		(module
		  (import "frg" "state_set" (func $state_set (param i32 i32 i32 i32) (result i32)))
		  (import "frg" "state_get" (func $state_get (param i32 i32 i32 i32) (result i32)))
		  (import "frg" "log" (func $log (param i32 i32)))
		  (memory (export "memory") 1)
		  (data (i32.const 0) "key-value")
		  (func (export "init"))
		  (func (export "call")
		    (call $state_set (i32.const 0) (i32.const 3) (i32.const 4) (i32.const 5))
		    (drop)
		    (call $state_get (i32.const 0) (i32.const 3) (i32.const 32) (i32.const 16))
		    (drop)
		    (call $log (i32.const 0) (i32.const 9))
		  )
		)
	`)
	if err != nil {
		t.Fatalf("Wat2Wasm: %v", err)
	}

	db, err := bolt.Open(t.TempDir()+"/test.db", 0600, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	l, err := ledger.New(db)
	if err != nil {
		t.Fatal(err)
	}
	kp, _ := keys.GenerateKeypair()
	if err := l.Seed(kp.PublicKey, big.NewInt(10000)); err != nil {
		t.Fatal(err)
	}

	store := contract.NewStateStore()
	var fuelUsed uint64
	if err := db.Update(func(btx *bolt.Tx) error {
		rt, err := contract.NewRuntime(&contract.RuntimeConfig{
			WasmBytes:   wasmBytes,
			Caller:      kp.PublicKey,
			SelfAddr:    kp.PublicKey,
			Value:       big.NewInt(0),
			BlockHeight: 1,
			State:       store,
			Ledger:      l,
			BoltTx:      btx,
			GasLimit:    1000,
		})
		if err != nil {
			return err
		}
		if _, err := rt.Call("call"); err != nil {
			return err
		}
		fuelUsed = rt.FuelConsumed()
		return nil
	}); err != nil {
		t.Fatalf("runtime call: %v", err)
	}

	minExpected := uint64(contract.HostStorageWriteFuel + contract.HostStorageReadFuel + contract.HostLogFuel)
	if fuelUsed < minExpected {
		t.Fatalf("fuelUsed=%d, want at least host charges %d", fuelUsed, minExpected)
	}
}

func watDataString(data []byte) string {
	var b strings.Builder
	for _, c := range data {
		fmt.Fprintf(&b, "\\%02x", c)
	}
	return b.String()
}

func TestLoadStateValue(t *testing.T) {
	db, err := bolt.Open(t.TempDir()+"/test.db", 0600, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	l, err := ledger.New(db)
	if err != nil {
		t.Fatal(err)
	}
	kp, _ := keys.GenerateKeypair()
	if err := l.Seed(kp.PublicKey, big.NewInt(10000)); err != nil {
		t.Fatal(err)
	}
	if err := db.Update(func(btx *bolt.Tx) error {
		_, err := btx.CreateBucketIfNotExists([]byte("contract_bytecode"))
		if err != nil {
			return err
		}
		_, err = btx.CreateBucketIfNotExists([]byte("contract_state"))
		return err
	}); err != nil {
		t.Fatal(err)
	}

	deployTx := &tx.Tx{
		Type:         tx.TxTypeContractDeploy,
		Sender:       "alice",
		Receiver:     "contract",
		Value:        big.NewInt(0),
		Nonce:        1,
		SenderPubKey: kp.PublicKey,
		WasmBytes:    minimalWasm,
	}
	sig, _ := deployTx.SignSender(kp)
	deployTx.SenderSig = sig
	contractAddr := contract.ContractAddr(kp.PublicKey, 1)

	if err := db.Update(func(btx *bolt.Tx) error {
		if _, _, err := contract.Deploy(btx, l, deployTx, 1, 1000000); err != nil {
			return err
		}
		state := contract.NewStateStore()
		if err := state.Set([]byte("count"), []byte{7}); err != nil {
			return err
		}
		return btx.Bucket([]byte("contract_state")).Put(contractAddr[:], state.Serialize())
	}); err != nil {
		t.Fatal(err)
	}

	if err := db.View(func(btx *bolt.Tx) error {
		exists, found, value, root := contract.LoadStateValue(btx, contractAddr, []byte("count"))
		if !exists || !found {
			t.Fatalf("exists=%v found=%v", exists, found)
		}
		if len(value) != 1 || value[0] != 7 {
			t.Fatalf("value = %x", value)
		}
		if root == [32]byte{} {
			t.Fatal("empty state root")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestContractAddrDeterminism(t *testing.T) {
	var pk [32]byte
	copy(pk[:], bytesRepeat(1, 32))
	addr1 := contract.ContractAddr(pk, 5)
	addr2 := contract.ContractAddr(pk, 5)
	if addr1 != addr2 {
		t.Fatal("contract addr not deterministic")
	}
	addr3 := contract.ContractAddr(pk, 6)
	if addr1 == addr3 {
		t.Fatal("different nonces should produce different addresses")
	}
}

func TestContractStatePersistence(t *testing.T) {
	db, err := bolt.Open(t.TempDir()+"/test.db", 0600, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	l, err := ledger.New(db)
	if err != nil {
		t.Fatal(err)
	}

	kp, _ := keys.GenerateKeypair()
	if err := l.Seed(kp.PublicKey, big.NewInt(10000)); err != nil {
		t.Fatal(err)
	}

	if err := db.Update(func(btx *bolt.Tx) error {
		_, err := btx.CreateBucketIfNotExists([]byte("contract_bytecode"))
		if err != nil {
			return err
		}
		_, err = btx.CreateBucketIfNotExists([]byte("contract_state"))
		return err
	}); err != nil {
		t.Fatal(err)
	}

	deployTx := &tx.Tx{
		Type:         tx.TxTypeContractDeploy,
		Sender:       "alice",
		Receiver:     "contract",
		Value:        big.NewInt(0),
		Nonce:        1,
		SenderPubKey: kp.PublicKey,
		WasmBytes:    minimalWasm,
	}
	sig, _ := deployTx.SignSender(kp)
	deployTx.SenderSig = sig

	var deployRoot [32]byte
	if err := db.Update(func(btx *bolt.Tx) error {
		var err error
		deployRoot, _, err = contract.Deploy(btx, l, deployTx, 1, 1000000)
		return err
	}); err != nil {
		t.Fatalf("deploy: %v", err)
	}

	contractAddr := contract.ContractAddr(kp.PublicKey, 1)

	var persisted []byte
	db.View(func(btx *bolt.Tx) error {
		persisted = btx.Bucket([]byte("contract_state")).Get(contractAddr[:])
		return nil
	})
	if persisted == nil {
		t.Fatal("contract state was not persisted after deploy")
	}

	callTx := &tx.Tx{
		Type:           tx.TxTypeContractCall,
		Sender:         "alice",
		Receiver:       "contract",
		Value:          big.NewInt(0),
		Nonce:          2,
		SenderPubKey:   kp.PublicKey,
		ReceiverPubKey: contractAddr,
		CallData:       []byte("call"),
	}
	sig2, _ := callTx.SignSender(kp)
	callTx.SenderSig = sig2

	if err := db.Update(func(btx *bolt.Tx) error {
		_, _, err := contract.Call(btx, l, callTx, 2, 1000000)
		return err
	}); err != nil {
		t.Fatalf("call: %v", err)
	}

	var persisted2 []byte
	db.View(func(btx *bolt.Tx) error {
		persisted2 = btx.Bucket([]byte("contract_state")).Get(contractAddr[:])
		return nil
	})
	if len(persisted2) == 0 {
		t.Fatal("contract state was lost after call")
	}

	_ = deployRoot
}

func bytesRepeat(b byte, n int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = b
	}
	return out
}
