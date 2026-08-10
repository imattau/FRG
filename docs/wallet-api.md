# FRG Wallet SDK and Local API

FRG has two wallet surfaces:

- `github.com/imattau/frg/wallet` for Go applications.
- `frg-wallet`, a local HTTP API for tools, scripts, and web apps that should not speak gRPC directly.

Both surfaces use the same model: the wallet owns one Ed25519 keypair, queries the node for the current account nonce, signs sender-authorized transactions, and submits the serialized transaction to the node gRPC API.

## Amount Units

The chain stores balances as integer quanta. `1 FRG = 10^18 quanta`, and one
quantum is the smallest token unit.

The HTTP API treats `amount` and `value` fields as FRG decimal strings. Use
`amount_quanta` or `value_quanta` only when you need to submit raw integer
quanta.

## Security Boundary

`frg-wallet` is a local developer/operator API, not a hosted custody service. Bind it to loopback unless it is behind your own authentication layer:

```sh
frg-wallet --listen 127.0.0.1:8090
```

Anyone who can reach this API can spend the wallet key through `POST /transfer`, `POST /bond`, `POST /contracts/deploy`, and `POST /contracts/call`.
Validator lifecycle endpoints such as `POST /unbond`, `POST /finalize-unbond`, and `POST /claim-rewards` also sign and submit transactions with the wallet key.

## Build

```sh
go build -o frg-wallet ./cmd/frg-wallet
```

## First Run

Create a wallet key and connect to a local node:

```sh
./frg-wallet \
  --create-key \
  --key frg-wallet.key \
  --node 127.0.0.1:50051 \
  --chain-id frg-mainnet-1 \
  --listen 127.0.0.1:8090
```

The key file stores the 32-byte Ed25519 seed with `0600` permissions. Reuse the same `--key` path on later runs.

## Endpoints

### Health

```sh
curl http://127.0.0.1:8090/health
```

### Wallet Public Key

```sh
curl http://127.0.0.1:8090/pubkey
```

Response:

```json
{"pubkey":"...","chain_id":"frg-mainnet-1"}
```

### Account or Balance

Without a query parameter, the API returns the local wallet account:

```sh
curl http://127.0.0.1:8090/account
```

Query another account:

```sh
curl "http://127.0.0.1:8090/account?pubkey=USER_OR_VALIDATOR_PUBKEY"
```

`/balance` is an alias of `/account`.

### Transfer

```sh
curl -X POST http://127.0.0.1:8090/transfer \
  -H 'content-type: application/json' \
  -d '{"to":"RECIPIENT_PUBKEY","amount":"100"}'
```

The wallet signs the transfer with the local key. The recipient does not need to countersign.
For raw quanta, use `{"to":"RECIPIENT_PUBKEY","amount_quanta":"100000000000000000000"}`.

### Bond

```sh
curl -X POST http://127.0.0.1:8090/bond \
  -H 'content-type: application/json' \
  -d '{"amount":"1000"}'
```

This bonds the local wallet key as a validator key. The account must already hold enough FRG for the bond and transaction gas.

Start validator unbonding:

```sh
curl -X POST http://127.0.0.1:8090/unbond
```

Finalize unbonding after the protocol lockup:

```sh
curl -X POST http://127.0.0.1:8090/finalize-unbond
```

Claim validator rewards:

```sh
curl -X POST http://127.0.0.1:8090/claim-rewards
```

### Contracts

Predict the address for the wallet's next contract deployment:

```sh
curl http://127.0.0.1:8090/contracts/address
```

Predict the address for a specific deploy nonce:

```sh
curl "http://127.0.0.1:8090/contracts/address?nonce=12"
```

Deploy a WASM contract:

```sh
WASM_HEX="$(xxd -p -c 0 contract.wasm)"
curl -X POST http://127.0.0.1:8090/contracts/deploy \
  -H 'content-type: application/json' \
  -d "{\"wasm_hex\":\"$WASM_HEX\",\"value\":\"0\"}"
```

Response:

```json
{"txid":"...","contract_address":"..."}
```

Call a contract:

```sh
curl -X POST http://127.0.0.1:8090/contracts/call \
  -H 'content-type: application/json' \
  -d '{"contract_address":"CONTRACT_ADDRESS","function":"call","value":"0"}'
```

For lower-level callers, provide raw calldata:

```sh
curl -X POST http://127.0.0.1:8090/contracts/call \
  -H 'content-type: application/json' \
  -d '{"contract_address":"CONTRACT_ADDRESS","call_data_hex":"63616c6c"}'
```

The current contract runtime selects the exported function from the first four bytes of calldata. If neither `function` nor `call_data_hex` is provided, the wallet sends `call`.

Query contract existence and state root:

```sh
curl "http://127.0.0.1:8090/contracts/state?contract_address=CONTRACT_ADDRESS"
```

Query a contract state key as text:

```sh
curl "http://127.0.0.1:8090/contracts/state?contract_address=CONTRACT_ADDRESS&key=count"
```

Query a contract state key as raw hex:

```sh
curl "http://127.0.0.1:8090/contracts/state?contract_address=CONTRACT_ADDRESS&key_hex=636f756e74"
```

Response values are hex-encoded:

```json
{"contract_address":"...","exists":true,"state_root":"...","key":"636f756e74","found":true,"value":"07"}
```

### Faucet

Start the wallet with a faucet URL:

```sh
./frg-wallet --create-key --faucet-url http://127.0.0.1:8088/faucet
```

Fund the local wallet:

```sh
curl -X POST http://127.0.0.1:8090/faucet \
  -H 'content-type: application/json' \
  -d '{}'
```

Fund another pubkey:

```sh
curl -X POST http://127.0.0.1:8090/faucet \
  -H 'content-type: application/json' \
  -d '{"pubkey":"RECIPIENT_PUBKEY"}'
```

### Node Status and Validators

```sh
curl http://127.0.0.1:8090/status
curl http://127.0.0.1:8090/validators
```

## Go SDK

```go
package main

import (
	"context"

	"github.com/imattau/frg/core/denom"
	"github.com/imattau/frg/wallet"
)

func main() {
	ctx := context.Background()
	kp, err := wallet.LoadKeypair("frg-wallet.key")
	if err != nil {
		panic(err)
	}
	w, err := wallet.Dial(ctx, "127.0.0.1:50051", kp, "frg-mainnet-1")
	if err != nil {
		panic(err)
	}
	defer w.Close()

	to, err := wallet.DecodePubKey("RECIPIENT_PUBKEY")
	if err != nil {
		panic(err)
	}
	amount, err := denom.ParseFRG("100")
	if err != nil {
		panic(err)
	}
	if _, err := w.Transfer(ctx, to, amount); err != nil {
		panic(err)
	}

	wasm := []byte{0x00, 0x61, 0x73, 0x6d}
	zero, err := denom.ParseFRG("0")
	if err != nil {
		panic(err)
	}
	if _, err := w.DeployContract(ctx, wasm, zero); err != nil {
		panic(err)
	}

	addr, err := wallet.DecodePubKey("CONTRACT_ADDRESS")
	if err != nil {
		panic(err)
	}
	if _, err := w.ContractState(ctx, addr, []byte("count")); err != nil {
		panic(err)
	}
}
```

## Token Flow

New users and validators obtain FRG from one of the existing distribution paths:

- genesis allocations
- treasury or another funded holder using `frg-cli send` or `frg-wallet /transfer`
- a configured faucet using `frg-faucet` or `frg-wallet /faucet`
- protocol mint rewards after validators are bonded and blocks are produced

The wallet does not mint FRG. It only signs transactions using tokens already available to the wallet account.
