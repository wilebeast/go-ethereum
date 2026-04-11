# Custom Precompile Lab

This is the shortest end-to-end path from source tree to a successful precompile call.

Related files:

- [run_precompile_devnet.sh](~/scripts/run_precompile_devnet.sh)
- [deploy_precompile_wrapper.py](~/scripts/deploy_precompile_wrapper.py)
- [precompile_payload.py](~/scripts/precompile_payload.py)
- [precompile-call-examples.md](~/docs/precompile-call-examples.md)

## 1. Build The Modified Node

```bash
CGO_ENABLED=1 make geth
```

## 2. Start The Local Chain

```bash
bash scripts/run_precompile_devnet.sh
```

Leave this process running in its own terminal.

## 3. Generate A Direct-Call Payload

Example for:

```text
A = [[1, 2],
     [3, 4]]

B = [[5, 6],
     [7, 8]]
```

Run:

```bash
python3 scripts/precompile_payload.py 1 2 3 4 5 6 7 8
```

Expected output:

```text
0x00000000000000000000000000000000000000000000000000000000000000010000000000000000000000000000000000000000000000000000000000000002000000000000000000000000000000000000000000000000000000000000000300000000000000000000000000000000000000000000000000000000000000040000000000000000000000000000000000000000000000000000000000000005000000000000000000000000000000000000000000000000000000000000000600000000000000000000000000000000000000000000000000000000000000070000000000000000000000000000000000000000000000000000000000000008
```

## 4. Call The Precompile Directly

```bash
PAYLOAD="$(python3 scripts/precompile_payload.py 1 2 3 4 5 6 7 8)"

curl -s http://127.0.0.1:8545 \
  -H 'Content-Type: application/json' \
  --data "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"eth_call\",\"params\":[{\"to\":\"0x0000000000000000000000000000000000000123\",\"data\":\"${PAYLOAD}\"},\"latest\"]}"
```

Expected result:

```text
0x00000000000000000000000000000000000000000000000000000000000000130000000000000000000000000000000000000000000000000000000000000016000000000000000000000000000000000000000000000000000000000000002b0000000000000000000000000000000000000000000000000000000000000032
```

Decoded:

```text
19, 22, 43, 50
```

## 5. Deploy The Solidity Wrapper

```bash
python3 scripts/deploy_precompile_wrapper.py
```

Example output:

```json
{
  "deployer": "0x...",
  "txHash": "0x...",
  "contractAddress": "0x...",
  "gasUsed": "0x..."
}
```

Record the returned `contractAddress` as `CALLER`.

## 6. Call The Wrapper Contract

If `cast` is available:

```bash
cast call CALLER \
  "multiplyWords(uint256,uint256,uint256,uint256,uint256,uint256,uint256,uint256)(uint256,uint256,uint256,uint256)" \
  1 2 3 4 5 6 7 8 \
  --rpc-url http://127.0.0.1:8545
```

If not, use raw `eth_call` with the encoded selector and arguments described in [precompile-call-examples.md](../docs/precompile-call-examples.md).

## 7. What This Validates

At this point you have validated three layers:

1. the precompile is registered in the EVM
2. direct `eth_call` to `0x...0123` reaches the native backend
3. a Solidity contract can `staticcall` the same precompile address successfully

## 8. Failure Cases To Try

1. Short input

```bash
curl -s http://127.0.0.1:8545 \
  -H 'Content-Type: application/json' \
  --data '{"jsonrpc":"2.0","id":2,"method":"eth_call","params":[{"to":"0x0000000000000000000000000000000000000123","data":"0x01"},"latest"]}'
```

Expected: call failure because the input is not `256` bytes.

2. Oversized word

Use an input where one 32-byte word has non-zero bytes above the low 64 bits. The native code rejects it and the call should fail.
