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


def compile_contracts(repo_root: pathlib.Path, out_dir: pathlib.Path) -> dict[str, str]:
    source_dir = repo_root / "docs" / "reentrancy"
    subprocess.run(
        ["solc", "--bin", str(source_dir / "VulnerableBank.sol"), str(source_dir / "ReentrancyAttacker.sol"), "-o", str(out_dir)],
        check=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
    )
    artifacts = {}
    for path in out_dir.glob("*.bin"):
        name = path.stem.split("_")[-1]
        artifacts[name] = path.read_text().strip()
    required = {"VulnerableBank", "ReentrancyAttacker"}
    missing = required.difference(artifacts)
    if missing:
        raise RuntimeError(f"missing compiled artifacts: {sorted(missing)}")
    return artifacts


def pad32_hex(value: int) -> str:
    return f"{value:064x}"


def encode_address_arg(address: str) -> str:
    return address.lower().removeprefix("0x").rjust(64, "0")


def selector(rpc_url: str, signature: str) -> str:
    sig_hex = "0x" + signature.encode().hex()
    digest = rpc(rpc_url, "web3_sha3", [sig_hex])
    return digest[2:10]


def wait_for_receipt(rpc_url: str, tx_hash: str, timeout: float) -> object:
    deadline = time.time() + timeout
    while time.time() < deadline:
        receipt = rpc(rpc_url, "eth_getTransactionReceipt", [tx_hash])
        if receipt is not None:
            return receipt
        time.sleep(1)
    raise TimeoutError(f"timed out waiting for receipt: {tx_hash}")


def send_tx(rpc_url: str, tx: dict) -> object:
    return rpc(rpc_url, "eth_sendTransaction", [tx])


def main() -> int:
    parser = argparse.ArgumentParser(description="Deploy and exploit the reentrancy demo, then trace the attack transaction.")
    parser.add_argument("--rpc", default="http://127.0.0.1:8545")
    parser.add_argument("--from", dest="from_addr", help="deployer/funder account; defaults to first eth_accounts result")
    parser.add_argument("--attack-value", type=int, default=10**18, help="wei sent into attacker.attack")
    parser.add_argument("--fund-value", type=int, default=5 * 10**18, help="wei deposited directly into the bank before attack")
    parser.add_argument("--reentries", type=int, default=2, help="max recursive withdraw depth")
    parser.add_argument("--timeout", type=float, default=90.0)
    parser.add_argument("--out", default="", help="trace output path; defaults to /tmp/reentrancy-attack-trace.json")
    args = parser.parse_args()

    repo_root = pathlib.Path(__file__).resolve().parent.parent
    out_dir = pathlib.Path("/tmp/reentrancy-lab-build")
    out_dir.mkdir(parents=True, exist_ok=True)
    artifacts = compile_contracts(repo_root, out_dir)

    deployer = args.from_addr or rpc(args.rpc, "eth_accounts", [])[0]

    bank_tx = send_tx(args.rpc, {"from": deployer, "data": "0x" + artifacts["VulnerableBank"], "gas": hex(3_000_000)})
    bank_receipt = wait_for_receipt(args.rpc, bank_tx, args.timeout)
    bank_addr = bank_receipt["contractAddress"]

    deposit_selector = selector(args.rpc, "deposit()")
    send_tx(
        args.rpc,
        {
            "from": deployer,
            "to": bank_addr,
            "data": "0x" + deposit_selector,
            "value": hex(args.fund_value),
            "gas": hex(300_000),
        },
    )

    attacker_bytecode = artifacts["ReentrancyAttacker"] + encode_address_arg(bank_addr)
    attacker_tx = send_tx(args.rpc, {"from": deployer, "data": "0x" + attacker_bytecode, "gas": hex(3_000_000)})
    attacker_receipt = wait_for_receipt(args.rpc, attacker_tx, args.timeout)
    attacker_addr = attacker_receipt["contractAddress"]

    attack_selector = selector(args.rpc, "attack(uint256)")
    attack_data = "0x" + attack_selector + pad32_hex(args.reentries)
    attack_tx = send_tx(
        args.rpc,
        {
            "from": deployer,
            "to": attacker_addr,
            "data": attack_data,
            "value": hex(args.attack_value),
            "gas": hex(2_000_000),
        },
    )
    wait_for_receipt(args.rpc, attack_tx, args.timeout)

    trace = rpc(
        args.rpc,
        "debug_traceTransaction",
        [
            attack_tx,
            {
                "enableMemory": False,
                "disableStack": False,
                "disableStorage": False,
                "enableReturnData": False,
            },
        ],
    )
    trace_path = pathlib.Path(args.out) if args.out else pathlib.Path("/tmp/reentrancy-attack-trace.json")
    trace_path.write_text(json.dumps(trace, indent=2))

    result = {
        "deployer": deployer,
        "bank": bank_addr,
        "attacker": attacker_addr,
        "attackTx": attack_tx,
        "tracePath": str(trace_path),
    }
    print(json.dumps(result, indent=2))
    return 0


if __name__ == "__main__":
    sys.exit(main())
