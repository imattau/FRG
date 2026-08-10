package contract

import (
	"errors"
	"fmt"
	"math/big"

	"github.com/bytecodealliance/wasmtime-go/v28"
	rgerrors "github.com/imattau/frg/core/errors"
	"github.com/imattau/frg/core/ledger"
	bolt "go.etcd.io/bbolt"
)

const maxFuel = 100_000_000_000

// FuelUnitsPerGas converts deterministic Wasmtime fuel into protocol gas.
// This is a consensus/economic calibration constant: update it only through a
// protocol upgrade after rerunning the benchmark suite documented in
// docs/protocol-gas.md.
const FuelUnitsPerGas = 1000

const (
	HostMemoryByteFuel     = FuelPerWasmOp
	HostContextReadFuel    = FuelUnitsPerGas
	HostStorageReadFuel    = GasStorageRead * FuelUnitsPerGas
	HostStorageWriteFuel   = GasStorageWrite * FuelUnitsPerGas
	HostBalanceReadFuel    = GasBalanceRead * FuelUnitsPerGas
	HostLogFuel            = GasLogBase * FuelUnitsPerGas
	HostTransferFuel       = GasTransferBase * FuelUnitsPerGas
	HostBlockHeightFuel    = HostContextReadFuel
	HostAddressReadFuel    = HostContextReadFuel
	HostSelfBalanceFuel    = HostBalanceReadFuel
	HostCalldataLengthFuel = HostContextReadFuel
	HostPairingCheckReason = "bn254 pairing precompile"
)

type Runtime struct {
	engine      *wasmtime.Engine
	store       *wasmtime.Store
	linker      *wasmtime.Linker
	instance    *wasmtime.Instance
	initialFuel uint64
	hostErr     error
}

type RuntimeConfig struct {
	WasmBytes   []byte
	Caller      [32]byte
	SelfAddr    [32]byte
	Value       *big.Int
	BlockHeight uint64
	// CallData is the payload after the four-byte function selector.
	CallData []byte
	State    *StateStore
	Ledger   *ledger.Ledger
	BoltTx   *bolt.Tx
	GasLimit uint64
}

func NewRuntime(cfg *RuntimeConfig) (*Runtime, error) {
	wasmCfg := wasmtime.NewConfig()
	wasmCfg.SetConsumeFuel(true)
	engine := wasmtime.NewEngineWithConfig(wasmCfg)
	store := wasmtime.NewStore(engine)

	var fuelCap uint64 = maxFuel
	if cfg.GasLimit != 0 {
		if cfg.GasLimit > ^uint64(0)/FuelUnitsPerGas {
			fuelCap = ^uint64(0)
		} else {
			fuelCap = cfg.GasLimit * FuelUnitsPerGas
		}
	}
	if fuelCap == 0 {
		fuelCap = maxFuel
	}
	if err := store.SetFuel(fuelCap); err != nil {
		return nil, fmt.Errorf("%w: configure fuel: %v", rgerrors.New(rgerrors.ErrContractOutOfGas, ""), err)
	}

	module, err := wasmtime.NewModule(store.Engine, cfg.WasmBytes)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", rgerrors.New(rgerrors.ErrContractNonDeterministic, ""), err)
	}

	if err := validateModule(module); err != nil {
		return nil, err
	}

	linker := wasmtime.NewLinker(engine)
	rt := &Runtime{engine: engine, store: store, linker: linker}
	rt.defineHostFunctions(cfg)

	instance, err := linker.Instantiate(store, module)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", rgerrors.New(rgerrors.ErrContractNonDeterministic, ""), err)
	}
	rt.instance = instance

	fuelAfterInit, err := store.GetFuel()
	if err == nil {
		rt.initialFuel = fuelAfterInit
	}

	return rt, nil
}

