# FRG Wallet SDK and Local API

FRG has two wallet surfaces:

- `github.com/imattau/frg/wallet` for Go applications.
- `frg-wallet`, a local HTTP API for tools, scripts, and web apps that should not speak gRPC directly.

Both surfaces use the same model: the wallet owns one Ed25519 keypair, queries the node for the current account nonce, signs sender-authorized transactions, and submits the serialized transaction to the node gRPC API.

## Security Boundary

`frg-wallet` is a local developer/operator API, not a hosted custody service. Bind it to loopback unless it is behind your own authentication layer:

```sh
frg-wallet --listen 127.0.0.1:8090
```

Anyone who can reach this API can spend the wallet key through `POST /transfer` and `POST /bond`.

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

### Bond

```sh
curl -X POST http://127.0.0.1:8090/bond \
  -H 'content-type: application/json' \
  -d '{"amount":"1000"}'
```

This bonds the local wallet key as a validator key. The account must already hold enough FRG for the bond and transaction gas.

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
	"math/big"

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
	if _, err := w.Transfer(ctx, to, big.NewInt(100)); err != nil {
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
