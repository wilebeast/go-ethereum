# Custom Precompile Call Examples

This document shows how to exercise the custom precompile at `0x0000000000000000000000000000000000000123`.

Related files:

- [MatrixMulCaller.sol](~/docs/precompile/MatrixMulCaller.sol)
- [MatrixMulCaller.yul](~/docs/precompile/MatrixMulCaller.yul)
- [run_precompile_devnet.sh](~/scripts/run_precompile_devnet.sh)
- [precompile_native.go](~/core/vm/precompile_native.go)

## 1. Start A Local Devnet

Build the modified node:

```bash
CGO_ENABLED=1 make geth
```

Start the local chain:

```bash
bash scripts/run_precompile_devnet.sh
```

Defaults:

- RPC: `http://127.0.0.1:8545`
- WS: `ws://127.0.0.1:8546`
- dev block period: `30s`
- datadir: `./devdata/precompile-devnet`

## 2. Input Encoding

The precompile expects exactly 8 x 32-byte words:

```text
[a00|a01|a10|a11|b00|b01|b10|b11]
```

Example matrices:

```text
A = [[1, 2],
     [3, 4]]

B = [[5, 6],
     [7, 8]]
```

Expected result:

```text
C = [[19, 22],
     [43, 50]]
```

ABI-encoded input payload:

```text
0x
0000000000000000000000000000000000000000000000000000000000000001
0000000000000000000000000000000000000000000000000000000000000002
0000000000000000000000000000000000000000000000000000000000000003
0000000000000000000000000000000000000000000000000000000000000004
0000000000000000000000000000000000000000000000000000000000000005
0000000000000000000000000000000000000000000000000000000000000006
0000000000000000000000000000000000000000000000000000000000000007
0000000000000000000000000000000000000000000000000000000000000008
```

The return payload should be:

```text
0x
0000000000000000000000000000000000000000000000000000000000000013
0000000000000000000000000000000000000000000000000000000000000016
000000000000000000000000000000000000000000000000000000000000002b
0000000000000000000000000000000000000000000000000000000000000032
```

## 3. Direct `cast` Call To The Precompile

If `cast` is installed locally, call the precompile directly:

```bash
cast call 0x0000000000000000000000000000000000000123 \
  --rpc-url http://127.0.0.1:8545 \
  --data 0x00000000000000000000000000000000000000000000000000000000000000010000000000000000000000000000000000000000000000000000000000000002000000000000000000000000000000000000000000000000000000000000000300000000000000000000000000000000000000000000000000000000000000040000000000000000000000000000000000000000000000000000000000000005000000000000000000000000000000000000000000000000000000000000000600000000000000000000000000000000000000000000000000000000000000070000000000000000000000000000000000000000000000000000000000000008
```

Expected result:

```text
0x00000000000000000000000000000000000000000000000000000000000000130000000000000000000000000000000000000000000000000000000000000016000000000000000000000000000000000000000000000000000000000000002b0000000000000000000000000000000000000000000000000000000000000032
```

## 4. Direct `curl` Call To The Precompile

The same call over raw JSON-RPC:

```bash
curl -s http://127.0.0.1:8545 \
  -H 'Content-Type: application/json' \
  --data '{"jsonrpc":"2.0","id":1,"method":"eth_call","params":[{"to":"0x0000000000000000000000000000000000000123","data":"0x00000000000000000000000000000000000000000000000000000000000000010000000000000000000000000000000000000000000000000000000000000002000000000000000000000000000000000000000000000000000000000000000300000000000000000000000000000000000000000000000000000000000000040000000000000000000000000000000000000000000000000000000000000005000000000000000000000000000000000000000000000000000000000000000600000000000000000000000000000000000000000000000000000000000000070000000000000000000000000000000000000000000000000000000000000008"},"latest"]}'
```

## 5. Compile And Deploy The Solidity Wrapper

Compile:

```bash
solc --bin --abi docs/precompile/MatrixMulCaller.sol -o /tmp/matrixmul-caller
```

Find the dev signer account:

```bash
curl -s http://127.0.0.1:8545 \
  -H 'Content-Type: application/json' \
  --data '{"jsonrpc":"2.0","id":2,"method":"eth_accounts","params":[]}'
```

Deploy using the first returned account as `FROM`:

```bash
BYTECODE="$(tr -d '\n' < /tmp/matrixmul-caller/MatrixMulCaller.bin)"

curl -s http://127.0.0.1:8545 \
  -H 'Content-Type: application/json' \
  --data "{\"jsonrpc\":\"2.0\",\"id\":3,\"method\":\"eth_sendTransaction\",\"params\":[{\"from\":\"FROM\",\"data\":\"0x${BYTECODE}\",\"gas\":\"0x47b760\"}]}"
```

Wait for a block, then fetch the receipt and deployed contract address from the transaction hash.

## 6. Call The Solidity Wrapper With `cast`

Assume the deployed wrapper address is `CALLER`.

Call the structured helper:

```bash
cast call CALLER \
  "multiplyWords(uint256,uint256,uint256,uint256,uint256,uint256,uint256,uint256)(uint256,uint256,uint256,uint256)" \
  1 2 3 4 5 6 7 8 \
  --rpc-url http://127.0.0.1:8545
```

Expected decoded result:

```text
19
22
43
50
```

## 7. Call The Solidity Wrapper With `curl`

The selector for `multiplyWords(uint256,uint256,uint256,uint256,uint256,uint256,uint256,uint256)` can be generated with:

```bash
cast sig "multiplyWords(uint256,uint256,uint256,uint256,uint256,uint256,uint256,uint256)"
```

Example payload layout:

```text
0x<4-byte-selector>
000...001
000...002
000...003
000...004
000...005
000...006
000...007
000...008
```

Then call:

```bash
curl -s http://127.0.0.1:8545 \
  -H 'Content-Type: application/json' \
  --data '{"jsonrpc":"2.0","id":4,"method":"eth_call","params":[{"to":"CALLER","data":"0x<selector_and_args>"},"latest"]}'
```

## 8. Yul Wrapper

The Yul wrapper in [MatrixMulCaller.yul](~/docs/precompile/MatrixMulCaller.yul):

- copies calldata to memory
- forwards it with `staticcall(gas(), 0x0123, ...)`
- returns exactly `0x80` bytes on success

This is useful if you want a minimal no-Solidity surface for experiments.

Compile it with `solc --standard-json`:

```bash
cat <<'EOF' | solc --standard-json
{
  "language": "Yul",
  "sources": {
    "MatrixMulCaller.yul": {
      "content": "object \"MatrixMulCaller\" { code { datacopy(0, dataoffset(\"runtime\"), datasize(\"runtime\")) return(0, datasize(\"runtime\")) } object \"runtime\" { code { let size := calldatasize() let ptr := mload(0x40) calldatacopy(ptr, 0, size) if iszero(staticcall(gas(), 0x0123, ptr, size, ptr, 0x80)) { revert(0, 0) } return(ptr, 0x80) } } }"
    }
  },
  "settings": {
    "outputSelection": {
      "*": {
        "*": ["evm.bytecode.object"]
      }
    }
  }
}
EOF
```

This route is compatible with the local `solc-js` wrapper used elsewhere in the repository.