func validateModule(module *wasmtime.Module) error {
	for _, imp := range module.Imports() {
		mod := imp.Module()
		if mod != "frg" && mod != "env" {
			return fmt.Errorf("%w: banned import from module %q", rgerrors.New(rgerrors.ErrContractNonDeterministic, ""), mod)
		}
		if mod == "env" {
			name := imp.Name()
			if name != nil && *name != "memory" {
				return fmt.Errorf("%w: banned env import %q", rgerrors.New(rgerrors.ErrContractNonDeterministic, ""), *name)
			}
		}
	}
	return nil
}

func (r *Runtime) defineHostFunctions(cfg *RuntimeConfig) {
	r.linker.FuncWrap("frg", "state_get", func(caller *wasmtime.Caller, keyPtr int32, keyLen int32, outPtr int32, outLen int32) int32 {
		if keyLen < 0 || outLen < 0 {
			return -1
		}
		if err := r.chargeFuelFor("state_get", hostFuel(HostStorageReadFuel, keyLen)); err != nil {
			r.hostErr = err
			return -1
		}
		mem := mustMem(caller)
		key, ok := readMem(mem, caller, keyPtr, keyLen)
		if !ok {
			return -1
		}
		val, found := cfg.State.Get(key)
		if !found {
			return 0
		}
		writeLen := len(val)
		if writeLen > int(outLen) {
			writeLen = int(outLen)
		}
		if err := r.chargeFuelFor("state_get output copy", hostFuel(0, int32(writeLen))); err != nil {
			r.hostErr = err
			return -1
		}
		return writeMem(mem, caller, outPtr, outLen, val)
	})

	r.linker.FuncWrap("frg", "state_set", func(caller *wasmtime.Caller, keyPtr int32, keyLen int32, valPtr int32, valLen int32) int32 {
		if keyLen < 0 || valLen < 0 {
			return 1
		}
		if err := r.chargeFuelFor("state_set", hostFuel(HostStorageWriteFuel, keyLen, valLen)); err != nil {
			r.hostErr = err
			return 1
		}
		mem := mustMem(caller)
		key, ok := readMem(mem, caller, keyPtr, keyLen)
		if !ok {
			return 1
		}
		val, ok := readMem(mem, caller, valPtr, valLen)
		if !ok {
			return 1
		}
		if err := cfg.State.Set(key, val); err != nil {
			r.hostErr = err
			return 1
		}
		return 0
	})

	r.linker.FuncWrap("frg", "self_balance", func(caller *wasmtime.Caller, outPtr int32) {
		if err := r.chargeFuelFor("self_balance", HostSelfBalanceFuel); err != nil {
			r.hostErr = err
			return
		}
		mem := mustMem(caller)
		bal, _ := cfg.Ledger.BalanceOf(cfg.SelfAddr)
		writeMem(mem, caller, outPtr, 16, padBigToLE(bal, 16))
	})

	r.linker.FuncWrap("frg", "balance_of", func(caller *wasmtime.Caller, addrPtr int32, addrLen int32, outPtr int32) {
		if addrLen < 0 {
			return
		}
		if err := r.chargeFuelFor("balance_of", hostFuel(HostBalanceReadFuel, addrLen)); err != nil {
			r.hostErr = err
			return
		}
		mem := mustMem(caller)
		addr, ok := readMem(mem, caller, addrPtr, addrLen)
		if !ok || len(addr) != 32 {
			return
		}
		var addr32 [32]byte
		copy(addr32[:], addr)
		bal, _ := cfg.Ledger.BalanceOf(addr32)
		writeMem(mem, caller, outPtr, 16, padBigToLE(bal, 16))
	})

	r.linker.FuncWrap("frg", "block_height", func() int64 {
		if err := r.chargeFuelFor("block_height", HostBlockHeightFuel); err != nil {
			r.hostErr = err
			return 0
		}
		return int64(cfg.BlockHeight)
	})

	r.linker.FuncWrap("frg", "caller", func(caller *wasmtime.Caller, outPtr int32) {
		if err := r.chargeFuelFor("caller", HostAddressReadFuel); err != nil {
			r.hostErr = err
			return
		}
		mem := mustMem(caller)
		writeMem(mem, caller, outPtr, 32, cfg.Caller[:])
	})

	r.linker.FuncWrap("frg", "self_address", func(caller *wasmtime.Caller, outPtr int32) {
		if err := r.chargeFuelFor("self_address", HostAddressReadFuel); err != nil {
			r.hostErr = err
			return
		}
		mem := mustMem(caller)
		writeMem(mem, caller, outPtr, 32, cfg.SelfAddr[:])
	})

	r.linker.FuncWrap("frg", "calldata_len", func() int32 {
		if err := r.chargeFuelFor("calldata_len", HostCalldataLengthFuel); err != nil {
			r.hostErr = err
			return 0
		}
		return int32(len(cfg.CallData))
	})

	r.linker.FuncWrap("frg", "calldata_copy", func(caller *wasmtime.Caller, dstPtr int32, offset int32, maxLen int32) int32 {
		if offset < 0 || maxLen < 0 || int64(offset) > int64(len(cfg.CallData)) {
			return -1
		}
		end := int64(offset) + int64(maxLen)
		if end > int64(len(cfg.CallData)) {
			end = int64(len(cfg.CallData))
		}
		copyLen := int32(end - int64(offset))
		if err := r.chargeFuelFor("calldata_copy", hostFuel(HostCalldataLengthFuel, copyLen)); err != nil {
			r.hostErr = err
			return -1
		}
		mem := mustMem(caller)
		if mem == nil || dstPtr < 0 || int64(dstPtr)+int64(copyLen) > int64(len(mem.UnsafeData(caller))) {
			return -1
		}
		if copyLen == 0 {
			return 0
		}
		writeMem(mem, caller, dstPtr, copyLen, cfg.CallData[int(offset):int(end)])
		return copyLen
	})

	r.linker.FuncWrap("frg", "log", func(caller *wasmtime.Caller, ptr int32, length int32) {
		if length < 0 {
			return
		}
		if err := r.chargeFuelFor("log", hostFuel(HostLogFuel, length)); err != nil {
			r.hostErr = err
			return
		}
		mem := mustMem(caller)
		_, _ = readMem(mem, caller, ptr, length)
	})

	r.linker.FuncWrap("frg", "transfer", func(caller *wasmtime.Caller, recvPtr int32, recvLen int32, amtLo int64, amtHi int64) int64 {
		if recvLen < 0 {
			return 1
		}
		if err := r.chargeFuelFor("transfer", hostFuel(HostTransferFuel, recvLen)); err != nil {
			r.hostErr = err
			return 1
		}
		mem := mustMem(caller)
		addr, ok := readMem(mem, caller, recvPtr, recvLen)
		if !ok || len(addr) != 32 {
			return 1
		}
		amount := new(big.Int).Lsh(big.NewInt(amtHi), 64)
		amount.Or(amount, big.NewInt(amtLo))
		if amount.Sign() <= 0 {
			return 1
		}
		var recipient [32]byte
		copy(recipient[:], addr)
		if err := cfg.Ledger.MoveTx(cfg.BoltTx, cfg.SelfAddr, recipient, amount); err != nil {
			return 1
		}
		return 0
	})

	r.linker.FuncWrap("frg", "bn254_pairing_check", func(caller *wasmtime.Caller, inputPtr int32, inputLen int32) int32 {
		mem := mustMem(caller)
		input, ok := readMem(mem, caller, inputPtr, inputLen)
		if !ok {
			return -1
		}
		cost, err := Bn254PairingFuel(len(input))
		if err != nil {
			return -1
		}
		if err := r.chargeFuel(cost); err != nil {
			r.hostErr = err
			return -1
		}
		valid, err := Bn254PairingCheck(input)
		if err != nil {
			return -1
		}
		if valid {
			return 1
		}
		return 0
	})
}

