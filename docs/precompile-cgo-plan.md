# Custom Precompile With CGO: Corrected Plan For This Repository

This document is a corrected implementation plan for adding a custom precompiled contract to the current repository version.

It addresses four things explicitly:

1. new address selection
2. correct `PrecompiledContract` implementation
3. correct registration position
4. correct CGO file layout and build instructions

Related source:

- [contracts.go](../core/vm/contracts.go)
- [evm.go](../core/vm/evm.go)
- [params](../params)

## 1. Why The Earlier Plan Was Not Safe

The earlier sketch had four critical problems against the current codebase:

1. it reused `0x05`, which is already `bigModExp`
2. it used old method names like `requiredGas` / `run`
3. it registered only in one fork map
4. it suggested a CGO library placement that does not match `#cgo LDFLAGS: -L.`

Current interface in [contracts.go](../core/vm/contracts.go#L50):

```go
type PrecompiledContract interface {
    RequiredGas(input []byte) uint64
    Run(input []byte) ([]byte, error)
    Name() string
}
```

So any implementation plan must match that exact interface.

## 2. Address Selection

### 2.1 What Is Already Occupied

Current built-in addresses:

- Homestead: `0x01` to `0x04`
- Byzantium/Istanbul/Berlin: `0x01` to `0x09`
- Cancun: `0x01` to `0x0a`
- Prague: `0x01` to `0x11`
- Osaka: `0x01` to `0x11`, plus `0x0100`

References:

- [contracts.go](../core/vm/contracts.go#L61)
- [contracts.go](../core/vm/contracts.go#L70)
- [contracts.go](../core/vm/contracts.go#L83)
- [contracts.go](../core/vm/contracts.go#L97)
- [contracts.go](../core/vm/contracts.go#L111)
- [contracts.go](../core/vm/contracts.go#L126)
- [contracts.go](../core/vm/contracts.go#L152)

### 2.2 Recommended New Address

Use:

```text
0x0000000000000000000000000000000000000123
```

In Go:

```go
var addrNativeExt = common.BytesToAddress([]byte{0x01, 0x23})
```

Why:

- it does not collide with existing `0x01` to `0x11`
- it does not collide with Osaka `0x0100`
- it is still easy to recognize in tests and demos

Do not use:

- `0x05`
- `0x09`
- `0x0a`
- `0x0100`

because they are already assigned in this repository.

## 3. Correct PrecompiledContract Implementation

### 3.1 Go Surface

The new precompile should look like this:

```go
type nativeMatrixMul struct{}

func (p *nativeMatrixMul) RequiredGas(input []byte) uint64 {
    // Example policy only.
    // Real formula must scale with actual input size and algorithm cost.
    words := uint64((len(input) + 31) / 32)
    return 5000 + words*300
}

func (p *nativeMatrixMul) Run(input []byte) ([]byte, error) {
    return runNativeMatrixMul(input)
}

func (p *nativeMatrixMul) Name() string {
    return "NATIVE_MATRIX_MUL"
}
```

### 3.2 Why This Shape

- `RequiredGas`
  - must be deterministic from input only
  - must not depend on wall clock or host CPU speed
- `Run`
  - executes the actual native logic
- `Name`
  - required by current repository interface

### 3.3 Recommended Input Contract

For a first CGO-backed precompile, keep the ABI small and fixed-width.

Example:

```text
input = [a00|a01|a10|a11|b00|b01|b10|b11]
```

Where each entry is a 32-byte big-endian unsigned integer.

Then:

- parse 8 x 32-byte words
- compute 2x2 matrix multiplication
- return 4 x 32-byte words

This is much better than a vague “hash-like demo” because:

- input size is explicit
- output size is explicit
- gas policy can be tied to a real computation shape

## 4. Correct CGO Layout

### 4.1 Recommended Files

Put the native integration directly under [core/vm](../core/vm), because the precompile implementation also lives there.

Recommended layout:

```text
core/vm/
├── contracts.go
├── precompile_native.go
├── native/
│   ├── native_ext.h
│   ├── native_ext.cpp
│   └── libnative.a
```

### 4.2 Why This Layout

If `precompile_native.go` is inside `core/vm`, then:

- `#cgo CFLAGS` and `#cgo LDFLAGS` should be written relative to `core/vm`
- using `-L${SRCDIR}/native` is stable
- copying the `.a` file to repo root is unnecessary and misleading

### 4.3 Correct CGO Header

Example:

```go
package vm

/*
#cgo CXXFLAGS: -O3 -std=c++17
#cgo LDFLAGS: -L${SRCDIR}/native -lnative -lstdc++
#include <stdint.h>
#include <stdlib.h>
#include "native/native_ext.h"
*/
import "C"

import (
    "errors"
    "unsafe"
)
```

Important detail:

- use `${SRCDIR}` instead of `-L.`
- `${SRCDIR}` resolves to the directory containing the Go file
- that makes the build stable from any working directory

### 4.4 Native Header

Example [native_ext.h](../core/vm/native/native_ext.h):

```c
#pragma once

#include <stddef.h>
#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

int native_matrix_mul_2x2(const uint8_t* input, size_t input_len, uint8_t* out, size_t out_len);

#ifdef __cplusplus
}
#endif
```

### 4.5 Native C++ Implementation

Example [native_ext.cpp](../core/vm/native/native_ext.cpp):

```cpp
#include "native_ext.h"

#include <array>
#include <cstring>

namespace {
uint64_t load_u64_word(const uint8_t* p) {
    uint64_t v = 0;
    for (int i = 24; i < 32; ++i) {
        v = (v << 8) | p[i];
    }
    return v;
}

void store_u64_word(uint64_t v, uint8_t* out) {
    std::memset(out, 0, 32);
    for (int i = 31; i >= 24; --i) {
        out[i] = static_cast<uint8_t>(v & 0xff);
        v >>= 8;
    }
}
}

extern "C" int native_matrix_mul_2x2(const uint8_t* input, size_t input_len, uint8_t* out, size_t out_len) {
    if (input_len != 8 * 32 || out_len != 4 * 32) {
        return 1;
    }
    uint64_t a00 = load_u64_word(input + 0*32);
    uint64_t a01 = load_u64_word(input + 1*32);
    uint64_t a10 = load_u64_word(input + 2*32);
    uint64_t a11 = load_u64_word(input + 3*32);
    uint64_t b00 = load_u64_word(input + 4*32);
    uint64_t b01 = load_u64_word(input + 5*32);
    uint64_t b10 = load_u64_word(input + 6*32);
    uint64_t b11 = load_u64_word(input + 7*32);

    uint64_t c00 = a00*b00 + a01*b10;
    uint64_t c01 = a00*b01 + a01*b11;
    uint64_t c10 = a10*b00 + a11*b10;
    uint64_t c11 = a10*b01 + a11*b11;

    store_u64_word(c00, out + 0*32);
    store_u64_word(c01, out + 1*32);
    store_u64_word(c10, out + 2*32);
    store_u64_word(c11, out + 3*32);
    return 0;
}
```

This is only a demonstration kernel. The important part is the CGO boundary shape, not the math itself.

## 5. Correct Go Bridge

Suggested file: [precompile_native.go](../core/vm/precompile_native.go)

```go
package vm

/*
#cgo CXXFLAGS: -O3 -std=c++17
#cgo LDFLAGS: -L${SRCDIR}/native -lnative -lstdc++
#include <stdint.h>
#include <stdlib.h>
#include "native/native_ext.h"
*/
import "C"

import (
    "errors"
    "unsafe"
)

var errNativeInput = errors.New("invalid native precompile input")
var errNativeExec = errors.New("native precompile execution failed")

type nativeMatrixMul struct{}

func (p *nativeMatrixMul) RequiredGas(input []byte) uint64 {
    if len(input) != 8*32 {
        return 5000
    }
    return 5000 + 8*300
}

func (p *nativeMatrixMul) Run(input []byte) ([]byte, error) {
    if len(input) != 8*32 {
        return nil, errNativeInput
    }
    out := make([]byte, 4*32)
    var inPtr *C.uint8_t
    if len(input) > 0 {
        inPtr = (*C.uint8_t)(unsafe.Pointer(&input[0]))
    }
    rc := C.native_matrix_mul_2x2(
        inPtr,
        C.size_t(len(input)),
        (*C.uint8_t)(unsafe.Pointer(&out[0])),
        C.size_t(len(out)),
    )
    if rc != 0 {
        return nil, errNativeExec
    }
    return out, nil
}

func (p *nativeMatrixMul) Name() string {
    return "NATIVE_MATRIX_MUL"
}
```

Important corrections versus the earlier answer:

- method names must be exported exactly as `RequiredGas`, `Run`, `Name`
- no extra `malloc` is needed for the output slice
- the link path must use `${SRCDIR}/native`
- return explicit Go errors on malformed input

## 6. Correct Registration Position

### 6.1 Do Not Register In A Separate `init()`

Do not do:

```go
func init() {
    PrecompiledContractsIstanbul[addrNativeExt] = &nativeMatrixMul{}
}
```

Why:

- current repository precomputes `PrecompiledAddresses*` slices in `contracts.go`
- mutating only one map later can desynchronize `ActivePrecompiles()`
- current active fork may not be `Istanbul`

### 6.2 Correct Registration Strategy

Modify [contracts.go](../core/vm/contracts.go) directly.

Pick one address constant:

```go
var addrNativeExt = common.BytesToAddress([]byte{0x01, 0x23})
```

Then insert the precompile into the fork sets you want to support.

For a modern custom devnet, the practical choice is:

- `PrecompiledContractsCancun`
- `PrecompiledContractsPrague`
- `PrecompiledContractsOsaka`

Example:

```go
var PrecompiledContractsCancun = PrecompiledContracts{
    // existing entries...
    common.BytesToAddress([]byte{0x01, 0x23}): &nativeMatrixMul{},
}
```

and likewise for Prague and Osaka.

Then update the precompiled address lists by extending the existing `init()` in [contracts.go](../core/vm/contracts.go#L190), or simply rely on the fact that the lists are rebuilt from the updated maps in that same file at init time.

This is why the registration must live in `contracts.go` itself, not in an extra file-level `init()`.

## 7. Build Instructions

### 7.1 Build The Native Library

From repository root:

```bash
g++ -c -O3 -std=c++17 core/vm/native/native_ext.cpp -o core/vm/native/native_ext.o
ar rcs core/vm/native/libnative.a core/vm/native/native_ext.o
```

### 7.2 Build Geth With CGO Enabled

```bash
CGO_ENABLED=1 make geth
```

If needed:

```bash
CGO_ENABLED=1 go build ./cmd/geth
```

### 7.3 Optional `.gitignore`

The static library and object file should be ignored.

Add:

```text
core/vm/native/*.o
core/vm/native/*.a
```

to [.gitignore](../.gitignore) in the repository root.

## 8. Contract Call Example

Solidity example:

```solidity
pragma solidity ^0.8.20;

contract TestNativeCaller {
    address constant NATIVE_PRECOMPILE = 0x0000000000000000000000000000000000000123;

    function callNative(bytes memory data) external view returns (bytes memory) {
        (bool ok, bytes memory out) = NATIVE_PRECOMPILE.staticcall(data);
        require(ok, "native precompile failed");
        return out;
    }
}
```

Input encoding for the 2x2 example:

- 8 words
- 32 bytes each
- total `256` bytes

Output:

- 4 words
- 32 bytes each
- total `128` bytes

## 9. Workflow Chart

```text
Solidity/Yul caller
   |
   v
CALL / STATICCALL to 0x...0123
   |
   v
EVM checks active precompile map
   |
   v
nativeMatrixMul.RequiredGas(input)
   |
   v
nativeMatrixMul.Run(input)
   |
   v
CGO bridge in precompile_native.go
   |
   v
native_matrix_mul_2x2(...) in C++
   |
   v
result bytes returned to Go
   |
   v
result bytes returned to EVM caller
```

## 10. Security And Engineering Notes

1. Gas must be conservative
   - especially if the native algorithm can scale with input size

2. Input validation must be strict
   - malformed native input must return explicit errors

3. Determinism is mandatory
   - no host-dependent randomness
   - no wall-clock dependence
   - no floating-point behavior differences

4. Crash safety matters
   - a C++ crash can take down the node
   - fuzz and sanitize the native boundary

5. Start with a small fixed-width algorithm
   - matrix multiply is easier to audit than an unbounded parser

## 11. Recommended Implementation Order

1. add `native_ext.h/.cpp`
2. build `libnative.a`
3. add `precompile_native.go`
4. register the new address in `contracts.go`
5. update `.gitignore`
6. build geth with `CGO_ENABLED=1`
7. deploy a test contract calling `0x...0123`
8. add tests in `core/vm/contracts_test.go`

## 12. Bottom Line

For this repository version, a correct custom precompile plan is:

- address: use `0x...0123`, not `0x05`
- interface: implement `RequiredGas`, `Run`, `Name`
- registration: edit the actual fork maps in [contracts.go](../core/vm/contracts.go)
- CGO layout: keep native files under `core/vm/native` and link with `${SRCDIR}/native`

That is the minimum shape required for a solution that is actually compatible with the current codebase.
