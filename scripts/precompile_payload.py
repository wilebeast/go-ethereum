#!/usr/bin/env python3

import argparse


def word(value: int) -> str:
    if value < 0:
        raise ValueError("matrix elements must be non-negative")
    if value > (1 << 64) - 1:
        raise ValueError("matrix elements must fit into uint64")
    return f"{value:064x}"


def main() -> None:
    parser = argparse.ArgumentParser(description="Build a direct-call payload for the 0x0123 matrix-mul precompile.")
    parser.add_argument("a00", type=int)
    parser.add_argument("a01", type=int)
    parser.add_argument("a10", type=int)
    parser.add_argument("a11", type=int)
    parser.add_argument("b00", type=int)
    parser.add_argument("b01", type=int)
    parser.add_argument("b10", type=int)
    parser.add_argument("b11", type=int)
    args = parser.parse_args()

    values = [args.a00, args.a01, args.a10, args.a11, args.b00, args.b01, args.b10, args.b11]
    print("0x" + "".join(word(v) for v in values))


if __name__ == "__main__":
    main()