func hostFuel(base uint64, lengths ...int32) uint64 {
	total := base
	for _, length := range lengths {
		if length <= 0 {
			continue
		}
		if uint64(length) > (^uint64(0)-total)/HostMemoryByteFuel {
			return ^uint64(0)
		}
		total += uint64(length) * HostMemoryByteFuel
	}
	return total
}

func (r *Runtime) chargeFuel(fuel uint64) error {
	return r.chargeFuelFor(HostPairingCheckReason, fuel)
}

func (r *Runtime) chargeFuelFor(reason string, fuel uint64) error {
	remaining, err := r.store.GetFuel()
	if err != nil {
		return fmt.Errorf("%w: read fuel: %v", rgerrors.New(rgerrors.ErrContractOutOfGas, ""), err)
	}
	if remaining < fuel {
		_ = r.store.SetFuel(0)
		return rgerrors.Newf(rgerrors.ErrContractOutOfGas, "%s needs %d fuel, remaining %d", reason, fuel, remaining)
	}
	if err := r.store.SetFuel(remaining - fuel); err != nil {
		return fmt.Errorf("%w: charge fuel: %v", rgerrors.New(rgerrors.ErrContractOutOfGas, ""), err)
	}
	return nil
}

func mustMem(caller *wasmtime.Caller) *wasmtime.Memory {
	export := caller.GetExport("memory")
	if export == nil {
		return nil
	}
	return export.Memory()
}

