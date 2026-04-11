#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
GETH_BIN="${GETH_BIN:-$ROOT_DIR/build/bin/geth}"
DATA_DIR="${DATA_DIR:-$ROOT_DIR/devdata/precompile-devnet}"
HTTP_ADDR="${HTTP_ADDR:-127.0.0.1}"
HTTP_PORT="${HTTP_PORT:-8545}"
WS_PORT="${WS_PORT:-8546}"
DEV_PERIOD="${DEV_PERIOD:-30}"
VERBOSITY="${VERBOSITY:-3}"

if [[ ! -x "$GETH_BIN" ]]; then
  echo "missing geth binary: $GETH_BIN" >&2
  echo "build it first with: CGO_ENABLED=1 make geth" >&2
  exit 1
fi

mkdir -p "$DATA_DIR"

exec "$GETH_BIN" \
  --dev \
  --dev.period "$DEV_PERIOD" \
  --datadir "$DATA_DIR" \
  --http \
  --http.addr "$HTTP_ADDR" \
  --http.port "$HTTP_PORT" \
  --http.api eth,net,web3,debug,txpool,miner \
  --ws \
  --ws.addr "$HTTP_ADDR" \
  --ws.port "$WS_PORT" \
  --ws.api eth,net,web3,debug,txpool,miner \
  --allow-insecure-unlock \
  --verbosity "$VERBOSITY"
