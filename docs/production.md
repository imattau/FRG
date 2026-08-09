# Production Operations

## Configuration

Run `frg-node` with an explicit configuration file. Non-loopback gRPC listeners require TLS 1.3, a client CA, and certificate fingerprint roles:

```toml
[grpc]
listen = "0.0.0.0:50051"
tls_cert_file = "/etc/frg/server.crt"
tls_key_file = "/etc/frg/server.key"
tls_client_ca_file = "/etc/frg/client-ca.crt"

[grpc.client_roles]
"<sha256-client-cert-fingerprint>" = "validator"
```

Keep the node key at mode `0600`. Metrics and readiness are exposed on loopback at `127.0.0.1:9090` by default.

## Backups

Create a timestamped backup and retain the newest seven snapshots:

```sh
frg-backup -db /var/lib/frg/frg.db -backup-dir /var/backups/frg -retain 7
```

Restore only into a new, stopped database path:

```sh
frg-backup -restore-from /var/backups/frg/frg-*.db \
  -restore-to /var/lib/frg/restore/frg.db
```

Validate the restored node with `frg-node -grpc-only -config ...` before replacing the active database. Never replace a database while `frg-node` is running.

## Upgrade and Rollback

1. Confirm `/readyz` is healthy and create a verified backup.
2. Stop the node cleanly and record the current binary, config, chain ID, and database height.
3. Install the new binary without changing the key, genesis, chain ID, or database path.
4. Start the node and verify `/readyz`, `/metrics`, peer count, height, and state root.
5. Roll back only by stopping the node, restoring the verified backup to a new path, validating it in isolation, and then switching the configured database path.

Do not reuse a database across different chain IDs or genesis files.

## Required Alerts

Alert on readiness failures, consensus phase remaining unchanged beyond the configured timeout, falling peer count, increasing `frg_sync_failures_total`, repeated RPC rejection spikes, and insufficient disk space for the database and backup volume.
