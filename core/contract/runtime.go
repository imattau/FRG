package contract

import (
	"fmt"
	"math/big"

	"github.com/bytecodealliance/wasmtime-go/v28"
	rgerrors "github.com/imattau/frg/core/errors"
	"github.com/imattau/frg/core/ledger"
	bolt "go.etcd.io/bbolt"
)

const maxFuel = 100_000_000_000

type Runtime struct {
	engine   *wasmtime.Engine
	store    *wasmtime.Store
	linker   *wasmtime.Linker
	instance *wasmtime.Instance
}

type RuntimeConfig struct {
	WasmBytes   []byte
	Caller      [32]byte
	SelfAddr    [32]byte
	Value       *big.Int
	BlockHeight uint64
	State       *StateStore
	Ledger      *ledger.Ledger
	BoltTx      *bolt.Tx
	GasLimit    uint64
}

func NewRuntime(cfg *RuntimeConfig) (*Runtime, error) {
	engine := wasmtime.NewEngine()
	store := wasmtime.NewStore(engine)
	store.SetFuel(maxFuel)

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
		mem := mustMem(caller)
		key, ok := readMem(mem, caller, keyPtr, keyLen)
		if !ok {
			return -1
		}
		val, found := cfg.State.Get(key)
		if !found {
			return 0
		}
		return writeMem(mem, caller, outPtr, outLen, val)
	})

	r.linker.FuncWrap("frg", "state_set", func(caller *wasmtime.Caller, keyPtr int32, keyLen int32, valPtr int32, valLen int32) {
		mem := mustMem(caller)
		key, ok := readMem(mem, caller, keyPtr, keyLen)
		if !ok {
			return
		}
		val, ok := readMem(mem, caller, valPtr, valLen)
		if !ok {
			return
		}
		cfg.State.Set(key, val)
	})

	r.linker.FuncWrap("frg", "self_balance", func(caller *wasmtime.Caller, outPtr int32) {
		mem := mustMem(caller)
		bal, _ := cfg.Ledger.BalanceOf(cfg.SelfAddr)
		writeMem(mem, caller, outPtr, 16, padBigToLE(bal, 16))
	})

	r.linker.FuncWrap("frg", "balance_of", func(caller *wasmtime.Caller, addrPtr int32, addrLen int32, outPtr int32) {
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
		return int64(cfg.BlockHeight)
	})

	r.linker.FuncWrap("frg", "caller", func(caller *wasmtime.Caller, outPtr int32) {
		mem := mustMem(caller)
		writeMem(mem, caller, outPtr, 32, cfg.Caller[:])
	})

	r.linker.FuncWrap("frg", "self_address", func(caller *wasmtime.Caller, outPtr int32) {
		mem := mustMem(caller)
		writeMem(mem, caller, outPtr, 32, cfg.SelfAddr[:])
	})

	r.linker.FuncWrap("frg", "log", func(caller *wasmtime.Caller, ptr int32, length int32) {
	})

	r.linker.FuncWrap("frg", "transfer", func(caller *wasmtime.Caller, recvPtr int32, recvLen int32, amtLo int64, amtHi int64) int64 {
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
		return nil, fmt.Errorf("%w: %v", rgerrors.New(rgerrors.ErrContractTrap, ""), err)
	}

	return nil, nil
}
