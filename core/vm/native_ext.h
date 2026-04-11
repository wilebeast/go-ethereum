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
