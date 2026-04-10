# EVM Execution Visualizer

This experiment uses a pure Yul contract and Geth's own EVM runtime + `StructLogger` tracer to capture every opcode step.

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
0x36600f5760005460005260206000f35b60003560005560005460005260206000f3
```

Init bytecode:

```text
0x6021600c60003960216000f336600f5760005460005260206000f35b60003560005560005460005260206000f3
```

## Run

Generate trace JSON and the standalone HTML viewer:

```bash
go run ./cmd/evmvisualizer \
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
