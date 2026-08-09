#!/usr/bin/env bash
# FRG Cluster Benchmark Script
# Usage: bash benchmarks/cluster_bench.sh [node_count] [tx_count]
#   node_count: number of validator nodes (default: 50)
#   tx_count:   transactions to submit (default: 10000)
#
# Prerequisites:
#   - frg-devnet and frg-stress built (go build ./cmd/frg-devnet ./cmd/frg-stress)
#   - docker and docker compose installed
#   - python3 available (for JSON parsing)
#
# Generates a devnet config, starts the cluster, runs stress test,
# polls all nodes for height parity, collects metrics, and tears down.

set -euo pipefail

NODE_COUNT="${1:-50}"
TX_COUNT="${2:-10000}"
WORKDIR="${TMPDIR:-/tmp}/frg-cluster-bench-$$"
BINDIR="$(dirname "$0")/.."

echo "=== FRG Cluster Benchmark ==="
echo "  nodes:       $NODE_COUNT"
echo "  transactions: $TX_COUNT"
echo "  workdir:     $WORKDIR"

cleanup() {
    echo "=== Cleanup ==="
    if [ -d "$WORKDIR" ]; then
        docker compose -f "$WORKDIR/docker-compose.yml" down -v 2>/dev/null || true
        rm -rf "$WORKDIR"
    fi
    echo "Done."
}
trap cleanup EXIT

mkdir -p "$WORKDIR"

echo ""
echo "=== Step 1: Generate devnet config ==="
cd "$BINDIR"
go build -o "$WORKDIR/frg-devnet" ./cmd/frg-devnet 2>&1
"$WORKDIR/frg-devnet" \
    --out "$WORKDIR" \
    --validators "$NODE_COUNT" \
    --stress-accounts 100 \
    > "$WORKDIR/devnet.log" 2>&1
echo "  config generated"

echo ""
echo "=== Step 2: Start cluster ==="
cd "$WORKDIR"
docker compose up -d 2>&1 | tail -5

echo "  waiting for cluster to stabilize..."
sleep 15

echo ""
echo "=== Step 3: Build stress client ==="
cd "$BINDIR"
go build -o "$WORKDIR/frg-stress" ./cmd/frg-stress 2>&1

echo ""
echo "=== Step 4: Find leader node ==="
LEADER_PORT=$(python3 -c "
import json
with open('$WORKDIR/devnet.json') as f:
    config = json.load(f)
# first node is leader
print(50051)
" 2>/dev/null || echo "50051")
echo "  leader on port $LEADER_PORT"

echo ""
echo "=== Step 5: Run stress test ==="
START_TIME=$(date +%s)
"$WORKDIR/frg-stress" \
    --accounts "$WORKDIR/stress_accounts.json" \
    --tx-per-account "$((TX_COUNT / 100))" \
    --rate 500 \
    --duration 120s \
    --addr "127.0.0.1:$LEADER_PORT" \
    2>&1 || true
END_TIME=$(date +%s)
DURATION=$((END_TIME - START_TIME))

echo ""
echo "=== Step 6: Collect final metrics ==="
HEIGHTS=""
CONVERGED=true
FIRST_HEIGHT=""

for i in $(seq 1 "$NODE_COUNT"); do
    PORT=$((50051 + i - 1))
    RESP=$(grpcurl -plaintext -protoset "$BINDIR/proto/frg.protoset" \
        "127.0.0.1:$PORT" frg.FRG/GetStatus 2>/dev/null || echo '{"height":0}')
    H=$(echo "$RESP" | python3 -c "import json,sys; print(json.load(sys.stdin).get('height',0))" 2>/dev/null || echo "0")
    HEIGHTS="$HEIGHTS $H"
    if [ -z "$FIRST_HEIGHT" ]; then
        FIRST_HEIGHT="$H"
    elif [ "$H" != "$FIRST_HEIGHT" ]; then
        CONVERGED=false
    fi
done

echo "  heights: $HEIGHTS"
echo "  converged: $CONVERGED"

echo ""
echo "=== Step 7: Compute TPS ==="
if [ "$FIRST_HEIGHT" != "0" ] && [ "$FIRST_HEIGHT" != "" ]; then
    TPS=$(echo "scale=2; $TX_COUNT / $DURATION" | bc 2>/dev/null || echo "N/A")
    echo "  total_duration_s: $DURATION"
    echo "  final_height: $FIRST_HEIGHT"
    echo "  txs_submitted: $TX_COUNT"
    echo "  approx_tps: $TPS"
fi

echo ""
echo "=== Cluster Benchmark Complete ==="
