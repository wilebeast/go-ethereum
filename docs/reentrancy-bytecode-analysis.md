# From EVM Bytecode To Reentrancy

This document is the repository implementation for the week-6 task:

- a vulnerable Solidity pair
- a Go bytecode scanner
- a trace-oriented explanation of how reentrancy appears at the opcode level

Related files:

- [VulnerableBank.sol](~/docs/reentrancy/VulnerableBank.sol)
- [ReentrancyAttacker.sol](~/docs/reentrancy/ReentrancyAttacker.sol)
- [main.go](~/cmd/reentrancyscan/main.go)
- [reentrancy_trace_lab.py](~/scripts/reentrancy_trace_lab.py)
- [reentrancy-lab.md](~/docs/reentrancy-lab.md)
- [logger.go](~/eth/tracers/logger/logger.go)
- [opcodes.go](~/core/vm/opcodes.go)

## 1. Why Reentrancy Is Visible In Bytecode

At the source level, the vulnerable pattern is simple:

1. read a balance
2. perform an external call
3. write the balance to zero afterward

In EVM terms, the dangerous shape is:

```text
SLOAD(slot)
...
CALL
...
SSTORE(slot)
```

The problem is not that `CALL` exists. The problem is that control leaves the contract before the relevant storage slot is updated.

The opcodes involved in this repository are:

