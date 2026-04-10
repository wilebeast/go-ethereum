# EVM Execution Visualizer

This experiment uses a pure Yul contract, compiles it with local `solc` when available, and then uses Geth's own EVM runtime + `StructLogger` tracer to capture every opcode step.

Files:

- [store_or_load.yul](~/docs/evm/store_or_load.yul)
- [main.go](~/cmd/evmvisualizer/main.go)
- [trace.json](~/docs/evm/trace.json)
- [visualizer.html](~/docs/evm/visualizer.html)

## Contract

The contract has two branches:

- `calldatasize() == 0`
  - `sload(0)`
  - `mstore(0, value)`
  - `return(0, 32)`
- `calldatasize() != 0`
  - `sstore(0, calldataload(0))`
  - `sload(0)`
  - `mstore(0, value)`
  - `return(0, 32)`

Runtime bytecode:

```text
0x365f146012575f355f555f545f5260205ff35b5f545f5260205ff3
```

Init bytecode:

```text
0x601b600b5f39601b5ff3fe365f146012575f355f555f545f5260205ff35b5f545f5260205ff3
```

Build behavior:

- preferred path
  - compile [store_or_load.yul](~/docs/evm/store_or_load.yul) with local `solc --standard-json`
- fallback path
  - if `solc` is missing, use embedded bytecode constants so the demo still runs

The actual path used for a given run is recorded in [trace.json](~/docs/evm/trace.json) under:

- `contract.buildMode`

## Run

Generate trace JSON and the standalone HTML viewer:

```bash
go run ./cmd/evmvisualizer \
  --yul docs/evm/store_or_load.yul \
  --json docs/evm/trace.json \
  --html docs/evm/visualizer.html
```

The HTML is self-contained. Open it in any browser or IDE preview that can render local HTML.

## What It Shows

- Per-opcode `pc`, `op`, `gas`, `gasCost`, `depth`
- Stack snapshot before each opcode
- Memory snapshot before each opcode
- Storage snapshot on `SLOAD` / `SSTORE`
- Manual gas breakdown vs Geth-measured gas
- The active build mode, so you can verify whether the run used compiled or fallback bytecode

## Relevant Source

- [interpreter.go](~/core/vm/interpreter.go)
- [gas_table.go](~/core/vm/gas_table.go)
- [opcodes.go](~/core/vm/opcodes.go)
- [stack.go](~/core/vm/stack.go)
- [memory.go](~/core/vm/memory.go)
- [logger.go](~/eth/tracers/logger/logger.go)

## Manual Gas Targets

Read path:

- `CALLDATASIZE` = `2`
- `PUSH1` = `3`
- `JUMPI` = `10`
- `PUSH1` = `3`
- cold `SLOAD` = `2100`
- `PUSH1` = `3`
- `MSTORE` = `3 + 3` memory expansion
- `PUSH1` = `3`
- `PUSH1` = `3`
- `RETURN` = `0`
- Total = `2133`

Write path (`0 -> nonzero`):

- `CALLDATASIZE` = `2`
- `PUSH1` = `3`
- `JUMPI` = `10`
- `JUMPDEST` = `1`
- `PUSH1` = `3`
- `CALLDATALOAD` = `3`
- `PUSH1` = `3`
- cold `SSTORE` = `22100`
- `PUSH1` = `3`
- warm `SLOAD` = `100`
- `PUSH1` = `3`
- `MSTORE` = `3 + 3` memory expansion
- `PUSH1` = `3`
- `PUSH1` = `3`
- `RETURN` = `0`
- Total = `22243`

The generator compares these manual numbers with Geth's trace output for:

- `read_empty`
- `write_value`
- `read_after_write`
