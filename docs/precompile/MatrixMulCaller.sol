// SPDX-License-Identifier: GPL-3.0
pragma solidity ^0.8.24;

/// @title MatrixMulCaller
/// @notice Minimal Solidity wrapper around the custom precompile at 0x...0123.
/// @dev The precompile expects 8 x 32-byte words and returns 4 x 32-byte words.
contract MatrixMulCaller {
    address internal constant PRECOMPILE = 0x0000000000000000000000000000000000000123;

    error NativeMatrixMulFailed();

    function multiply(bytes calldata input) external view returns (bytes memory output) {
        (bool ok, bytes memory result) = PRECOMPILE.staticcall(input);
        if (!ok) revert NativeMatrixMulFailed();
        return result;
    }

    function multiplyWords(
        uint256 a00,
        uint256 a01,
        uint256 a10,
        uint256 a11,
        uint256 b00,
        uint256 b01,
        uint256 b10,
        uint256 b11
    ) external view returns (uint256 c00, uint256 c01, uint256 c10, uint256 c11) {
        bytes memory input = abi.encode(a00, a01, a10, a11, b00, b01, b10, b11);
        (bool ok, bytes memory result) = PRECOMPILE.staticcall(input);
        if (!ok) revert NativeMatrixMulFailed();
        return abi.decode(result, (uint256, uint256, uint256, uint256));
    }
}
