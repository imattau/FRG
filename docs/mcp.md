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
- `frg_get_contract_state`
- `frg_predict_contract_address`
- `frg_request_faucet`

Autonomous tools:

- `frg_transfer`
- `frg_bond`
- `frg_contract_deploy`
- `frg_contract_call`

Autonomous tools sign with the MCP wallet key and submit to the configured FRG node. Use `allowed_recipients` and `allowed_contracts` when an agent should only interact with known peers or coordination contracts.

## Agent-To-Agent Use

Each agent can run its own `frg-mcp` process with a separate key. Agents can exchange pubkeys, request or receive funding, send FRG, call shared contracts, and query contract state as a coordination surface.

The MCP server is intentionally local. Do not expose stdio through a network bridge unless the bridge provides authentication, authorization, logging, and spend limits.
