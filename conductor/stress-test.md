# FRG P2P Stress Test Plan

## Objective
Implement a dedicated stress test to verify the resilience, throughput, and correctness of the FRG P2P GossipSub layer under high transaction load across a multi-node mesh network.

## Scope
*   **Target:** `test/e2e/stress_test.go`
*   **Focus:** Transaction broadcast and propagation (`frg/tx/v1`).
*   **Validation:** Ensuring all nodes in the mesh eventually receive all valid transactions without deadlocking or dropping messages.

## Design

### Topology
1.  **Nodes:** Spawn a cluster of *N* local nodes (e.g., N=5).
2.  **Bootstrap:** Node 0 acts as the bootstrap node. Nodes 1 to N-1 connect to Node 0. GossipSub will automatically build a mesh from there.
3.  **Wait for Mesh:** Allow sufficient time for `go-libp2p-pubsub` to form the `D` (degree) peer connections.

### Workload (The Flood)
1.  **Generation:** Pre-generate *T* valid transactions per node (e.g., T=1000, Total=5000). Transactions must have valid signatures to pass the `parseTx` validation layer.
2.  **Concurrency:** Launch *N* goroutines (one per node). Each goroutine iterates through its assigned *T* transactions and broadcasts them via `node.BroadcastTx`.
3.  **Pacing:** Add an optional small delay (e.g., 1ms) between broadcasts to simulate high but realistic TPS, or blast them instantly to test burst capacity.

### Verification (Mempool Parity)
1.  **Collection:** Each node runs a subscriber goroutine listening on `SubscribeTxs()`. Received transactions are stored in a thread-safe `map[[32]byte]struct{}` (using `tx.ID()` as the key) to track unique receipts.
2.  **Condition:** The test actively polls until *all N nodes* report having exactly *N * T* unique transactions in their respective maps.
3.  **Timeout:** If parity is not reached within a reasonable timeframe (e.g., 30 seconds), the test fails, indicating dropped messages or a stalled network.

## Implementation Steps

1.  **Create `test/e2e/stress_test.go`:**
    *   Implement the test logic as described above.
    *   Use `testing.Short()` to skip or reduce the load if running in short mode.
2.  **Run and Tune:**
    *   Execute the stress test locally.
    *   Tune the number of nodes (N) and transactions (T) to ensure the test completes in a reasonable time for CI but still provides meaningful stress.
3.  **Review and Merge:**
    *   Ensure the test does not introduce flakiness into the CI pipeline.