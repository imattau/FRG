package contract

import (
	"math/big"
)

const (
	GasDeployBase    = 100
	GasDeployPerByte = 1
	GasCallBase      = 10
	GasStorageRead   = 1
	GasStorageWrite  = 50
	GasTransferBase  = 50
	GasBalanceRead   = 2
	GasLogBase       = 1
	FuelPerWasmOp    = 10
)

func DeployGas(wasmLen uint32, baseFee *big.Int) *big.Int {
	total := new(big.Int).Mul(baseFee, big.NewInt(GasDeployBase))
	perByte := new(big.Int).Mul(baseFee, big.NewInt(int64(wasmLen)*GasDeployPerByte))
	total.Add(total, perByte)
	return total
}

func CallGas(dataLen int, baseFee *big.Int) *big.Int {
	total := new(big.Int).Mul(baseFee, big.NewInt(GasCallBase))
	return total
}

func WasmFuelToGas(fuelConsumed uint64, baseFee *big.Int) *big.Int {
	fuel := new(big.Int).SetUint64(fuelConsumed)
	return new(big.Int).Mul(fuel, baseFee)
}
