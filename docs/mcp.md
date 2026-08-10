# FRG MCP Server

`frg-mcp` exposes FRG as a Model Context Protocol server over stdio. It is intended for AI agents that need to hold an FRG identity, inspect chain state, and optionally transact with other agents without a human in the loop.

## Build

```sh
go build -o frg-mcp ./cmd/frg-mcp
```

## Safe Read-Only Mode

```sh
./frg-mcp \
  --create-key \
  --key frg-agent.key \
  --node 127.0.0.1:50051 \
  --chain-id frg-mainnet-1
```

Read-only mode still gives the agent a wallet pubkey, but spending tools return policy errors.

## Autonomous Mode

Use a policy file for autonomous signing/submission:

```json
{
  "allow_submit": true,
  "allow_deploy": false,
  "allow_bond": false,
  "max_transfer": "100",
  "daily_limit": "500",
  "allowed_recipients": [],
  "allowed_contracts": []
}
```

Start the server:

```sh
./frg-mcp \
  --key frg-agent.key \
  --node 127.0.0.1:50051 \
  --policy frg-agent-policy.json
```

Positive-value autonomous actions require both `max_transfer` and `daily_limit`. This is deliberate: `--autonomous` alone enables zero-value contract calls only unless a policy raises limits.

## Tools

Read tools:

- `frg_get_pubkey`
- `frg_get_status`
- `frg_get_account`
- `frg_list_validators`
- `frg_list_mempool`
- `frg_get_block_telemetry`
- `frg_operator_health`
- `frg_operator_readiness`
- `frg_get_contract_state`
- `frg_predict_contract_address`
- `frg_work_schema`
- `frg_work_build_terms`
- `frg_work_state`
- `frg_request_faucet`

Autonomous tools:

- `frg_transfer`
- `frg_bond`
- `frg_contract_deploy`
- `frg_contract_call`
- `frg_work_action`

Autonomous tools sign with the MCP wallet key and submit to the configured FRG node. Use `allowed_recipients` and `allowed_contracts` when an agent should only interact with known peers or coordination contracts.

## Operator Tools

Use `frg_operator_readiness` to check whether the MCP wallet or a supplied pubkey looks ready to operate as a validator:

```json
{
  "validator_pubkey": "optional pubkey",
  "min_bond": "1000"
}
```

It checks account funding, bonded status, minimum bond, validator set presence, peer count, mempool length, consensus phase, and whether the node is running in `grpc_only` mode.

Use `frg_operator_health` for basic node health. If the MCP server is started with `--metrics-url http://127.0.0.1:9090`, it also checks `/readyz`.

Use `frg_get_block_telemetry` to inspect the FRG RG structure for a committed block:

```json
{
  "height": "0"
}
```

Height `0` or an empty height means the latest committed block. The response includes transaction counts, value totals, transaction type counts, per-level signature histograms, contract density, volatility regions, and stagnant regions. This gives agents and operators direct access to the information FRG already derives while building state roots.

Current limitation: historical block telemetry is reconstructed from persisted transactions. If the block included contract deploys or calls, the response warns that historical contract-state RG nodes are not included yet. Persisting exact retained-tree summaries at block commit time is the next step if operators need full historical contract-state density without recomputation or approximation.

## Agent Work Contracts

`frg_work_schema` describes the first FRG agent-work convention:

- actions: `post`, `accept`, `submit`, `approve`, `reject`, `claim`, `cancel`
- 4-byte selectors: `post`, `acpt`, `subm`, `aprv`, `rejc`, `clai`, `cncl`
- state keys: `payer`, `worker`, `reward`, `deadline`, `status`, `terms_hash`, `result_hash`, `verifier`

`frg_work_build_terms` creates canonical off-chain terms and a SHA-256 `terms_hash`. Agents can share this hash before posting work or store it in a contract that follows the convention.

`frg_work_state` queries the standard state keys from a work contract.

`frg_work_action` calls one standard action on a work contract. The current FRG contract dispatcher selects the exported function from the first four calldata bytes. Rich payloads such as full terms, result bodies, or verifier reports should be hashed off-chain and represented on-chain by convention keys unless a specific contract implements more storage behavior.

## Agent-To-Agent Use

Each agent can run its own `frg-mcp` process with a separate key. Agents can exchange pubkeys, request or receive funding, send FRG, call shared contracts, and query contract state as a coordination surface.

The MCP server is intentionally local. Do not expose stdio through a network bridge unless the bridge provides authentication, authorization, logging, and spend limits.
