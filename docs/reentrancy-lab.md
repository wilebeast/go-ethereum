# Reentrancy Lab Workflow

This document is the execution guide for the week-6 reentrancy task.

Related files:

- [run_precompile_devnet.sh](../scripts/run_precompile_devnet.sh)
- [reentrancy_trace_lab.py](../scripts/reentrancy_trace_lab.py)
- [main.go](../cmd/reentrancyscan/main.go)
- [reentrancy-bytecode-analysis.md](../docs/reentrancy-bytecode-analysis.md)

## 1. Start A Local Chain

Build `geth` first:

```bash
CGO_ENABLED=1 make geth
```

Start the local node:

```bash
bash scripts/run_precompile_devnet.sh
```

The reentrancy contracts do not depend on the custom precompile. This script is reused because it already starts a local dev chain with HTTP RPC enabled.

## 2. Run The Full Exploit-And-Trace Script

```bash
python3 scripts/reentrancy_trace_lab.py
```

What it does:

1. compiles [VulnerableBank.sol](../docs/reentrancy/VulnerableBank.sol)
2. compiles [ReentrancyAttacker.sol](../docs/reentrancy/ReentrancyAttacker.sol)
3. deploys the bank
4. deposits funding into the bank from the dev account
5. deploys the attacker
6. calls `attack(maxDepth)`
7. fetches `debug_traceTransaction`
8. writes the trace JSON to `/tmp/reentrancy-attack-trace.json` by default

Expected output shape:

```json
{
  "deployer": "0x...",
  "bank": "0x...",
  "attacker": "0x...",
  "attackTx": "0x...",
  "tracePath": "/tmp/reentrancy-attack-trace.json"
}
```

## 3. Static Analysis From Deployed Bytecode

Once the bank contract is deployed, scan its runtime bytecode directly from RPC:

```bash
go run ./cmd/reentrancyscan \
  --rpc http://127.0.0.1:8545 \
  --address BANK_ADDRESS
```

Optional disassembly:

```bash
go run ./cmd/reentrancyscan \
  --rpc http://127.0.0.1:8545 \
  --address BANK_ADDRESS \
  --disasm
```

This uses `eth_getCode` under the hood.

## 4. Dynamic Analysis From Trace JSON

Analyze the attack trace produced by the script:

```bash
go run ./cmd/reentrancyscan \
  --trace-json /tmp/reentrancy-attack-trace.json
```

Expected style of output:

```text
[warning] depth=1->2 sload_pc=0x... call_pc=0x... reentry_pc=0x...
```

That means:

- a frame at depth `1` performed `SLOAD`
- then issued a `CALL`
- before that frame executed `SSTORE`, a deeper frame began doing storage-sensitive work again

## 5. Manual Inspection With The Article

Use the generated trace file together with:

- [reentrancy-bytecode-analysis.md](../docs/reentrancy-bytecode-analysis.md)

and inspect:

- where the first `SLOAD` happens
- where the bank performs the external `CALL`
- where the attacker reenters
- where the vulnerable function reads state again before the original writeback

## 6. Limits Of The Demo

This workflow is intended for:

- teaching the bytecode shape of reentrancy
- validating the scanner on a live example
- showing how Geth trace output can confirm the hazard dynamically

It is not:

- a historical DAO replay
- a full verifier
- a replacement for full CFG/data-flow analysis
