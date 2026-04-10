# EVM Execution Visualizer Design

This document explains how the visualizer works, why it is structured this way, and which Geth interfaces it uses.

Related files:

- [main.go](../../cmd/evmvisualizer/main.go)
- [store_or_load.yul](../../docs//evm/store_or_load.yul)
- [trace.json](../../docs//evm/trace.json)
- [visualizer.html](../../docs//evm/visualizer.html)
- [README.md](../../docs//evm/README.md)

## 1. Goal

The visualizer is designed to make EVM execution observable at opcode granularity without relying on Solidity tooling.

It solves four concrete problems:

1. Define a minimal contract directly in Yul.
2. Execute it inside Geth's own EVM implementation.
3. Capture per-step `stack`, `memory`, `storage`, `gas`, and `gasCost`.
4. Present those traces in a browser-friendly step viewer.

## 2. Scope

Current scope:

- Pure Yul contract compiled from source via local `solc` when available
- In-process execution via `core/vm/runtime`
- Tracing via Geth `StructLogger`
- Static HTML output with embedded JSON payload
- Manual gas accounting for selected scenarios

Current non-goals:

- No Solidity compiler integration
- No external node dependency
- No RPC dependency
- No live blockchain replay
- No dynamic disassembler UI

## 3. High-Level Workflow

```text
Yul source
   |
   v
solc --standard-json
   |
   v
runtime.Create(initcode)
   |
   v
contract deployed into in-memory StateDB
   |
   +-----------------------------------------------+
   |                                               |
   v                                               v
runtime.Call(read path)                    runtime.Call(write path)
   |                                               |
   v                                               v
vm interpreter loop                        vm interpreter loop
   |                                               |
   v                                               v
StructLogger.OnOpcode(...) captures each step
   |
   v
ExecutionResult { gas, returnValue, structLogs }
   |
   v
decode legacy structLogs into typed stepData
   |
   v
visualizerData
   |
   +-----------------------------+
   |                             |
   v                             v
trace.json                 visualizer.html
                                 |
                                 v
browser-side step viewer
```

## 4. Detailed Runtime Flow

### 4.1 Contract Definition

The contract is defined as Yul source in [store_or_load.yul](../../docs/evm/store_or_load.yul) and duplicated as a string constant in [main.go](../../cmd/evmvisualizer/main.go).

Important relation between source and bytecode in this project:

```text
store_or_load.yul
   |
   |  human-readable source of truth
   v
local solc available?
   |
   +---------------------------+-----------------------------+
   | yes                       | no                          |
   v                           v
compile Yul with              use embedded fallback
solc --standard-json          bytecode constants
   |
   v
runtimeCode / initCode []byte
   |
   v
runtime.Create(initCode, cfg)
   |
   v
contract deployed with runtimeBytecodeHex
```

This means:

- the Yul file explains the contract logic
- the normal path is to compile that Yul source with local `solc`
- the embedded bytecode is only a fallback for environments without `solc`
- `trace.json` records which path was used via `contract.buildMode`

So the current implementation is:

```text
Yul source
-> compiler
-> initcode
-> runtime code
```

with:

```text
embedded bytecode fallback
only when solc is unavailable
```

The runtime code has two branches:

- `calldatasize() == 0`
  - load storage slot `0`
  - write the loaded value into memory at offset `0`
  - return 32 bytes
- `calldatasize() != 0`
  - load the first 32 bytes of calldata
  - write it into storage slot `0`
  - read slot `0` again
  - store the value in memory
  - return 32 bytes

This is intentionally small because the visualizer is meant to teach:

- stack effects
- memory growth
- storage access patterns
- gas accounting for `SLOAD` and `SSTORE`

Execution state relationship:

```text
external caller
   |
   | input bytes for this call
   v
+------------------------------+
| calldata                     |
| - read-only                  |
| - per-call input             |
| - accessed by CALLDATASIZE   |
|   and CALLDATALOAD           |
+------------------------------+
              |
              | values loaded from input
              v
+------------------------------+
| stack                        |
| - opcode operand stack       |
| - temporary, per-call        |
| - PUSH/CALLDATALOAD/SLOAD    |
|   put values here            |
| - JUMPI/MSTORE/SSTORE/RETURN |
|   consume values from here   |
+------------------------------+
      |                  |
      |                  |
      v                  v
+-------------+   +------------------+
| memory      |   | storage          |
| - read/write|   | - persistent     |
| - temporary |   | - contract state |
| - per-call  |   | - survives calls |
| - MSTORE    |   | - SLOAD/SSTORE   |
| - RETURN    |   |   read/write it  |
+-------------+   +------------------+
```

In this contract:

- `calldata`
  - supplies the optional 32-byte write value
- `stack`
  - carries offsets, slot indices, loaded values, and return arguments between opcodes
- `memory`
  - holds the 32-byte return buffer before `RETURN`
- `storage`
  - keeps slot `0` across calls so the second read can observe the prior write

Single-step view of the write path:

```text
calldata[0:32]
   |
   | CALLDATALOAD(0)
   v
stack: [value]
   |
   | PUSH1 0x00
   v
stack: [slot=0, value]
   |
   | SSTORE
   v
storage[0] = value
   |
   | PUSH1 0x00
   | SLOAD
   v
stack: [value]
   |
   | PUSH1 0x00
   | MSTORE
   v
memory[0:32] = value
   |
   | PUSH1 0x20
   | PUSH1 0x00
   | RETURN
   v
output = memory[0:32]
```

This chain shows the role of each area:

- `CALLDATALOAD`
  - moves a 32-byte input word from calldata into the stack
- `SSTORE`
  - consumes `slot` and `value` from the stack and writes persistent state
- `SLOAD`
  - reads the same slot back into the stack
- `MSTORE`
  - moves the stack value into memory so it can be returned
- `RETURN`
  - copies bytes from memory into the final EVM output buffer

Meaning of `0` in different opcodes:

```text
+------------------+------------------+---------------------------------------------+
| Opcode form      | The `0` means    | Effect                                      |
+------------------+------------------+---------------------------------------------+
| CALLDATALOAD(0)  | calldata offset  | read 32 bytes starting at calldata byte 0   |
| MSTORE(0, x)     | memory offset    | write 32 bytes into memory starting at 0    |
| SLOAD(0)         | storage slot     | read storage slot 0                         |
| SSTORE(0, x)     | storage slot     | write storage slot 0                        |
+------------------+------------------+---------------------------------------------+
```

These all use the literal value `0`, but the address space is different:

- calldata uses byte offsets
- memory uses byte offsets
- storage uses slot indices

So:

- `CALLDATALOAD(0)` means `calldata[0:32]`
- `MSTORE(0, x)` means `memory[0:32] = x`
- `SLOAD(0)` / `SSTORE(0, x)` mean `storage[0]`

### 4.2 Deployment

Deployment uses [runtime.Create](../../core/vm/runtime/runtime.go).

Flow:

1. Build an in-memory [StateDB](../../core/state/statedb.go).
2. Seed the caller account with balance.
3. Call `runtime.Create(initCode, cfg)`.
4. `runtime.Create` internally creates an EVM with [NewEnv](../../core/vm/runtime/env.go).
5. The initcode executes and returns the runtime code.
6. Geth stores the returned runtime code under the new contract address.

Deployment boundary:

```text
compiled init bytecode
   |
   v
common.FromHex(initBytecodeHex)
   |
   v
initCode []byte
   |
   v
runtime.Create(initCode, cfg)
   |
   v
EVM executes initCode once
   |
   +----------------------------------+
   | initCode responsibilities        |
   | - copy runtime bytes to memory   |
   | - RETURN(runtime bytes)          |
   +----------------------------------+
   |
   v
returned byte slice
   |
   v
Geth stores it as contract code in StateDB
   |
   v
contract address now owns runtime code
   |
   v
runtime.Call(contractAddr, input, cfg)
   |
   v
EVM executes runtime code on every call
```

In short:

- `initCode` is the one-time deployment program
- `runtime code` is the persistent contract program
- `Create` runs `initCode`
- `Call` runs the stored runtime code

Generic initcode template vs current initcode:

```text
generic pattern
  PUSH runtime_size
  PUSH runtime_offset
  PUSH memory_offset
  CODECOPY
  PUSH runtime_size
  PUSH memory_offset
  RETURN
  <runtime bytes>
```

This pattern means:

- deployment code contains the runtime bytes inside itself
- `CODECOPY` copies those bytes from the initcode body into memory
- `RETURN` returns the copied bytes
- the returned bytes become the deployed contract code

Bytecode derivation for this project:

The current runtime and init bytecode are produced by compiling the Yul source with local `solc --standard-json`.
The embedded constants in [main.go](../../cmd/evmvisualizer/main.go) are only used as a fallback when `solc` is missing.

Runtime bytecode:

```text
0x365f146012575f355f555f545f5260205ff35b5f545f5260205ff3
```

Disassembly:

```text
00: 36        CALLDATASIZE
01: 5f        PUSH0
02: 14        EQ
03: 60 12     PUSH1 0x12
05: 57        JUMPI

06: 5f        PUSH0
07: 35        CALLDATALOAD
08: 5f        PUSH0
09: 55        SSTORE
0a: 5f        PUSH0
0b: 54        SLOAD
0c: 5f        PUSH0
0d: 52        MSTORE
0e: 60 20     PUSH1 0x20
10: 5f        PUSH0
11: f3        RETURN

12: 5b        JUMPDEST
13: 5f        PUSH0
14: 54        SLOAD
15: 5f        PUSH0
16: 52        MSTORE
17: 60 20     PUSH1 0x20
19: 5f        PUSH0
1a: f3        RETURN
```

How it maps to the Yul logic:

- `CALLDATASIZE; PUSH0; EQ; PUSH1 0x12; JUMPI`
  - compares `calldatasize()` with zero
  - if equal, jump to `pc = 0x12`
  - `0x12` is the start of the read branch
- bytes `0x06` to `0x11`
  - write branch
  - `CALLDATALOAD(0) -> SSTORE(0, value) -> SLOAD(0) -> MSTORE(0, value) -> RETURN(0, 32)`
- bytes `0x12` to `0x1a`
  - read branch
  - `SLOAD(0) -> MSTORE(0, value) -> RETURN(0, 32)`

Why the jump target is `0x12`:

```text
00: 36
01: 5f
02: 14
03: 60
04: 12
05: 57
06: 5f
07: 35
08: 5f
09: 55
0a: 5f
0b: 54
0c: 5f
0d: 52
0e: 60
0f: 20
10: 5f
11: f3
12: 5b  <-- first byte of the read branch JUMPDEST
```

So `PUSH1 0x12` is the compiled branch target for the read path.

Init bytecode:

```text
0x601b600b5f39601b5ff3fe365f146012575f355f555f545f5260205ff35b5f545f5260205ff3
```

Disassembly of the init prelude:

```text
00: 60 1b     PUSH1 0x1b
02: 60 0b     PUSH1 0x0b
04: 5f        PUSH0
05: 39        CODECOPY
06: 60 1b     PUSH1 0x1b
08: 5f        PUSH0
09: f3        RETURN
0a: fe        INVALID
```

Meaning of the constants:

- `0x1b`
  - runtime size
  - decimal `27`
  - the compiled runtime bytecode is `27` bytes long
- `0x0b`
  - runtime offset inside the initcode
  - decimal `11`
  - the init prelude before the runtime body is exactly `11` bytes

Why the runtime offset is `0x0b`:

```text
60 1b   2 bytes
60 0b   2 bytes
5f      1 byte
39      1 byte
60 1b   2 bytes
5f      1 byte
f3      1 byte
----------------
fe      1 byte padding / separator
----------------
runtime begins at byte 11 = 0x0b
```

So the initcode logic is:

```text
copy 27 bytes
from code offset 11
into memory offset 0
then return those 27 bytes
```

That returned slice is exactly the compiled runtime bytecode shown above.

Current initcode in this project:

```text
0x601b600b5f39601b5ff3fe
365f146012575f355f555f545f5260205ff35b5f545f5260205ff3
```

Split view:

```text
601b      PUSH1 0x1b   ; runtime size = 27 bytes
600b      PUSH1 0x0b   ; runtime starts at code offset 11
5f        PUSH0        ; memory destination = 0
39        CODECOPY
601b      PUSH1 0x1b   ; return size = 27 bytes
5f        PUSH0        ; return offset = 0
f3        RETURN
fe        INVALID      ; separator before appended runtime bytes

<27 bytes of runtime code follow>
365f146012575f355f555f545f5260205ff35b5f545f5260205ff3
```

Comparison:

- the current initcode is an instance of the generic `CODECOPY + RETURN` deployment template
- what changes from contract to contract is usually:
  - runtime size
  - runtime offset
  - runtime byte sequence
- more complex contracts may also add constructor logic before `RETURN`
  - initialize storage
  - read constructor calldata
  - compute dynamic runtime output

So the structure is common, but the actual bytes are contract-specific.

Why this design:

- It reuses Geth's actual deployment path.
- It avoids a fake interpreter harness.
- It keeps the experiment deterministic and local.

### 4.3 Traced Execution

Each scenario calls [runtime.Call](../../core/vm/runtime/runtime.go).

Scenarios:

- `read_empty`
- `write_value`
- `read_after_write`

Scenario comparison:

```text
+------------------+----------------------+-------------------+----------------------------------------------+
| Scenario         | Calldata             | Expected return   | Purpose                                       |
+------------------+----------------------+-------------------+----------------------------------------------+
| read_empty       | 0x                   | 0x00..00          | Show the initial read path before any write  |
| write_value      | 32-byte word = 0x2a | 0x00..2a          | Show write path, SSTORE, then immediate read |
| read_after_write | 0x                   | 0x00..2a          | Prove storage persists across calls          |
+------------------+----------------------+-------------------+----------------------------------------------+
```

Why all three are needed:

- `read_empty`
  - establishes the baseline
  - slot `0` starts as zero
  - the contract takes the empty-calldata branch
- `write_value`
  - exercises the write branch
  - stores `0x2a` into slot `0`
  - immediately reads the stored value back in the same call
- `read_after_write`
  - executes a new call with empty calldata
  - confirms that `storage[0]` still contains `0x2a`
  - shows that storage persists, while warm/cold access accounting resets per call

Same control flow vs different state result:

```text
+------------------------------+------------------------------+-----------------------------------+
| Comparison                   | Control flow                 | Result                            |
+------------------------------+------------------------------+-----------------------------------+
| read_empty vs write_value    | different                    | different                         |
| read_empty vs read_after_write | same read branch           | different, because storage changed|
| write_value vs read_after_write | different                 | different                         |
+------------------------------+------------------------------+-----------------------------------+
```

Key point:

- `read_empty` and `read_after_write` execute the same opcode branch
  - `CALLDATASIZE -> JUMPI(not taken) -> SLOAD -> MSTORE -> RETURN`
- but they do not observe the same state
  - the first read sees `storage[0] = 0`
  - the later read sees `storage[0] = 0x2a`
- this is exactly why both scenarios are useful
  - same control flow
  - different persistent state result

For each scenario:

1. Create a [StructLogger](../../eth/tracers/logger/logger.go).
2. Install it into `vm.Config.Tracer`.
3. Execute `runtime.Call`.
4. Let the EVM interpreter emit `OnOpcode` events before each opcode executes.
5. Extract the final JSON result via `StructLogger.GetResult()`.

Critical principle:

- The tracer sees the machine state *before* the opcode executes.
- Therefore:
  - `stack` is pre-op stack
  - `memory` is pre-op memory
  - `storage` snapshots appear on `SLOAD` and `SSTORE`
  - `gas` is remaining gas before the opcode
  - `gasCost` is the computed cost of the current opcode

## 5. Geth Interfaces Used

### 5.1 Execution Layer

- [runtime.Create](../../core/vm/runtime/runtime.go)
- [runtime.Call](../../core/vm/runtime/runtime.go)
- [NewEnv](../../core/vm/runtime/env.go)
- [interpreter.go](../../core/vm/interpreter.go)

Responsibilities:

- construct an EVM
- prepare state access context
- execute initcode/runtime code
- route opcodes through the interpreter loop

### 5.2 Tracing Layer

- [StructLogger](../../eth/tracers/logger/logger.go)
- `StructLogger.Hooks()`
- `OnOpcode`
- `GetResult`

Responsibilities:

- capture `pc`, `op`, `gas`, `gasCost`, `depth`
- optionally capture memory
- optionally capture stack
- capture storage reads/writes around `SLOAD` and `SSTORE`
- serialize trace result into JSON

### 5.3 State Layer

- [StateDB](../../core/state/statedb.go)
- `AddBalance`
- `GetState`

Responsibilities:

- hold the contract code
- persist storage slot updates across scenarios
- provide final slot values for result summaries

## 6. Internal Data Model

The main program converts raw Geth trace output into a simpler UI-facing schema.

### 6.1 Top-Level Model

```text
visualizerData
├── title
├── generatedAt
├── contract
├── contractAddress
├── scenarios[]
├── sourceLinks[]
└── manualSummary[]
```

Purpose:

- one object is enough to render the full viewer
- the HTML does not need extra network fetches
- the JSON can also be consumed by other UIs later

### 6.2 Scenario Model

```text
scenarioData
├── id
├── name
├── description
├── inputHex
├── returnHex
├── gasLimit
├── actualGasUsed
├── manualGasUsed
├── gasDelta
├── gasBreakdown[]
├── steps[]
└── finalStorage
```

Purpose:

- bind one transaction-like execution to one UI selection
- keep gas accounting and state evolution together

### 6.3 Step Model

```text
stepData
├── index
├── pc
├── op
├── gas
├── gasCost
├── depth
├── stack[]
├── memory[]
├── storage{}
├── gasUsedBefore
└── gasAfter
```

Purpose:

- flatten tracer output into stable browser-side fields
- remove legacy decoding complexity from the HTML
- make each step independently renderable

## 7. Workflow Chart With Interfaces

```text
+------------------------------+
| cmd/evmvisualizer/main.go    |
| buildVisualizerData()        |
+------------------------------+
              |
              v
+------------------------------+
| state.New(...)               |
| StateDB                      |
+------------------------------+
              |
              v
+------------------------------+
| runtime.Create(initcode)     |
| core/vm/runtime/runtime.go   |
+------------------------------+
              |
              v
+------------------------------+
| contract deployed            |
| address + code in StateDB    |
+------------------------------+
              |
              v
+------------------------------+
| logger.NewStructLogger(...)  |
| eth/tracers/logger/logger.go |
+------------------------------+
              |
              v
+------------------------------+
| runtime.Call(...)            |
| core/vm/runtime/runtime.go   |
+------------------------------+
              |
              v
+------------------------------+
| vm.Run(...)                  |
| core/vm/interpreter.go       |
+------------------------------+
              |
              v
+------------------------------+
| Tracer.OnOpcode(...)         |
| stack/memory/storage/gas     |
+------------------------------+
              |
              v
+------------------------------+
| StructLogger.GetResult()     |
| ExecutionResult JSON         |
+------------------------------+
              |
              v
+------------------------------+
| decodeSteps(...)             |
| typed stepData[]             |
+------------------------------+
              |
              +------------------------------+
              |                              |
              v                              v
+------------------------------+  +------------------------------+
| writeJSON(trace.json)        |  | writeHTML(visualizer.html)   |
+------------------------------+  +------------------------------+
                                               |
                                               v
                                +------------------------------+
                                | Browser-side render()        |
                                | scenario + step navigation   |
                                +------------------------------+
```

## 8. Interface Design

This project has two interface layers:

- Go-side execution/tracing interfaces
- HTML-side user interaction interfaces

### 8.1 Go-Side Interface Design

#### CLI Interface

Current flags:

```text
--json  path to write the generated trace JSON
--html  path to write the generated HTML visualizer
```

Design rationale:

- minimal surface area
- deterministic outputs
- easy to automate from shell scripts or CI

#### Internal Function Interfaces

Key internal entry points:

- `buildVisualizerData()`
- `deployContract(cfg *runtime.Config)`
- `traceCall(...)`
- `decodeSteps(...)`
- `writeJSON(...)`
- `writeHTML(...)`

Design rationale:

- deployment separated from execution
- trace extraction separated from rendering
- output generation separated from EVM logic

This keeps the code extensible for:

- more contracts
- more scenarios
- a future RPC-backed mode
- a future TUI or web server frontend

### 8.2 HTML Interface Design

The HTML viewer is intentionally static and client-side only.

Main UI regions:

```text
+-----------------------------------------------------------+
| Header                                                    |
+--------------------------+--------------------------------+
| Left Sidebar             | Main Content                   |
|                          |                                |
| - Scenario selector      | - Scenario summary            |
| - Step controls          | - Gas breakdown table         |
| - Contract summary       | - Current step summary        |
| - Manual gas notes       | - Stack panel                 |
|                          | - Memory panel                |
|                          | - Storage panel               |
|                          | - Yul source panel            |
+--------------------------+--------------------------------+
```

Interaction model:

- choose scenario from a dropdown
- step through opcodes with buttons or range slider
- watch stack/memory/storage update
- compare manual gas totals with actual totals

Design rationale:

- no backend required
- no fetch timing issues
- easy to archive and share as a single file

## 9. Why the Gas Accounting Matches

The manual totals match Geth because the selected contract is intentionally simple and avoids variable-cost complexity beyond:

- cold vs warm storage access
- one-time memory expansion from 0 to 32 bytes

That makes the calculation tractable:

- `SLOAD`
  - cold on first access per transaction
  - warm when revisiting the same slot inside the same transaction
- `SSTORE`
  - zero-to-nonzero write cost
  - includes the cold access component
- `MSTORE`
  - base opcode cost plus first-word memory expansion

This is why the current three scenarios all produce `gasDelta = 0` in [trace.json](../../docs//evm/trace.json).

## 10. Design Constraints

Constraints that shaped the implementation:

1. No Solidity compiler
   - bytecode must be fixed and deterministic

2. No external RPC
   - tracing must happen in-process

3. No custom EVM fork
   - behavior must reflect Geth as-is

4. UI must be portable
   - output should be a single HTML file

## 11. Extension Plan

Natural next steps:

1. Add more contracts
   - adder
   - branch-heavy examples
   - memory copy examples

2. Add RPC-backed mode
   - call `debug_traceCall`
   - reuse the same `visualizerData` schema

3. Add disassembly view
   - map `pc` to bytecode offsets and opcode bytes

4. Add gas attribution view
   - show dynamic gas rules inline per opcode

5. Add terminal UI
   - same data model
   - different renderer

## 12. Summary

The visualizer works because it stays close to Geth's actual execution path:

- contract deployment uses `runtime.Create`
- execution uses `runtime.Call`
- opcode snapshots come from `StructLogger.OnOpcode`
- final rendering uses a schema purpose-built for browsing execution state

The design is intentionally simple:

- one contract
- three scenarios
- one JSON schema
- one static HTML viewer

That simplicity is what makes the execution flow, gas model, and interface boundaries easy to inspect and extend.
