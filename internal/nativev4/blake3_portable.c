#ifndef VIPR_STATIC_BLAKE3
/*
 * Portable BLAKE3 implementation adapted for Viper-Patcher.
 * Copyright 2019 Jack O'Connor and Samuel Neves.
 * Copyright 2026 Viper-Patcher contributors.
 * SPDX-License-Identifier: Apache-2.0
 */

#include "blake3_portable.h"

#include <string.h>

#define CHUNK_START 1u
#define CHUNK_END 2u
#define PARENT 4u
#define ROOT 8u

static const uint32_t IV[8] = {
    0x6A09E667u, 0xBB67AE85u, 0x3C6EF372u, 0xA54FF53Au,
    0x510E527Fu, 0x9B05688Cu, 0x1F83D9ABu, 0x5BE0CD19u,
};

static const uint8_t MSG_SCHEDULE[7][16] = {
    {0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15},
    {2, 6, 3, 10, 7, 0, 4, 13, 1, 11, 12, 5, 9, 14, 15, 8},
    {3, 4, 10, 12, 13, 2, 7, 14, 6, 5, 9, 0, 11, 15, 8, 1},
    {10, 7, 12, 9, 14, 3, 13, 15, 4, 0, 11, 2, 5, 8, 1, 6},
    {12, 13, 9, 11, 15, 10, 14, 8, 7, 2, 5, 3, 0, 1, 6, 4},
    {9, 14, 11, 5, 8, 12, 15, 1, 13, 3, 0, 10, 2, 6, 4, 7},
    {11, 15, 5, 0, 1, 9, 8, 6, 14, 10, 2, 12, 3, 4, 7, 13},
};

typedef struct {
    uint32_t input_cv[8];
    uint32_t block_words[16];
    uint64_t counter;
    uint32_t block_len;
    uint32_t flags;
} output_t;

static uint32_t load32(const uint8_t *p) {
    return ((uint32_t)p[0]) |
           ((uint32_t)p[1] << 8) |
           ((uint32_t)p[2] << 16) |
           ((uint32_t)p[3] << 24);
}

static void store32(uint8_t *p, uint32_t value) {
    p[0] = (uint8_t)value;
    p[1] = (uint8_t)(value >> 8);
    p[2] = (uint8_t)(value >> 16);
    p[3] = (uint8_t)(value >> 24);
}

static uint32_t rotr32(uint32_t value, uint32_t count) {
    return (value >> count) | (value << (32u - count));
}

static void g(uint32_t state[16], size_t a, size_t b, size_t c, size_t d, uint32_t mx, uint32_t my) {
    state[a] = state[a] + state[b] + mx;
    state[d] = rotr32(state[d] ^ state[a], 16);
    state[c] = state[c] + state[d];
    state[b] = rotr32(state[b] ^ state[c], 12);
    state[a] = state[a] + state[b] + my;
    state[d] = rotr32(state[d] ^ state[a], 8);
    state[c] = state[c] + state[d];
    state[b] = rotr32(state[b] ^ state[c], 7);
}

static void round_fn(uint32_t state[16], const uint32_t msg[16], size_t round) {
    const uint8_t *s = MSG_SCHEDULE[round];
    g(state, 0, 4, 8, 12, msg[s[0]], msg[s[1]]);
    g(state, 1, 5, 9, 13, msg[s[2]], msg[s[3]]);
    g(state, 2, 6, 10, 14, msg[s[4]], msg[s[5]]);
    g(state, 3, 7, 11, 15, msg[s[6]], msg[s[7]]);
    g(state, 0, 5, 10, 15, msg[s[8]], msg[s[9]]);
    g(state, 1, 6, 11, 12, msg[s[10]], msg[s[11]]);
    g(state, 2, 7, 8, 13, msg[s[12]], msg[s[13]]);
    g(state, 3, 4, 9, 14, msg[s[14]], msg[s[15]]);
}

static void compress(const uint32_t cv[8], const uint32_t block_words[16], uint64_t counter,
                     uint32_t block_len, uint32_t flags, uint32_t out[16]) {
    uint32_t state[16];
    memcpy(state, cv, 8 * sizeof(uint32_t));
    state[8] = IV[0];
    state[9] = IV[1];
    state[10] = IV[2];
    state[11] = IV[3];
    state[12] = (uint32_t)counter;
    state[13] = (uint32_t)(counter >> 32);
    state[14] = block_len;
    state[15] = flags;
    for (size_t round = 0; round < 7; ++round) {
        round_fn(state, block_words, round);
    }
    for (size_t i = 0; i < 8; ++i) {
        out[i] = state[i] ^ state[i + 8];
        out[i + 8] = state[i + 8] ^ cv[i];
    }
}

static void block_words_from_bytes(const uint8_t block[64], uint32_t words[16]) {
    for (size_t i = 0; i < 16; ++i) {
        words[i] = load32(block + i * 4);
    }
}

static uint32_t chunk_start_flag(const vipr_blake3_chunk_state *state) {
    return state->blocks_compressed == 0 ? CHUNK_START : 0;
}

static size_t chunk_len(const vipr_blake3_chunk_state *state) {
    return (size_t)state->blocks_compressed * VIPR_BLAKE3_BLOCK_LEN + state->block_len;
}

static void chunk_state_init(vipr_blake3_chunk_state *state, const uint32_t key[8], uint64_t counter, uint8_t flags) {
    memcpy(state->cv, key, 8 * sizeof(uint32_t));
    state->chunk_counter = counter;
    memset(state->block, 0, sizeof(state->block));
    state->block_len = 0;
    state->blocks_compressed = 0;
    state->flags = flags;
}

