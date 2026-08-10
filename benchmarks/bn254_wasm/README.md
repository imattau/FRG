# BN254 WASM Fixture

This fixture builds a deterministic `no_std` WASM contract that runs one
arkworks BN254 pairing over the G1/G2 generators. It is used by
`benchmarks/fuel_gas_test.go` to measure whether a plain-WASM pairing verifier is
too expensive for FRG's contract gas model.

The current plain-WASM measurement is roughly `65,338,457` fuel for one pairing,
or `65,338` FRG gas at `FuelUnitsPerGas = 1000`. That exceeds the default
`TargetGasPerBlock` of `32,768`, so FRG also exposes a native
`frg.bn254_pairing_check(ptr,len)` host precompile for contract verifiers.

Regenerate `../workloads/bn254_pairing.wasm` with:

```sh
rustup target add wasm32-unknown-unknown
CARGO_TARGET_DIR=/tmp/frg-bn254-wasm-target \
  cargo build --release --target wasm32-unknown-unknown --offline
cp /tmp/frg-bn254-wasm-target/wasm32-unknown-unknown/release/frg_bn254_wasm.wasm \
  ../workloads/bn254_pairing.wasm
```

The generated module must not import WASI or other host modules. FRG contracts
currently permit only `frg` imports and `env.memory`.
