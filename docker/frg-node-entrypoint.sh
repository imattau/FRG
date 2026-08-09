#!/bin/sh
set -eu

DATA_DIR="${FRG_DATA_DIR:-/var/lib/frg}"
CONFIG_PATH="${FRG_CONFIG:-$DATA_DIR/config.toml}"
KEY_PATH="${FRG_KEY_PATH:-$DATA_DIR/frg.key}"
DB_PATH="${FRG_DB_PATH:-$DATA_DIR/frg.db}"
GENESIS_PATH="${FRG_GENESIS_PATH:-$DATA_DIR/genesis.json}"
CHAIN_ID="${FRG_CHAIN_ID:-frg-mainnet-1}"
P2P_LISTEN="${FRG_P2P_LISTEN:-/ip4/0.0.0.0/tcp/7777}"
P2P_PEERS="${FRG_P2P_PEERS:-}"
P2P_ENABLE_MDNS="${FRG_P2P_ENABLE_MDNS:-false}"
GRPC_LISTEN="${FRG_GRPC_LISTEN:-127.0.0.1:50051}"
GRPC_TLS_CERT_FILE="${FRG_GRPC_TLS_CERT_FILE:-}"
GRPC_TLS_KEY_FILE="${FRG_GRPC_TLS_KEY_FILE:-}"
GRPC_TLS_CLIENT_CA_FILE="${FRG_GRPC_TLS_CLIENT_CA_FILE:-}"
METRICS_LISTEN="${FRG_METRICS_LISTEN:-127.0.0.1:9090}"
PROPOSE_DELAY_MS="${FRG_PROPOSE_DELAY_MS:-500}"
PROPOSE_TIMEOUT_MS="${FRG_PROPOSE_TIMEOUT_MS:-3000}"
PREVOTE_TIMEOUT_MS="${FRG_PREVOTE_TIMEOUT_MS:-3000}"
PRECOMMIT_TIMEOUT_MS="${FRG_PRECOMMIT_TIMEOUT_MS:-3000}"

mkdir -p "$DATA_DIR"

if [ "${1:-}" = "init" ]; then
  shift
  if [ "${FRG_ALLOW_BOOTSTRAP_GENESIS:-false}" = "true" ]; then
    exec frg-node init \
      --data-dir "$DATA_DIR" \
      --chain-id "$CHAIN_ID" \
      --p2p-listen "$P2P_LISTEN" \
      --grpc-listen "$GRPC_LISTEN" \
      --grpc-tls-cert-file "$GRPC_TLS_CERT_FILE" \
      --grpc-tls-key-file "$GRPC_TLS_KEY_FILE" \
      --grpc-tls-client-ca-file "$GRPC_TLS_CLIENT_CA_FILE" \
      --metrics-listen "$METRICS_LISTEN" \
      --peers "$P2P_PEERS" \
      --enable-mdns="$P2P_ENABLE_MDNS" \
      --bootstrap-genesis \
      "$@"
  fi
  exec frg-node init \
    --data-dir "$DATA_DIR" \
    --chain-id "$CHAIN_ID" \
    --p2p-listen "$P2P_LISTEN" \
    --grpc-listen "$GRPC_LISTEN" \
    --grpc-tls-cert-file "$GRPC_TLS_CERT_FILE" \
    --grpc-tls-key-file "$GRPC_TLS_KEY_FILE" \
    --grpc-tls-client-ca-file "$GRPC_TLS_CLIENT_CA_FILE" \
    --metrics-listen "$METRICS_LISTEN" \
    --peers "$P2P_PEERS" \
    --enable-mdns="$P2P_ENABLE_MDNS" \
    "$@"
fi

if [ -f "$KEY_PATH" ]; then
  chmod 600 "$KEY_PATH" 2>/dev/null || true
fi

if [ ! -f "$CONFIG_PATH" ]; then
  if [ ! -f "$GENESIS_PATH" ] && [ "${FRG_ALLOW_BOOTSTRAP_GENESIS:-false}" != "true" ]; then
    cat >&2 <<EOF
Missing genesis file: $GENESIS_PATH
Mount an existing network genesis at $GENESIS_PATH, or set FRG_ALLOW_BOOTSTRAP_GENESIS=true for a private single-node bootstrap.
EOF
    exit 1
  fi

  {
    cat <<EOF
[node]
keypair_path = "$KEY_PATH"
db_path = "$DB_PATH"
genesis_path = "$GENESIS_PATH"

[p2p]
listen = "$P2P_LISTEN"
peers = [
EOF
    if [ -n "$P2P_PEERS" ]; then
      old_ifs="$IFS"
      IFS=","
      for peer in $P2P_PEERS; do
        peer="$(echo "$peer" | sed 's/^[[:space:]]*//;s/[[:space:]]*$//')"
        if [ -n "$peer" ]; then
          printf '  "%s",\n' "$peer"
        fi
      done
      IFS="$old_ifs"
    fi
    cat <<EOF
]
enable_mdns = $P2P_ENABLE_MDNS

[grpc]
listen = "$GRPC_LISTEN"
EOF
    if [ -n "${FRG_GRPC_TLS_CERT_FILE:-}" ]; then
      printf 'tls_cert_file = "%s"\n' "$FRG_GRPC_TLS_CERT_FILE"
    fi
    if [ -n "${FRG_GRPC_TLS_KEY_FILE:-}" ]; then
      printf 'tls_key_file = "%s"\n' "$FRG_GRPC_TLS_KEY_FILE"
    fi
    if [ -n "${FRG_GRPC_TLS_CLIENT_CA_FILE:-}" ]; then
      printf 'tls_client_ca_file = "%s"\n' "$FRG_GRPC_TLS_CLIENT_CA_FILE"
    fi
    cat <<EOF

[metrics]
listen = "$METRICS_LISTEN"

[consensus]
propose_delay_ms = $PROPOSE_DELAY_MS
propose_timeout_ms = $PROPOSE_TIMEOUT_MS
prevote_timeout_ms = $PREVOTE_TIMEOUT_MS
precommit_timeout_ms = $PRECOMMIT_TIMEOUT_MS

chain_id = "$CHAIN_ID"
EOF
  } > "$CONFIG_PATH"
  echo "Generated $CONFIG_PATH"
fi

exec frg-node -config "$CONFIG_PATH" "$@"