func readMem(mem *wasmtime.Memory, store wasmtime.Storelike, ptr int32, length int32) ([]byte, bool) {
	if mem == nil || ptr < 0 || length < 0 {
		return nil, false
	}
	data := mem.UnsafeData(store)
	if int(ptr)+int(length) > len(data) {
		return nil, false
	}
	out := make([]byte, length)
	copy(out, data[ptr:ptr+length])
	return out, true
}

func writeMem(mem *wasmtime.Memory, store wasmtime.Storelike, ptr int32, maxLen int32, data []byte) int32 {
	if mem == nil || ptr < 0 {
		return 0
	}
	raw := mem.UnsafeData(store)
	writeLen := len(data)
	if writeLen > int(maxLen) {
		writeLen = int(maxLen)
	}
	if int(ptr)+writeLen > len(raw) {
		return 0
	}
	copy(raw[ptr:], data[:writeLen])
	return int32(writeLen)
}

func padBigToLE(n *big.Int, width int) []byte {
	raw := n.Bytes()
	out := make([]byte, width)
	offset := width - len(raw)
	if offset < 0 {
		offset = 0
		copy(raw, raw[len(raw)-width:])
	}
	if offset > 0 {
		copy(out[offset:], raw)
	} else {
		copy(out, raw)
	}
	for i, j := 0, width-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

func (r *Runtime) Call(functionName string) ([]byte, error) {
	run := r.instance.GetFunc(r.store, functionName)
	if run == nil {
		return nil, fmt.Errorf("%w: function %q not found", rgerrors.New(rgerrors.ErrContractNonDeterministic, ""), functionName)
	}

	_, err := run.Call(r.store)
	if err != nil {
		var trap *wasmtime.Trap
		if errors.As(err, &trap) {
			if code := trap.Code(); code != nil && *code == wasmtime.OutOfFuel {
				return nil, fmt.Errorf("%w: %v", rgerrors.New(rgerrors.ErrContractOutOfGas, ""), err)
			}
		}
		return nil, fmt.Errorf("%w: %v", rgerrors.New(rgerrors.ErrContractTrap, ""), err)
	}
	if r.hostErr != nil {
		var rgErr *rgerrors.RGError
		if errors.As(r.hostErr, &rgErr) {
			return nil, r.hostErr
		}
		return nil, fmt.Errorf("%w: %v", rgerrors.New(rgerrors.ErrContractStateInvalid, ""), r.hostErr)
	}

	return nil, nil
}

func (r *Runtime) FuelConsumed() uint64 {
	remaining, err := r.store.GetFuel()
	if err != nil {
		return 0
	}
	if r.initialFuel > remaining {
		return r.initialFuel - remaining
	}
	return 0
}
