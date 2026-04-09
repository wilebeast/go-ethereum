#!/usr/bin/env python3
"""Send many low-gas-price transactions to a local development geth node.

This script is intended for private devnets only. It uses eth_sendTransaction,
so the sender account must be unlocked on the target node.
"""

import argparse
import json
import urllib.request


def rpc(url, method, params, request_id):
    body = json.dumps({
        "jsonrpc": "2.0",
        "id": request_id,
        "method": method,
        "params": params,
    }).encode()
    request = urllib.request.Request(url, data=body, headers={"Content-Type": "application/json"})
    with urllib.request.urlopen(request, timeout=10) as response:
        payload = json.loads(response.read().decode())
    if "error" in payload:
        raise RuntimeError(payload["error"])
    return payload["result"]


def main():
    parser = argparse.ArgumentParser(description="Flood a dev geth txpool with low-gas-price transactions")
    parser.add_argument("--rpc", default="http://127.0.0.1:8545", help="HTTP JSON-RPC endpoint")
    parser.add_argument("--from", dest="sender", required=True, help="Unlocked sender address")
    parser.add_argument("--to", required=True, help="Recipient address")
    parser.add_argument("--count", type=int, default=200, help="Number of transactions to send")
    parser.add_argument("--gas-price", default="0x1", help="Gas price in wei, hex string")
    parser.add_argument("--gas", default="0x5208", help="Gas limit, hex string")
    parser.add_argument("--value", default="0x0", help="Value in wei, hex string")
    args = parser.parse_args()

    start_nonce = int(rpc(args.rpc, "eth_getTransactionCount", [args.sender, "pending"], 1), 16)
    accepted = 0
    for i in range(args.count):
        tx = {
            "from": args.sender,
            "to": args.to,
            "gas": args.gas,
            "gasPrice": args.gas_price,
            "value": args.value,
            "nonce": hex(start_nonce + i),
        }
        try:
            tx_hash = rpc(args.rpc, "eth_sendTransaction", [tx], i + 2)
            accepted += 1
            print(f"{i:05d} accepted {tx_hash}")
        except Exception as err:
            print(f"{i:05d} rejected {err}")

    status = rpc(args.rpc, "txpool_status", [], args.count + 2)
    print(f"accepted={accepted} txpool_status={status}")


if __name__ == "__main__":
    main()
