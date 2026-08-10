# FRG Validator Quickstart

This guide covers the two operator paths:

- create the first node for a new network
- join an existing network

The examples use Podman. Docker works with the same image and commands; remove `:Z` from volume mounts if your Docker host does not support SELinux labels.

## Build the Image

```sh
podman build -t frg-node:local .
```

## First Node for a New Network

The first node creates the initial network identity and genesis. It does not use bootstrap peers, because there is nothing to join yet.

```sh
mkdir -p frg-first
podman run --rm \
  -v "$PWD/frg-first:/var/lib/frg:Z" \
  -e FRG_CHAIN_ID="frg-mainnet-1" \
  frg-node:local init-first-network
```

This writes `frg.key`, `config.toml`, `.env`, `genesis.json`, and `run-validator.sh` into `frg-first/`.

Start the first node:

```sh
cd frg-first
./run-validator.sh
```

The init output prints the validator public key, peer ID, and advertised multiaddr. Share the advertised multiaddr as the bootstrap peer for later nodes.

For a real public network with multiple genesis validators, collect all validator public keys first and publish one shared `genesis.json`. Do not let each validator run `init-first-network`, because that creates separate networks.

## Join an Existing Network

Joining requires the real network genesis and at least one bootstrap peer.

```sh
mkdir -p frg-data
podman run --rm \
  -v "$PWD/frg-data:/var/lib/frg:Z" \
  -v "$PWD/genesis.json:/network-genesis.json:ro,Z" \
  -e FRG_CHAIN_ID="frg-mainnet-1" \
  -e FRG_P2P_PEERS="/dns4/bootstrap-1.example/tcp/7777/p2p/PEER_ID" \
  frg-node:local init-join-network --genesis-source /network-genesis.json
```

Start the validator:

```sh
cd frg-data
./run-validator.sh
```

If this node is intended to be a genesis validator, send the printed `validator_pubkey` to the network coordinator before genesis is finalized.

## Become a Validator After Joining

A joined node can sync as a full node immediately. To become an active validator after genesis, fund the node's validator public key and submit a bond transaction from the node key:

```sh
go build -o frg-cli ./cmd/frg-cli
./frg-cli bond \
  --key frg-data/frg.key \
  --addr 127.0.0.1:50051 \
  --chain-id frg-mainnet-1 \
  --amount 1000
```

The bond amount must be at least the protocol minimum. The account also needs enough extra balance to pay transaction gas. Once the bond transaction is committed, the node appears in `ListValidators` and participates in proposer/vote selection.

Confirm activation:

```sh
./frg-cli validators --addr 127.0.0.1:50051
```

## Get Tokens

Users and new validators get FRG from genesis allocations, a funded treasury account, a faucet, or another holder. Transfers are sender-signed, so funded accounts can send directly to any user or validator pubkey:

```sh
./frg-cli send \
  --key treasury.key \
  --to USER_OR_VALIDATOR_PUBKEY \
  --amount 100
```

The faucet endpoint also funds the requested pubkey directly:

```sh
curl -X POST http://127.0.0.1:8088/faucet \
  -H 'content-type: application/json' \
  -d '{"pubkey":"USER_OR_VALIDATOR_PUBKEY"}'
```

## Manual Start

The generated `run-validator.sh` is equivalent to:

```sh
podman run -d --name frg-validator \
  --restart unless-stopped \
  -p 7777:7777 \
  -p 127.0.0.1:50051:50051 \
  -p 127.0.0.1:9090:9090 \
  -v "$PWD:/var/lib/frg:Z" \
  --env-file "$PWD/.env" \
  frg-node:local
```

## Health Checks

```sh
curl -fsS http://127.0.0.1:9090/readyz
curl -fsS http://127.0.0.1:9090/metrics
podman logs -f frg-validator
```

## Public gRPC

The default gRPC listener is loopback-only. For public gRPC, configure mTLS at init time:

```sh
podman run --rm \
  -v "$PWD/frg-data:/var/lib/frg:Z" \
  -e FRG_GRPC_LISTEN="0.0.0.0:50051" \
  -e FRG_GRPC_TLS_CERT_FILE="/var/lib/frg/tls/server.crt" \
  -e FRG_GRPC_TLS_KEY_FILE="/var/lib/frg/tls/server.key" \
  -e FRG_GRPC_TLS_CLIENT_CA_FILE="/var/lib/frg/tls/client-ca.crt" \
  frg-node:local init-join-network --force \
    --peers "/dns4/bootstrap-1.example/tcp/7777/p2p/PEER_ID" \
    --genesis-source /var/lib/frg/genesis.json
```

`config.toml` is the source of truth once it exists, so set TLS values during init or rerun init with `--force`.

## Source Build

Without containers:

```sh
go build -o frg-node ./cmd/frg-node
./frg-node init-first-network --data-dir ./frg-first --chain-id frg-mainnet-1
./frg-node init-join-network \
  --data-dir ./frg-data \
  --chain-id frg-mainnet-1 \
  --peers "/dns4/bootstrap-1.example/tcp/7777/p2p/PEER_ID" \
  --genesis-source ./genesis.json
```
