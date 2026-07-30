#ifndef VIPR_BLAKE3_BACKEND_H
#define VIPR_BLAKE3_BACKEND_H

#include <stddef.h>
#include <stdint.h>

#ifdef VIPR_STATIC_BLAKE3
#include <blake3.h>
#define VIPR_BLAKE3_BACKEND_NAME "official-" BLAKE3_VERSION_STRING
typedef blake3_hasher vipr_blake3_hasher;
static inline void vipr_blake3_init(vipr_blake3_hasher *hasher) {
    blake3_hasher_init(hasher);
}
static inline void vipr_blake3_update(vipr_blake3_hasher *hasher, const void *input, size_t input_len) {
    blake3_hasher_update(hasher, input, input_len);
}
static inline void vipr_blake3_finalize(const vipr_blake3_hasher *hasher, uint8_t out[32]) {
    blake3_hasher_finalize(hasher, out, 32);
}
static inline void vipr_blake3_hash(const void *input, size_t input_len, uint8_t out[32]) {
    blake3_hasher hasher;
    blake3_hasher_init(&hasher);
    blake3_hasher_update(&hasher, input, input_len);
    blake3_hasher_finalize(&hasher, out, 32);
}
#else
#include "blake3_portable.h"
#define VIPR_BLAKE3_BACKEND_NAME "portable"
#endif

#endif
