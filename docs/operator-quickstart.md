# FRG Validator Docker Quickstart

This guide starts one validator container from an existing network genesis file.

## Build the Image

```sh
docker build -t frg-node:local .
```

## Prepare Node Data

Create a persistent data directory and run the first-run initializer:

```sh
mkdir -p frg-data
docker run --rm \
  -v "$PWD/frg-data:/var/lib/frg" \
  -e FRG_CHAIN_ID="frg-mainnet-1" \
  -e FRG_P2P_PEERS="/dns4/bootstrap-1.example/tcp/7777/p2p/PEER_ID,/dns4/bootstrap-2.example/tcp/7777/p2p/PEER_ID" \
  frg-node:local init
```

This writes:

- `frg-data/frg.key`
- `frg-data/config.toml`
- `frg-data/.env`

It prints the validator public key, peer ID, and advertised multiaddr. Send the validator public key to the network/genesis coordinator if you are joining as a genesis validator.

Then place the network genesis in the data directory:

```sh
cp /path/to/genesis.json frg-data/genesis.json
```

If you already have a validator key instead, place the 32-byte seed or 64-byte private key before running `init`:

```sh
frg-data/frg.key
chmod 600 frg-data/frg.key
```

If `frg.key` is missing, `frg-node` creates one on first start. That is useful for generating a node identity, but it will only be a validator if that public key is included in genesis or bonded by the network rules.

## Run a Validator

```sh
docker run -d --name frg-validator \
  --restart unless-stopped \
  -p 7777:7777 \
  -p 127.0.0.1:50051:50051 \
  -p 127.0.0.1:9090:9090 \
  -v "$PWD/frg-data:/var/lib/frg" \
  --env-file "$PWD/frg-data/.env" \
  frg-node:local
```

The container generates `/var/lib/frg/config.toml` if one is not mounted. It refuses to start without `genesis.json` unless `FRG_ALLOW_BOOTSTRAP_GENESIS=true` is set.

## Environment

| Variable | Default | Purpose |
|---|---:|---|
| `FRG_DATA_DIR` | `/var/lib/frg` | Persistent node data directory |
| `FRG_CONFIG` | `$FRG_DATA_DIR/config.toml` | Config file path |
| `FRG_KEY_PATH` | `$FRG_DATA_DIR/frg.key` | Validator key seed/private key |
| `FRG_DB_PATH` | `$FRG_DATA_DIR/frg.db` | BoltDB state path |
| `FRG_GENESIS_PATH` | `$FRG_DATA_DIR/genesis.json` | Network genesis |
| `FRG_CHAIN_ID` | `frg-mainnet-1` | Chain identity |
| `FRG_P2P_LISTEN` | `/ip4/0.0.0.0/tcp/7777` | P2P listener |
| `FRG_P2P_PEERS` | empty | Comma-separated multiaddrs |
| `FRG_P2P_ENABLE_MDNS` | `false` | Local discovery |
| `FRG_GRPC_LISTEN` | `127.0.0.1:50051` | Admin gRPC listener |
| `FRG_METRICS_LISTEN` | `127.0.0.1:9090` | Metrics/readiness listener; must be loopback |

For public gRPC, configure mTLS:

```sh
docker run --rm \
  -v "$PWD/frg-data:/var/lib/frg" \
  -e FRG_GRPC_LISTEN="0.0.0.0:50051" \
  -e FRG_GRPC_TLS_CERT_FILE="/var/lib/frg/tls/server.crt" \
  -e FRG_GRPC_TLS_KEY_FILE="/var/lib/frg/tls/server.key" \
  -e FRG_GRPC_TLS_CLIENT_CA_FILE="/var/lib/frg/tls/client-ca.crt" \
  frg-node:local init --force
```

Set these before the first run, or re-run `init --force`, because `config.toml` is the source of truth once it exists.

## Health Checks

From the host:

```sh
curl -fsS http://127.0.0.1:9090/readyz
curl -fsS http://127.0.0.1:9090/metrics
docker logs -f frg-validator
```

## Private Single-Node Bootstrap

For local experiments only:

```sh
docker run --rm \
  -v "$PWD/frg-private:/var/lib/frg" \
  -e FRG_ALLOW_BOOTSTRAP_GENESIS=true \
  frg-node:local init

docker run --rm -it \
  -p 7777:7777 \
  -p 127.0.0.1:50051:50051 \
  -p 127.0.0.1:9090:9090 \
  -v "$PWD/frg-private:/var/lib/frg" \
  --env-file "$PWD/frg-private/.env" \
  frg-node:local
```

Do not use bootstrap genesis for joining an existing network.

## Source-Build Init

Without Docker:

```sh
go build -o frg-node ./cmd/frg-node
./frg-node init \
  --data-dir ./frg-data \
  --chain-id frg-mainnet-1 \
  --p2p-listen /ip4/0.0.0.0/tcp/7777 \
  --peers "/dns4/bootstrap-1.example/tcp/7777/p2p/PEER_ID"
```