static void chunk_state_compress_block(vipr_blake3_chunk_state *state) {
    uint32_t words[16];
    uint32_t out[16];
    block_words_from_bytes(state->block, words);
    compress(state->cv, words, state->chunk_counter, VIPR_BLAKE3_BLOCK_LEN,
             (uint32_t)state->flags | chunk_start_flag(state), out);
    memcpy(state->cv, out, 8 * sizeof(uint32_t));
    state->blocks_compressed++;
    memset(state->block, 0, sizeof(state->block));
    state->block_len = 0;
}

static void chunk_state_update(vipr_blake3_chunk_state *state, const uint8_t *input, size_t input_len) {
    while (input_len > 0) {
        if (state->block_len == VIPR_BLAKE3_BLOCK_LEN) {
            chunk_state_compress_block(state);
        }
        size_t want = VIPR_BLAKE3_BLOCK_LEN - state->block_len;
        size_t take = input_len < want ? input_len : want;
        memcpy(state->block + state->block_len, input, take);
        state->block_len = (uint8_t)(state->block_len + take);
        input += take;
        input_len -= take;
    }
}

static output_t chunk_state_output(const vipr_blake3_chunk_state *state) {
    output_t output;
    memcpy(output.input_cv, state->cv, sizeof(output.input_cv));
    block_words_from_bytes(state->block, output.block_words);
    output.counter = state->chunk_counter;
    output.block_len = state->block_len;
    output.flags = (uint32_t)state->flags | chunk_start_flag(state) | CHUNK_END;
    return output;
}

static void output_chaining_value(const output_t *output, uint32_t cv[8]) {
    uint32_t words[16];
    compress(output->input_cv, output->block_words, output->counter, output->block_len, output->flags, words);
    memcpy(cv, words, 8 * sizeof(uint32_t));
}

static output_t parent_output(const uint32_t left[8], const uint32_t right[8], const uint32_t key[8], uint8_t flags) {
    output_t output;
    memcpy(output.input_cv, key, sizeof(output.input_cv));
    memcpy(output.block_words, left, 8 * sizeof(uint32_t));
    memcpy(output.block_words + 8, right, 8 * sizeof(uint32_t));
    output.counter = 0;
    output.block_len = VIPR_BLAKE3_BLOCK_LEN;
    output.flags = (uint32_t)flags | PARENT;
    return output;
}

static void parent_cv(const uint32_t left[8], const uint32_t right[8], const uint32_t key[8], uint8_t flags, uint32_t out[8]) {
    output_t output = parent_output(left, right, key, flags);
    output_chaining_value(&output, out);
}

static void push_stack(vipr_blake3_hasher *hasher, const uint32_t cv[8]) {
    memcpy(hasher->cv_stack[hasher->cv_stack_len], cv, 8 * sizeof(uint32_t));
    hasher->cv_stack_len++;
}

static void pop_stack(vipr_blake3_hasher *hasher, uint32_t cv[8]) {
    hasher->cv_stack_len--;
    memcpy(cv, hasher->cv_stack[hasher->cv_stack_len], 8 * sizeof(uint32_t));
}

static void add_chunk_cv(vipr_blake3_hasher *hasher, uint32_t new_cv[8], uint64_t total_chunks) {
    while ((total_chunks & 1u) == 0u) {
        uint32_t left[8];
        pop_stack(hasher, left);
        parent_cv(left, new_cv, hasher->key, hasher->flags, new_cv);
        total_chunks >>= 1;
    }
    push_stack(hasher, new_cv);
}

void vipr_blake3_init(vipr_blake3_hasher *hasher) {
    memcpy(hasher->key, IV, sizeof(hasher->key));
    hasher->flags = 0;
    hasher->cv_stack_len = 0;
    chunk_state_init(&hasher->chunk, hasher->key, 0, hasher->flags);
}

void vipr_blake3_update(vipr_blake3_hasher *hasher, const void *input_ptr, size_t input_len) {
    const uint8_t *input = (const uint8_t *)input_ptr;
    while (input_len > 0) {
        if (chunk_len(&hasher->chunk) == VIPR_BLAKE3_CHUNK_LEN) {
            output_t output = chunk_state_output(&hasher->chunk);
            uint32_t chunk_cv[8];
            output_chaining_value(&output, chunk_cv);
            uint64_t total_chunks = hasher->chunk.chunk_counter + 1;
            add_chunk_cv(hasher, chunk_cv, total_chunks);
            chunk_state_init(&hasher->chunk, hasher->key, total_chunks, hasher->flags);
        }
        size_t remaining = VIPR_BLAKE3_CHUNK_LEN - chunk_len(&hasher->chunk);
        size_t take = input_len < remaining ? input_len : remaining;
        chunk_state_update(&hasher->chunk, input, take);
        input += take;
        input_len -= take;
    }
}

void vipr_blake3_finalize(const vipr_blake3_hasher *source, uint8_t out[VIPR_BLAKE3_OUT_LEN]) {
    vipr_blake3_hasher hasher = *source;
    output_t output = chunk_state_output(&hasher.chunk);
    while (hasher.cv_stack_len > 0) {
        uint32_t right[8];
        uint32_t left[8];
        output_chaining_value(&output, right);
        pop_stack(&hasher, left);
        output = parent_output(left, right, hasher.key, hasher.flags);
    }
    uint32_t words[16];
    compress(output.input_cv, output.block_words, 0, output.block_len, output.flags | ROOT, words);
    for (size_t i = 0; i < 8; ++i) {
        store32(out + i * 4, words[i]);
    }
}

void vipr_blake3_hash(const void *input, size_t input_len, uint8_t out[VIPR_BLAKE3_OUT_LEN]) {
    vipr_blake3_hasher hasher;
    vipr_blake3_init(&hasher);
    vipr_blake3_update(&hasher, input, input_len);
    vipr_blake3_finalize(&hasher, out);
}
#endif