- `SLOAD` [opcodes.go](~/core/vm/opcodes.go#L116)
- `SSTORE` [opcodes.go](~/core/vm/opcodes.go#L117)
- `CALL` [opcodes.go](~/core/vm/opcodes.go#L240)
- `CALLCODE` [opcodes.go](~/core/vm/opcodes.go#L241)
- `DELEGATECALL` [opcodes.go](~/core/vm/opcodes.go#L243)

For classic ether-withdraw reentrancy, the critical external-control-transfer opcode is ordinary `CALL`.

## 2. Vulnerable Contract Pair

### 2.1 Vulnerable Bank

[VulnerableBank.sol](~/docs/reentrancy/VulnerableBank.sol) intentionally violates the Checks-Effects-Interactions pattern:

```solidity
uint256 amount = balances[msg.sender];
(bool ok, ) = msg.sender.call{value: amount}("");
require(ok, "transfer failed");
balances[msg.sender] = 0;
```

The storage write happens too late.

### 2.2 Attacker

[ReentrancyAttacker.sol](~/docs/reentrancy/ReentrancyAttacker.sol) reenters from `receive()`:

```solidity
receive() external payable {
    if (address(bank).balance > 0 && reentryCount < maxReentries) {
        reentryCount++;
        bank.withdraw();
    }
}
```

This is enough to demonstrate repeated control return into the vulnerable withdrawal function while the original balance slot still reflects the old value.

## 3. What To Trace In Geth

For actual transaction tracing in this repository, the relevant structured tracer output is defined in [logger.go](~/eth/tracers/logger/logger.go).

The key fields are:

- `pc`
- `op`
- `stack`
- `memory`
- `storage`
- `depth`
- `gas`
- `gasCost`

Those fields let you recognize the exploit shape directly:

```text
depth=1  ... SLOAD
depth=1  ... CALL
depth=2  ... attacker receive()
depth=2  ... CALL back into bank
depth=3  ... SLOAD of the same logical balance slot again
```

The important observation is not just that `CALL` appears twice. It is that the reentered `SLOAD` happens before the original `SSTORE` to zero has executed.

In this repository's demo pair, the callback is specifically `receive()`, not a generic `fallback / receive` pair. The reason is that the vulnerable bank executes:

```solidity
msg.sender.call{value: amount}("");
```

That sends:

- non-zero `value`
- empty calldata

so the attacker contract's `receive()` is the callback that gets triggered.

## 4. Reproduction Workflow

This repository does not hardcode a historical DAO environment. Instead it provides a minimal modern reproduction pair, which is easier to reason about and easier to trace.

Suggested workflow:

1. Start a local chain with a modified `geth`.
2. Compile and deploy [VulnerableBank.sol](../docs/reentrancy/VulnerableBank.sol).
3. Fund the bank contract from a normal account.
4. Deploy [ReentrancyAttacker.sol](../docs/reentrancy/ReentrancyAttacker.sol) against the bank address.
5. Call `attack(maxDepth)`.
6. Trace the resulting transaction with `debug_traceTransaction`.

At that point, the trace should show:

```text
bank.withdraw:
  SLOAD(balance slot)
  CALL(attacker)

attacker.receive:
  CALL(bank.withdraw)

bank.withdraw again:
  SLOAD(same balance slot, still non-zero)
```

The automated version of this workflow is implemented in [reentrancy_trace_lab.py](..//scripts/reentrancy_trace_lab.py).

### 4.1 Why The Demo Does Not Loop Forever

The attacker contract does not recurse without bound. Its `receive()` handler only reenters while both of these conditions hold:

```solidity
address(bank).balance > 0
reentryCount < maxReentries
```

So execution ends when any of the following becomes true:

1. `reentryCount` reaches `maxReentries`
2. the bank no longer has enough ether to continue the pattern
3. gas is exhausted
4. one of the nested calls fails and the call stack unwinds

At that point the deepest reentered call returns first, then each older frame resumes from the point after its `CALL`. The original vulnerable function eventually reaches its delayed:

```solidity
balances[msg.sender] = 0;
```

but by then the earlier nested withdrawals have already observed the old balance.

## 5. Why The Scanner Uses Bytecode, Not Source

The scanner under [main.go](../cmd/reentrancyscan/main.go) works on bytecode because:

- deployed contracts are ultimately EVM bytecode
- source may be unavailable
- opcode order is what actually constrains execution

The scanner is still a demo. It is not a full verifier.

## 6. Scanner Design

The scanner does four things:

1. decode hex bytecode into instructions
2. disassemble `PUSH*` immediates correctly
3. simulate a small abstract stack for:
   - `PUSH*`
   - `DUP*`
   - `SWAP*`
   - `POP`
   - `SLOAD`
   - `SSTORE`
   - `JUMP`
   - `JUMPI`
   - `CALL`
   - `CALLCODE`
   - `DELEGATECALL`
4. flag paths where a constant storage slot is `SLOAD`ed and then an external call occurs before a matching `SSTORE` clears the hazard

This is intentionally stronger than a naive linear grep for `54 f1 55`:

- it tracks stack-derived constant slots
- it follows simple constant jump destinations
- it removes a slot from the hazard set once `SSTORE(slot)` happens
- it recognizes a common mapping-slot shape:
  - `CALLER`
  - memory writes for key and base slot
  - `KECCAK256`
  - `SLOAD` / `SSTORE`

That last addition is what lets the demo scanner reason about `balances[msg.sender]`-style layouts instead of only trivial constant slots.

## 7. Scanner Usage

Run it on a runtime bytecode hex string:

```bash
go run ./cmd/reentrancyscan --bytecode 600054506000600060006000600060006000f1600060005500
```

Expected style of output:

```text
[warning] slot=0x sload_pc=0x2 call_pc=0x11 call_op=CALL
```

If you want disassembly too:

```bash
go run ./cmd/reentrancyscan --disasm --bytecode 600054506000600060006000600060006000f1600060005500
```

You can also fetch deployed runtime code directly from a node:

```bash
go run ./cmd/reentrancyscan \
  --rpc http://127.0.0.1:8545 \
  --address BANK_ADDRESS
```

And you can analyze a saved `debug_traceTransaction` result:

```bash
go run ./cmd/reentrancyscan \
  --trace-json /tmp/reentrancy-attack-trace.json
```

The bundled tests cover:

- vulnerable order: `SLOAD -> CALL -> SSTORE`
- safe order: `SLOAD -> SSTORE -> CALL`
- a simple constant jump path
- a trace JSON wrapper format
- a dynamic trace heuristic for reentry-like depth transitions

## 8. Interpreting Findings

A finding means:

- the scanner saw a storage read from a constant slot
- that slot had not yet been overwritten on the explored path
- an external control-transfer opcode occurred before the write

It does **not** prove exploitability by itself.

Real exploitability still depends on:

- whether the `CALL` target is attacker-controlled
- whether the slot corresponds to funds or a guard variable
- whether the contract uses other mitigations
- whether the reentered path can actually reach the vulnerable function

## 9. Limitations

This is a demo scanner, not Slither.

Current limitations:

1. it only reasons well about constant storage slots
2. it supports only a narrow mapping pattern for storage expressions, mainly `keccak256(CALLER, baseSlot)`
3. it only resolves jumps when the destination is statically known
4. it does not build a complete interprocedural CFG
5. it treats `CALLCODE` and `DELEGATECALL` conservatively as external-control-transfer hazards

That means:

- it can identify the core bytecode shape
- it still cannot fully reconstruct high-level semantics for arbitrary optimized contracts

## 10. Why This Is Still Useful

Even with those limits, the scanner is useful as a teaching and triage tool because it maps the source-level anti-pattern to what actually matters in the VM:

```text
load sensitive state
-> hand over control
-> update state too late
```

That is the bytecode essence of reentrancy.

## 11. Bottom Line

The practical lesson from the EVM side is simple:

- `SLOAD` before `CALL` is not automatically bad
- `CALL` before `SSTORE` is not automatically bad
- but `SLOAD(slot)` followed by an attacker-reachable external call, with the relevant `SSTORE(slot)` delayed until afterward, is the classic danger shape

That is exactly the pattern the vulnerable bank demonstrates and the scanner is built to highlight.
