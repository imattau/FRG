# FRG Agent Swarm

LLM-powered multi-agent system for automated devnet testing of the Fractal Renormalisation Group (FRG) blockchain protocol.

You can spin up a full swarm of reasoning agents that share a local ollama model to systematically test the devnet.

## Quickstart

```bash
# 1. Build the FRG tools
go build -o frg-devnet ./cmd/frg-devnet
go build -o frg-cli ./cmd/frg-cli

# 2. Install Python deps
cd agents
pip install -r requirements.txt

# 3. Ensure ollama is running with a model
ollama pull llama3.2

# 4. Run the swarm (generates devnet, deploys, tests, tears down)
python -m src.orchestrator --model llama3.2

# Or with an existing devnet
python -m src.orchestrator --data-dir devnet-data --no-deploy --keep-devnet
```

The default test flow runs 7 phases:

| Phase | Agent | Description |
|-------|-------|-------------|
| 1 | Monitor | Baseline network health observation |
| 2 | Contract Tester | Deploy and call Wasm contracts (trivial → heavy) |
| 3 | Traffic Generator | Transaction load testing |
| 4 | Adversarial | Red team: exploit attempts on every attack surface |
| 5 | Fault Injector | Node kill/restart resilience testing |
| 6 | Traffic + Monitor | Parallel stress test with monitoring |
| 7 | Analyst | Final snapshot and report generation |

## Architecture

```
agents/
├── src/
│   ├── orchestrator.py          # CLI + main test flow
│   ├── swarm.py                 # Agent lifecycle manager
│   ├── tools/
│   │   ├── devnet_tools.py      # Docker compose / frg-devnet integration
│   │   ├── node_tools.py        # gRPC via frg-cli subprocess
│   │   ├── transaction_tools.py # Tx serialization + Ed25519 signing
│   │   ├── contract_tools.py    # Contract deploy/call/state
│   │   └── analysis_tools.py    # Consensus checks, metrics, reports
│   └── agents/
│       ├── base.py              # ReAct loop with ollama tool calling
│       ├── network_monitor.py   # Consensus health monitoring
│       ├── traffic_generator.py # Transaction load generation
│       ├── contract_tester.py   # Wasm contract deploy/call testing
│       ├── adversarial.py       # Red team: exploit attempts
│       ├── fault_injector.py    # Node failure injection
│       └── analyst.py           # Report generation and analysis
├── workloads/                   # Pre-built Wasm contracts
├── proto/frg.proto              # Reference protobuf spec
└── requirements.txt
```

## Agent Design

Each agent runs a ReAct (Reasoning + Acting) loop:

1. **Observe** — Query network state via tools
2. **Reason** — Send observations to shared ollama model
3. **Act** — Model returns a tool call → execute it
4. **Report** — Result becomes next observation

Agents share a single local ollama instance. Tools are defined as JSON schemas that ollama uses for native function calling.

## CLI Options

```
usage: frg-swarm [-h] [--model MODEL] [--ollama-host OLLAMA_HOST]
                 [--validators VALIDATORS] [--stress-accounts STRESS_ACCOUNTS]
                 [--base-grpc-port BASE_GRPC_PORT] [--max-iterations MAX_ITERATIONS]
                 [--data-dir DATA_DIR] [--keep-devnet] [--no-deploy]
                 [--frg-devnet-bin FRG_DEVNET_BIN]

Options:
  --model            Ollama model name (default: llama3.2)
  --ollama-host      Ollama host URL
  --validators       Number of validator nodes (default: 7)
  --stress-accounts  Pre-funded stress accounts (default: 50)
  --max-iterations   Max agent loop iterations (default: 30)
  --data-dir         Existing devnet directory (skip generation)
  --keep-devnet      Don't tear down devnet after test
  --no-deploy        Skip docker compose up (devnet already running)
  --frg-devnet-bin   Path to frg-devnet binary
```

## Adversarial Agent

The adversarial agent systematically probes every attack surface defined in the FRG error codes:

- **ERR_012**: Invalid/zeroed/wrong-key signatures
- **ERR_018**: Double spends, nonce skipping, replay attacks
- **ERR_013**: Insufficient funds transfers
- **ERR_019**: Unknown tx type bytes
- **ERR_009**: Malformed serialization, wrong domain, non-NFC UTF-8
- **ERR_026**: Calls to non-existent contracts
- **ERR_022**: Oversized Wasm bytecode
- **ERR_010**: Calldata size overflow, tx size overflow

Each attack must be rejected without crashing the node or corrupting state.

## Adding New Agents

1. Create a class in `src/agents/` extending `Agent`
2. Define tools in `_setup_tools()`
3. Implement `system_prompt()` with instructions for the LLM
4. Register in `AGENT_REGISTRY` in `swarm.py`
5. Add a phase in `run_default_swarm()`
