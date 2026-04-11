package vm

/*
#cgo CXXFLAGS: -O3 -std=c++17
#cgo LDFLAGS: -lstdc++
#include <stdint.h>
#include <stdlib.h>
#include "native_ext.h"
*/
import "C"

import (
	"errors"
	"unsafe"

	"github.com/ethereum/go-ethereum/common"
)

const (
	nativeMatrixMulInputSize  = 8 * 32
	nativeMatrixMulOutputSize = 4 * 32
	nativeMatrixMulBaseGas    = 5000
	nativeMatrixMulPerWordGas = 300
)

var (
	addrNativeMatrixMul = common.BytesToAddress([]byte{0x01, 0x23})

	errNativeMatrixMulInvalidInputLength = errors.New("invalid input length")
	errNativeMatrixMulInvalidWordSize    = errors.New("matrix element exceeds uint64")
	errNativeMatrixMulNativeFailure      = errors.New("native execution failure")
)

// nativeMatrixMul is a CGO-backed 2x2 matrix multiplication precompile.
//
// The input format is exactly eight 32-byte words:
// [a00|a01|a10|a11|b00|b01|b10|b11]
//
// Each word must fit into uint64, which keeps the native implementation small
// and makes the test vectors easy to reason about. The output is four 32-byte
// words [c00|c01|c10|c11], left-padded to EVM word width.
type nativeMatrixMul struct{}

func (p *nativeMatrixMul) RequiredGas(input []byte) uint64 {
	words := uint64((len(input) + 31) / 32)
	return nativeMatrixMulBaseGas + words*nativeMatrixMulPerWordGas
}

func (p *nativeMatrixMul) Run(input []byte) ([]byte, error) {
	if len(input) != nativeMatrixMulInputSize {
		return nil, errNativeMatrixMulInvalidInputLength
	}
	out := make([]byte, nativeMatrixMulOutputSize)
	status := C.native_matrix_mul_2x2(
		(*C.uint8_t)(unsafe.Pointer(&input[0])),
		C.size_t(len(input)),
		(*C.uint8_t)(unsafe.Pointer(&out[0])),
		C.size_t(len(out)),
	)
	switch status {
	case 0:
		return out, nil
	case 1:
		return nil, errNativeMatrixMulInvalidInputLength
	case 2:
		return nil, errNativeMatrixMulInvalidWordSize
	default:
		return nil, errNativeMatrixMulNativeFailure
	}
}

func (p *nativeMatrixMul) Name() string {
	return "NATIVE_MATRIX_MUL_2X2"
}
