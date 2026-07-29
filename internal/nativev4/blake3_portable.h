/*
 * Portable BLAKE3 implementation adapted for Viper-Patcher.
 * Copyright 2019 Jack O'Connor and Samuel Neves.
 * Copyright 2026 Viper-Patcher contributors.
 * SPDX-License-Identifier: Apache-2.0
 */

#ifndef VIPR_BLAKE3_PORTABLE_H
#define VIPR_BLAKE3_PORTABLE_H

#include <stddef.h>
#include <stdint.h>

#define VIPR_BLAKE3_OUT_LEN 32
#define VIPR_BLAKE3_BLOCK_LEN 64
#define VIPR_BLAKE3_CHUNK_LEN 1024
#define VIPR_BLAKE3_MAX_DEPTH 54

typedef struct {
    uint32_t cv[8];
    uint64_t chunk_counter;
    uint8_t block[VIPR_BLAKE3_BLOCK_LEN];
    uint8_t block_len;
    uint8_t blocks_compressed;
    uint8_t flags;
} vipr_blake3_chunk_state;

typedef struct {
    uint32_t key[8];
    vipr_blake3_chunk_state chunk;
    uint32_t cv_stack[VIPR_BLAKE3_MAX_DEPTH][8];
    uint8_t cv_stack_len;
    uint8_t flags;
} vipr_blake3_hasher;

void vipr_blake3_init(vipr_blake3_hasher *hasher);
void vipr_blake3_update(vipr_blake3_hasher *hasher, const void *input, size_t input_len);
void vipr_blake3_finalize(const vipr_blake3_hasher *hasher, uint8_t out[VIPR_BLAKE3_OUT_LEN]);
void vipr_blake3_hash(const void *input, size_t input_len, uint8_t out[VIPR_BLAKE3_OUT_LEN]);

#endif
