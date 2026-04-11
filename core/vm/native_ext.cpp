#include "native_ext.h"

#include <cstddef>
#include <cstdint>

namespace {

bool read_u64_word(const uint8_t* word, uint64_t* out) {
    for (size_t i = 0; i < 24; ++i) {
        if (word[i] != 0) {
            return false;
        }
    }
    uint64_t value = 0;
    for (size_t i = 24; i < 32; ++i) {
        value = (value << 8) | static_cast<uint64_t>(word[i]);
    }
    *out = value;
    return true;
}

void write_u64_word(uint8_t* word, uint64_t value) {
    for (size_t i = 0; i < 24; ++i) {
        word[i] = 0;
    }
    for (size_t i = 0; i < 8; ++i) {
        word[31 - i] = static_cast<uint8_t>(value & 0xff);
        value >>= 8;
    }
}

}  // namespace

extern "C" int native_matrix_mul_2x2(const uint8_t* input, size_t input_len, uint8_t* out, size_t out_len) {
    if (input_len != 256 || out_len != 128) {
        return 1;
    }

    uint64_t values[8];
    for (size_t i = 0; i < 8; ++i) {
        if (!read_u64_word(input + i * 32, &values[i])) {
            return 2;
        }
    }

    const uint64_t a00 = values[0];
    const uint64_t a01 = values[1];
    const uint64_t a10 = values[2];
    const uint64_t a11 = values[3];
    const uint64_t b00 = values[4];
    const uint64_t b01 = values[5];
    const uint64_t b10 = values[6];
    const uint64_t b11 = values[7];

    const uint64_t c00 = a00 * b00 + a01 * b10;
    const uint64_t c01 = a00 * b01 + a01 * b11;
    const uint64_t c10 = a10 * b00 + a11 * b10;
    const uint64_t c11 = a10 * b01 + a11 * b11;

    write_u64_word(out + 0 * 32, c00);
    write_u64_word(out + 1 * 32, c01);
    write_u64_word(out + 2 * 32, c10);
    write_u64_word(out + 3 * 32, c11);
    return 0;
}
