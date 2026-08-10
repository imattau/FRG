# FRG Protocol Gas Calibration

`contract.FuelUnitsPerGas` is a consensus and economic calibration constant.
It is not meant to be adjusted dynamically by nodes.

Current value:

```go
const FuelUnitsPerGas = 1000
```

FRG charges contract compute as:

```text
protocol_gas = wasm_fuel / FuelUnitsPerGas
```

Every validator must use the same conversion or they will disagree about
balances, fee burns, base-fee updates, and block validity. Changing it is a
protocol upgrade.

## What Should Drive It

The value should be selected from benchmark data, not chosen per transaction:

- target validator hardware class
- target block execution CPU budget
- `gas.TargetGasPerBlock`
- the workload table from `go test ./benchmarks -run TestFuelCostModel -v`
- adversarial worst-case contracts, not just average application contracts

The current `1000` divisor keeps normal benchmark contracts cheap while making
plain-WASM cryptographic pairings visibly expensive.

Latest measured examples on the local benchmark machine:

| workload | fuel | protocol gas |
| --- | ---: | ---: |
| `heavy` | `242408` | `242` |
| `memory` | `3277061` | `3277` |
| `arithmetic` | `2300004` | `2300` |
| `bn254_pairing` | `65338457` | `65338` |

`bn254_pairing` exceeds the default `gas.TargetGasPerBlock` of `32768`, so FRG
exposes `frg.bn254_pairing_check(ptr,len)` as a charged host precompile instead
of expecting verifier contracts to run pairings as plain WASM.

## Retuning Rules

Retune `FuelUnitsPerGas` only when the project intentionally changes the target
block CPU budget or validator hardware assumption. Before changing it:

1. Run `go test ./benchmarks -run TestFuelCostModel -v`.
2. Run `go test ./benchmarks -bench BenchmarkFuelCalibration -run '^$' -benchmem`.
3. Confirm representative contract blocks stay within the intended CPU budget.
4. Confirm expensive crypto workloads either remain deliberately expensive or
   have explicit precompile pricing.
5. Treat the change as consensus-affecting for any existing chain.
