#!/usr/bin/env python3

import argparse
import json
import pathlib
import subprocess
import sys
import time
import urllib.request


def rpc(url: str, method: str, params: list) -> object:
    payload = json.dumps({"jsonrpc": "2.0", "id": 1, "method": method, "params": params}).encode()
    req = urllib.request.Request(url, data=payload, headers={"Content-Type": "application/json"})
    with urllib.request.urlopen(req) as resp:
        body = json.loads(resp.read().decode())
    if "error" in body:
        raise RuntimeError(f"{method} failed: {body['error']}")
    return body["result"]


def compile_wrapper(repo_root: pathlib.Path, output_dir: pathlib.Path) -> str:
    source = repo_root / "docs" / "precompile" / "MatrixMulCaller.sol"
    subprocess.run(
        ["solc", "--bin", "--abi", str(source), "-o", str(output_dir)],
        check=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
    )
    candidates = sorted(output_dir.glob("*.bin"))
    if not candidates:
        raise FileNotFoundError(f"no .bin artifact found in {output_dir}")
    if len(candidates) != 1:
        raise RuntimeError(f"expected exactly one .bin artifact in {output_dir}, found {len(candidates)}")
    bytecode_path = candidates[0]
    return bytecode_path.read_text().strip()


def wait_for_receipt(rpc_url: str, tx_hash: str, timeout: float) -> object:
    deadline = time.time() + timeout
    while time.time() < deadline:
        receipt = rpc(rpc_url, "eth_getTransactionReceipt", [tx_hash])
        if receipt is not None:
            return receipt
        time.sleep(1)
    raise TimeoutError(f"timed out waiting for receipt: {tx_hash}")


def main() -> int:
    parser = argparse.ArgumentParser(description="Compile and deploy MatrixMulCaller.sol to a local geth devnet.")
    parser.add_argument("--rpc", default="http://127.0.0.1:8545", help="JSON-RPC endpoint")
    parser.add_argument("--from", dest="from_addr", help="deployer account; defaults to first eth_accounts result")
    parser.add_argument("--gas", default="0x47b760", help="gas limit for deployment")
    parser.add_argument("--timeout", type=float, default=90.0, help="receipt wait timeout in seconds")
    args = parser.parse_args()

    repo_root = pathlib.Path(__file__).resolve().parent.parent
    output_dir = pathlib.Path("/tmp/matrixmul-caller")
    output_dir.mkdir(parents=True, exist_ok=True)

    bytecode = compile_wrapper(repo_root, output_dir)
    deployer = args.from_addr or rpc(args.rpc, "eth_accounts", [])[0]

    tx_hash = rpc(
        args.rpc,
        "eth_sendTransaction",
        [{"from": deployer, "data": "0x" + bytecode, "gas": args.gas}],
    )
    receipt = wait_for_receipt(args.rpc, tx_hash, args.timeout)

    result = {
        "deployer": deployer,
        "txHash": tx_hash,
        "contractAddress": receipt["contractAddress"],
        "gasUsed": receipt.get("gasUsed"),
    }
    print(json.dumps(result, indent=2))
    return 0


if __name__ == "__main__":
    sys.exit(main())
