#include "native_internal.h"

#include <limits.h>
#include <stdlib.h>
#include <string.h>

typedef struct {
    uint8_t *data;
    size_t size;
    size_t capacity;
} byte_buffer;

typedef enum { OP_COPY = 1, OP_ADD = 2 } op_kind;
typedef struct {
    op_kind kind;
    uint64_t source_offset;
    uint32_t target_offset;
    uint32_t length;
} delta_op;

typedef struct {
    delta_op *items;
    size_t count;
    size_t capacity;
    uint64_t copied_bytes;
} op_list;

typedef struct {
    uint64_t hash;
    uint32_t offset;
    uint32_t length;
    int32_t next;
} indexed_chunk;

typedef struct {
    indexed_chunk *chunks;
    uint32_t chunk_count;
    uint32_t chunk_capacity;
    int32_t *buckets;
    uint32_t bucket_count;
    uint32_t bucket_capacity;
} chunk_index;

struct vipr_window_workspace {
    vipr_scratch_buffer source;
    vipr_scratch_buffer target;
    byte_buffer sparse_delta;
    byte_buffer cdc_delta;
    byte_buffer replacement_zstd;
    byte_buffer delta_zstd;
    op_list sparse_ops;
    op_list cdc_ops;
    chunk_index index;
    uint8_t run_byte;
};

static int buffer_reserve(byte_buffer *buffer, size_t extra) {
    if (extra > SIZE_MAX - buffer->size) return 0;
    size_t required = buffer->size + extra;
    if (required <= buffer->capacity) return 1;
    size_t capacity = buffer->capacity == 0 ? 256 : buffer->capacity;
    while (capacity < required) {
        if (capacity > SIZE_MAX / 2) { capacity = required; break; }
        capacity *= 2;
    }
    uint8_t *next = (uint8_t *)realloc(buffer->data, capacity);
    if (next == NULL) return 0;
    buffer->data = next;
    buffer->capacity = capacity;
    return 1;
}

static void buffer_reset(byte_buffer *buffer) { buffer->size = 0; }
static void buffer_free(byte_buffer *buffer) {
    free(buffer->data);
    memset(buffer, 0, sizeof(*buffer));
}

static int buffer_append(byte_buffer *buffer, const void *data, size_t size) {
    if (!buffer_reserve(buffer, size)) return 0;
    if (size != 0) memcpy(buffer->data + buffer->size, data, size);
    buffer->size += size;
    return 1;
}

static int buffer_byte(byte_buffer *buffer, uint8_t value) { return buffer_append(buffer, &value, 1); }
static int buffer_u16(byte_buffer *buffer, uint16_t value) {
    uint8_t data[2] = {(uint8_t)value, (uint8_t)(value >> 8)};
    return buffer_append(buffer, data, sizeof(data));
}
static int buffer_varint(byte_buffer *buffer, uint64_t value) {
    uint8_t data[10]; size_t count = vipr_write_uvarint(data, value); return buffer_append(buffer, data, count);
}

static void ops_reset(op_list *list) {
    list->count = 0;
    list->copied_bytes = 0;
}

static int ops_reserve(op_list *list) {
    if (list->count < list->capacity) return 1;
    size_t next_capacity = list->capacity == 0 ? 64 : list->capacity * 2;
    if (next_capacity < list->capacity || next_capacity > SIZE_MAX / sizeof(*list->items)) return 0;
    delta_op *next = (delta_op *)realloc(list->items, next_capacity * sizeof(*next));
    if (next == NULL) return 0;
    list->items = next; list->capacity = next_capacity; return 1;
}

static int ops_add_copy(op_list *list, uint64_t source_offset, uint32_t length) {
    if (length == 0) return 1;
    if (list->count > 0) {
        delta_op *last = &list->items[list->count - 1];
        if (last->kind == OP_COPY && last->source_offset + last->length == source_offset && last->length <= UINT32_MAX - length) {
            last->length += length; list->copied_bytes += length; return 1;
        }
    }
    if (!ops_reserve(list)) return 0;
    list->items[list->count++] = (delta_op){OP_COPY, source_offset, 0, length};
    list->copied_bytes += length;
    return 1;
}

static int ops_add_literal(op_list *list, uint32_t target_offset, uint32_t length) {
    if (length == 0) return 1;
    if (list->count > 0) {
        delta_op *last = &list->items[list->count - 1];
        if (last->kind == OP_ADD && last->target_offset + last->length == target_offset && last->length <= UINT32_MAX - length) {
            last->length += length; return 1;
        }
    }
    if (!ops_reserve(list)) return 0;
    list->items[list->count++] = (delta_op){OP_ADD, 0, target_offset, length};
    return 1;
}

static const uint64_t GEAR_TABLE[256] = {
    0x2cb0f69f4abea221ULL, 0xb19e04cb9dce3146ULL, 0xedbbddc1c7f7b932ULL, 0x9899c471311432c4ULL,
    0x077045873482600bULL, 0x96d8c4d8cdc10675ULL, 0xddc8f2da53280d7bULL, 0x7751653253b64096ULL,
    0x1b7667c3f390f03eULL, 0x4b7eb9f6216d62efULL, 0xe2c3904e3d5c2be4ULL, 0xa682f1a42af18195ULL,
    0x9643f2adedab21f3ULL, 0xe9415eda92dde346ULL, 0x608287818dd33ccfULL, 0xbf045e7a19c494d8ULL,
    0x309e8633bc17cfa5ULL, 0x111e997bdd7f4e66ULL, 0x27177c0fb93645abULL, 0xc6e939f547e73cd7ULL,
    0xc712e6f512970572ULL, 0x9ddbc924c903707cULL, 0xb84a549879b14cdcULL, 0x9d9b7fc21c045803ULL,
    0x600a15843b22cdbdULL, 0xe8f5d546c394101dULL, 0xf8ca39ea7c44439cULL, 0xabac22d69623316bULL,
    0xf9ed2892dca5173cULL, 0x4b2c89b21c546384ULL, 0x79d0286e8f2e89e0ULL, 0xdeb455fbeb56fbaaULL,
    0x59424d3a7239d388ULL, 0xcae5d2368f673b18ULL, 0xec539b487807f717ULL, 0x8c0658ea203c3e15ULL,
    0x0018ee75c4854a20ULL, 0x3bd4d088c597306dULL, 0x64d910c6f79a9e85ULL, 0x170a4158be257964ULL,
    0x7d6385067a2b5effULL, 0x5f3e41bdd808a69aULL, 0xbf71c487edcd09f8ULL, 0x73e2072eab2f7ca2ULL,
    0x5b5d62d2bf83ca44ULL, 0xddef013edc544159ULL, 0x286ece62bade126aULL, 0x6c845baa9bce87f4ULL,
    0x6b384a5a90b6aebfULL, 0xa6ccb784c933b0d4ULL, 0xcccefdf652c954d1ULL, 0x10bb2c4527b07507ULL,
    0xce35c23b260f516cULL, 0x600e4eb3966dcfbcULL, 0x872912fd72291898ULL, 0x9154319cdf681edcULL,
    0x0278f89d8acb659eULL, 0x41d85ae934d24e3dULL, 0x0e9351824b1ca983ULL, 0xec10296a03235f9cULL,
    0x2ddad45d6d7fdf64ULL, 0xec161a34bfe1177fULL, 0xd7e24d40607913a2ULL, 0x5e53790f40637b8dULL,
    0xec9c8cdf231f39a4ULL, 0x7ca6f63f71f83562ULL, 0x716b202856520ef1ULL, 0x4d50c84ec7a419d6ULL,
    0xc9d99f942fa55f54ULL, 0xefd628393326d0d1ULL, 0x2e27cbe708e63a9cULL, 0x3987d89e3527d9a5ULL,
    0xb1af4d71e9984e72ULL, 0xb9e5b53a5568672aULL, 0x4f7572266fd9267bULL, 0x9263bb8028a09507ULL,
    0xcbc738566c19526bULL, 0x75da78dbc27e4bd4ULL, 0x31fc3cca8f4475e6ULL, 0xb4e5dd70b2c3fb37ULL,
    0x7f5a84b5fb2495f6ULL, 0x31acd6aa5b51a95fULL, 0x6298793670d35d37ULL, 0x5655c6a539456497ULL,
    0xd7bd91a1e02571cdULL, 0x8b6ba0f1f497fce8ULL, 0x70be638350ac3f13ULL, 0x74baaccc685a3092ULL,
    0x96d2523ce322842aULL, 0x3254481248a7732eULL, 0x492eb11538f42803ULL, 0x757ecfa5875d7b8dULL,
    0x30efa548caecc2e6ULL, 0x40fc93f245147cfdULL, 0x7c75330be730d413ULL, 0xc15734fe048c9ddeULL,
    0x44c589d274bade6fULL, 0x74f93215894d7b83ULL, 0xd38ea3c660f5e552ULL, 0xb46eb18a2b2cfe60ULL,
    0xcb10fff814c92ab0ULL, 0x3f463729a4be02bcULL, 0x5358012220daff33ULL, 0x55c7e06a5204941dULL,
    0xcb536f658761f5c7ULL, 0xf4eee9049ad42589ULL, 0x98fadc39e827d1e3ULL, 0x09fa0c5e1c75b19dULL,
    0x0ffa448d5ad69a0fULL, 0x99cdf39aefae8fb1ULL, 0xec7da8f390f65513ULL, 0xa024ea535f978727ULL,
    0xadd22cfb9e816fadULL, 0x43bdd58e539d07faULL, 0x66cc149c87363867ULL, 0xf85c66e4ff570da2ULL,
    0xa84c0d5668cef168ULL, 0x9d86f068bf00b25eULL, 0xa2352f0a02379cceULL, 0x521210fb2bfffeadULL,
    0xaa1843fdaf639d94ULL, 0xdb23be849b01d1dbULL, 0xda095ab097b7e2c6ULL, 0xa914279f812777d3ULL,
    0xab23e68be57fe66eULL, 0x82c1d4981bb2debeULL, 0xb013c57f466f6802ULL, 0x9e69085723b77059ULL,
    0x973413d9cdfadf17ULL, 0xdb3d74ff6ef64719ULL, 0x9e7a98933fa0e9bdULL, 0x41a03ffde27eb2a8ULL,
    0xcd37a774d6b056a6ULL, 0x45f0409fd30a79edULL, 0xc660c004eacf7051ULL, 0x42a771d3ee6bd5eaULL,
    0xe1fccf089e8e78e2ULL, 0xdc526e60bf26b37dULL, 0xcd4ea084961fc154ULL, 0x4fc5dee22ff00c55ULL,
    0xe1e0dedc458a8d82ULL, 0x86c4e322fb9f1e42ULL, 0x8e73044ca30ca78bULL, 0x9a99a78b76f4d8e3ULL,
    0xcf2380b653382afbULL, 0x5efe1d7e20638cbfULL, 0x9dacc223e8a9227eULL, 0x495ccab737c61320ULL,
    0xca01070f338eacc7ULL, 0x24e3d8854487031bULL, 0xd385a85a62d96d3aULL, 0xc155c7dee3d38080ULL,
    0x02564d6c3361c416ULL, 0x34925438eeb9025cULL, 0x5a0c577d5f522fcaULL, 0xdcf54d94187da601ULL,
    0x286082e1dd0bc45cULL, 0xe0573a8096b8c6f9ULL, 0x37bec1ed346ee8f8ULL, 0x7e1b07c75df6e84bULL,
    0xfd97f3ad71fee1ceULL, 0x54b7bee683f9127cULL, 0x1dd7f64d9b41274eULL, 0xb711f9b14f85ac69ULL,
    0x67c8ba073e658ddfULL, 0xf719de08acaf041cULL, 0x0d68144063050fefULL, 0xe497b50766b85addULL,
    0x1e4ea9bcd3c3e7d5ULL, 0xad3772b4234b24f1ULL, 0x8145481fc2665e5eULL, 0x5366465bad09975aULL,
    0x39c63df7cb7d4a9dULL, 0xfb620a0244ff36eeULL, 0x06457dfc7b2354ccULL, 0xe92e43375c158c0bULL,
    0x0a365f933e2aa183ULL, 0x31807f78decefeffULL, 0xe3def87ca9ba8bd6ULL, 0xb34c6bee173964c5ULL,
    0x87d2acb091200bddULL, 0xe343544fb7cac7e8ULL, 0xd02677b899fb1e63ULL, 0x92c5a55fd4911b70ULL,
    0x0c84fe8eb21fa8b7ULL, 0xb88d87d4febd2d23ULL, 0xbc0fbab569583b85ULL, 0x8035508382baa503ULL,
    0x18f3ff3b15bf238eULL, 0x34fe6f965e241783ULL, 0x41a3d8dd029563bcULL, 0x7ee7b1aa0756d1fdULL,
    0xbe84b2be27d49bcdULL, 0x3d0322c753232059ULL, 0x88842f3a0243e730ULL, 0x865a1e05e904122cULL,
    0x4713aebf650134beULL, 0x7a70c9f1d8ee95d0ULL, 0xfc22f42cca64d109ULL, 0xd6f9e0c6eee56251ULL,
    0x717cf2314018675bULL, 0xe0520717a9167ebaULL, 0x8f4d0686a7849630ULL, 0x3dc1819e6c1b4acfULL,
    0x453a2897aab94cf8ULL, 0x8d5569f7771909f1ULL, 0xcb6cbd74134e95d2ULL, 0x8dce289251f6953cULL,
    0xaf785be00e1f3b03ULL, 0x0766feb305d80cb3ULL, 0x71c9285a0e6313fdULL, 0x65c7b97d7cd54facULL,
    0x08bd0f82c2fed582ULL, 0xef337cde110ef087ULL, 0xde3315aa7ce1840dULL, 0x9be6349365c84ad5ULL,
    0x16ef3969c1c30e38ULL, 0x974cb0628c7032e3ULL, 0x3ae214a362fc3e3fULL, 0xe26ced27432a3ce0ULL,
    0x0cb7962119e6473eULL, 0x8d2725f904387cfaULL, 0x3690bf4ca24cfcf2ULL, 0x26c75f73bcbc303bULL,
    0xef7d97e55c2c5c2dULL, 0xd68e464b42eecbf6ULL, 0xd22b4d0a230c2615ULL, 0x4ffce0d5944a4b3eULL,
    0xf3f79a9d6fee320cULL, 0x66eeb49703f1e039ULL, 0x8e2a30efd88553c1ULL, 0x8cf1fe2627c40dedULL,
    0xc8072ae6dda44cc7ULL, 0x27db137ad427e3e7ULL, 0x09e1f4f4b873ec69ULL, 0x8b0b80936fa978abULL,
    0xbd5a7a6e54b47964ULL, 0x037c55e298453405ULL, 0x53fbf12baa2e38ebULL, 0xc3bada5aabe7747fULL,
    0x46fa56c8b66bda42ULL, 0x791146a43919c5c8ULL, 0x488c08bc425eab5cULL, 0x641d6144d4541a46ULL,
    0x1fdb694ccf87a372ULL, 0xf8a25a4e3eb21938ULL, 0x8cc12b0d6020a406ULL, 0xdb6e521cd864efafULL,
    0xa6bd2dd9d30812b6ULL, 0xe4ccbe33e8a90e24ULL, 0x3b1db08bfbfcfb5dULL, 0xbbae13149ba8ad02ULL,
    0xda2ef44f3d461bf0ULL, 0xf96f819fe4f08e72ULL, 0x17972aa5f6766d04ULL, 0xb19388bda81d6152ULL,
};

static uint32_t next_power_of_two(uint32_t value) {
    if (value < 2) return 2;
    value--; value |= value >> 1; value |= value >> 2; value |= value >> 4; value |= value >> 8; value |= value >> 16;
    return value + 1;
}

typedef int (*chunk_callback)(uint32_t offset, uint32_t length, void *context);

static int for_each_chunk(const uint8_t *data, uint32_t size, uint32_t minimum, uint32_t average, uint32_t maximum,
                          chunk_callback callback, void *context) {
    uint32_t start = 0;
    uint64_t gear = 0;
    for (uint32_t index = 0; index < size; ++index) {
        gear = (gear << 1) + GEAR_TABLE[data[index]];
        uint32_t length = index + 1 - start;
        if (length < minimum) continue;
        if (length < maximum && (gear & (average - 1u)) != 0) continue;
        if (!callback(start, length, context)) return 0;
        start = index + 1; gear = 0;
    }
    if (start < size && !callback(start, size - start, context)) return 0;
    return 1;
}

typedef struct { const uint8_t *data; chunk_index *index; } index_builder;

static int index_reserve_chunk(chunk_index *index) {
    if (index->chunk_count < index->chunk_capacity) return 1;
    uint32_t capacity = index->chunk_capacity == 0 ? 64u : index->chunk_capacity * 2u;
    if (capacity < index->chunk_capacity) return 0;
#if SIZE_MAX < UINT64_MAX
    if ((size_t)capacity > SIZE_MAX / sizeof(*index->chunks)) return 0;
#endif
    indexed_chunk *next = (indexed_chunk *)realloc(index->chunks, (size_t)capacity * sizeof(*next));
    if (next == NULL) return 0;
    index->chunks = next;
    index->chunk_capacity = capacity;
    return 1;
}

static int collect_index_chunk(uint32_t offset, uint32_t length, void *context) {
    index_builder *builder = (index_builder *)context;
    if (!index_reserve_chunk(builder->index)) return 0;
    indexed_chunk *chunk = &builder->index->chunks[builder->index->chunk_count++];
    chunk->hash = vipr_hash64(builder->data + offset, length);
    chunk->offset = offset;
    chunk->length = length;
    chunk->next = -1;
    return 1;
}

static int build_chunk_index(const uint8_t *data, uint32_t size, uint32_t minimum, uint32_t average, uint32_t maximum,
                             chunk_index *index) {
    index->chunk_count = 0;
    index_builder builder = {data, index};
    if (!for_each_chunk(data, size, minimum, average, maximum, collect_index_chunk, &builder)) return 0;
    uint32_t bucket_count = next_power_of_two(index->chunk_count * 2u + 1u);
    if (bucket_count > index->bucket_capacity) {
        int32_t *next = (int32_t *)realloc(index->buckets, (size_t)bucket_count * sizeof(*next));
        if (next == NULL) return 0;
        index->buckets = next;
        index->bucket_capacity = bucket_count;
    }
    index->bucket_count = bucket_count;
    for (uint32_t i = 0; i < bucket_count; ++i) index->buckets[i] = -1;
    for (uint32_t i = 0; i < index->chunk_count; ++i) {
        indexed_chunk *chunk = &index->chunks[i];
        uint32_t bucket = (uint32_t)chunk->hash & (bucket_count - 1u);
        chunk->next = index->buckets[bucket];
        index->buckets[bucket] = (int32_t)i;
    }
    return 1;
}

vipr_window_workspace *vipr_window_workspace_create(void) {
    return (vipr_window_workspace *)calloc(1, sizeof(vipr_window_workspace));
}

void vipr_window_workspace_free(vipr_window_workspace *workspace) {
    if (workspace == NULL) return;
    vipr_scratch_free(&workspace->source);
    vipr_scratch_free(&workspace->target);
    buffer_free(&workspace->sparse_delta);
    buffer_free(&workspace->cdc_delta);
    buffer_free(&workspace->replacement_zstd);
    buffer_free(&workspace->delta_zstd);
    free(workspace->sparse_ops.items);
    free(workspace->cdc_ops.items);
    free(workspace->index.chunks);
    free(workspace->index.buckets);
    free(workspace);
}

typedef struct {
    const uint8_t *source;
    const uint8_t *target;
    const chunk_index *index;
    op_list *ops;
    uint64_t source_base;
    uint64_t preferred;
} target_matcher;

static int match_target_chunk(uint32_t offset, uint32_t length, void *context) {
    target_matcher *matcher = (target_matcher *)context;
    uint64_t hash = vipr_hash64(matcher->target + offset, length);
    uint32_t bucket = (uint32_t)hash & (matcher->index->bucket_count - 1u);
    const indexed_chunk *fallback = NULL;
    const indexed_chunk *selected = NULL;
    for (int32_t cursor = matcher->index->buckets[bucket]; cursor >= 0; cursor = matcher->index->chunks[cursor].next) {
        const indexed_chunk *candidate = &matcher->index->chunks[cursor];
        if (candidate->hash != hash || candidate->length != length) continue;
        if (memcmp(matcher->source + candidate->offset, matcher->target + offset, length) != 0) continue;
        if (fallback == NULL || candidate->offset < fallback->offset) fallback = candidate;
        if (matcher->source_base + candidate->offset == matcher->preferred) { selected = candidate; break; }
    }
    if (selected == NULL) selected = fallback;
    if (selected != NULL) {
        uint64_t absolute = matcher->source_base + selected->offset;
        if (!ops_add_copy(matcher->ops, absolute, length)) return 0;
        matcher->preferred = absolute + length;
    } else {
        if (!ops_add_literal(matcher->ops, offset, length)) return 0;
        matcher->preferred = UINT64_MAX;
    }
    return 1;
}

static int all_same_byte(const uint8_t *data, uint32_t size, uint8_t *value) {
    if (size == 0) return 0;
    uint8_t first = data[0];
    for (uint32_t i = 1; i < size; ++i) if (data[i] != first) return 0;
    *value = first; return 1;
}


static int build_sparse_ops(const uint8_t *source, const uint8_t *target, uint32_t size,
                            uint64_t source_offset, op_list *ops) {
    uint32_t cursor = 0;
    while (cursor < size) {
        uint32_t start = cursor;
        int equal = source[cursor] == target[cursor];
        while (cursor < size && (source[cursor] == target[cursor]) == equal) cursor++;
        uint32_t length = cursor - start;
        if (equal) {
            if (!ops_add_copy(ops, source_offset + start, length)) return 0;
        } else if (!ops_add_literal(ops, start, length)) {
            return 0;
        }
    }
    return 1;
}

static int encode_copy(byte_buffer *buffer, uint64_t source_absolute, uint64_t source_base,
                       uint64_t output_absolute, uint64_t produced, uint64_t previous_end,
                       uint32_t length) {
    if (source_absolute == output_absolute + produced && length <= 256u) {
        return buffer_byte(buffer, VIPR_OPCODE_COPY_SAME_SHORT) && buffer_byte(buffer, (uint8_t)(length - 1u));
    }
    uint64_t local = source_absolute - source_base;
    int have_delta = 0;
    int64_t delta = 0;
    if (previous_end != UINT64_MAX) {
        if (source_absolute >= previous_end && source_absolute - previous_end <= INT16_MAX) {
            delta = (int64_t)(source_absolute - previous_end);
            have_delta = 1;
        } else if (previous_end > source_absolute && previous_end - source_absolute <= (uint64_t)(-(int64_t)INT16_MIN)) {
            delta = -(int64_t)(previous_end - source_absolute);
            have_delta = 1;
        }
    }
    if (have_delta && length <= UINT16_MAX) {
        return buffer_byte(buffer, VIPR_OPCODE_COPY_DELTA_SHORT) && buffer_u16(buffer, (uint16_t)(int16_t)delta) && buffer_u16(buffer, (uint16_t)length);
    }
    if (local <= UINT16_MAX && length <= UINT16_MAX) {
        return buffer_byte(buffer, VIPR_OPCODE_COPY_LOCAL_SHORT) && buffer_u16(buffer, (uint16_t)local) && buffer_u16(buffer, (uint16_t)length);
    }
    if (source_absolute >= source_base) {
        return buffer_byte(buffer, VIPR_OPCODE_COPY_LOCAL_LONG) && buffer_varint(buffer, local) && buffer_varint(buffer, length);
    }
    return buffer_byte(buffer, VIPR_OPCODE_COPY_ABSOLUTE) && buffer_varint(buffer, source_absolute) && buffer_varint(buffer, length);
}

static int encode_literal(byte_buffer *buffer, const uint8_t *data, uint32_t length) {
    uint8_t run = 0;
    if (length >= 8 && all_same_byte(data, length, &run)) {
        if (run == 0) return buffer_byte(buffer, VIPR_OPCODE_ZERO) && buffer_varint(buffer, length);
        return buffer_byte(buffer, VIPR_OPCODE_RUN_BYTE) && buffer_byte(buffer, run) && buffer_varint(buffer, length);
    }
    if (length <= 255u) return buffer_byte(buffer, VIPR_OPCODE_ADD_SHORT) && buffer_byte(buffer, (uint8_t)length) && buffer_append(buffer, data, length);
    return buffer_byte(buffer, VIPR_OPCODE_ADD_LONG) && buffer_varint(buffer, length) && buffer_append(buffer, data, length);
}

static int encode_delta(const op_list *ops, const uint8_t *target, uint64_t source_base, uint64_t output_offset,
                        byte_buffer *encoded, uint16_t *instruction_count) {
    static const uint8_t magic[8] = {'V','4','O','P','S','\r','\n',1};
    if (!buffer_append(encoded, magic, sizeof(magic))) return 0;
    uint64_t produced = 0;
    uint64_t previous_end = UINT64_MAX;
    uint32_t count = 0;
    for (size_t i = 0; i < ops->count; ++i) {
        const delta_op *op = &ops->items[i];
        if (count == UINT16_MAX) return 0;
        if (op->kind == OP_COPY) {
            // Combine the common COPY+ADD pair into one opcode when both lengths
            // are small enough. The source offset remains local to the descriptor.
            if (i + 1 < ops->count && ops->items[i + 1].kind == OP_ADD && op->source_offset >= source_base) {
                const delta_op *add = &ops->items[i + 1];
                if (add->length <= 255u) {
                    if (!buffer_byte(encoded, VIPR_OPCODE_COPY_ADD_PAIR) ||
                        !buffer_varint(encoded, op->source_offset - source_base) ||
                        !buffer_varint(encoded, op->length) ||
                        !buffer_byte(encoded, (uint8_t)add->length) ||
                        !buffer_append(encoded, target + add->target_offset, add->length)) return 0;
                    produced += (uint64_t)op->length + add->length;
                    previous_end = op->source_offset + op->length;
                    ++i; ++count; continue;
                }
            }
            if (!encode_copy(encoded, op->source_offset, source_base, output_offset, produced, previous_end, op->length)) return 0;
            produced += op->length; previous_end = op->source_offset + op->length;
        } else {
            if (!encode_literal(encoded, target + op->target_offset, op->length)) return 0;
            produced += op->length; previous_end = UINT64_MAX;
        }
        ++count;
    }
    if (!buffer_byte(encoded, VIPR_OPCODE_END)) return 0;
    *instruction_count = (uint16_t)count;
    return 1;
}

static vipr_status compress_payload(vipr_io_session *session,
                                    const uint8_t *input, size_t input_size, int level,
                                    byte_buffer *output,
                                    char *error_buffer, size_t error_buffer_size) {
    if (session == NULL || output == NULL) return VIPR_STATUS_INVALID_ARGUMENT;
    if (session->compress_context == NULL) {
        session->compress_context = ZSTD_createCCtx();
        if (session->compress_context == NULL) {
            vipr_set_error(error_buffer, error_buffer_size, "allocate V4 compression context");
            return VIPR_STATUS_MEMORY_LIMIT;
        }
    }
    size_t bound = ZSTD_compressBound(input_size);
    output->size = 0;
    if (!buffer_reserve(output, bound == 0 ? 1 : bound)) {
        vipr_set_error(error_buffer, error_buffer_size, "reserve V4 compression buffer");
        return VIPR_STATUS_MEMORY_LIMIT;
    }
    size_t code = ZSTD_CCtx_reset(session->compress_context, ZSTD_reset_session_only);
    if (!ZSTD_isError(code)) code = ZSTD_CCtx_setParameter(session->compress_context, ZSTD_c_compressionLevel, level);
    if (!ZSTD_isError(code)) code = ZSTD_CCtx_setParameter(session->compress_context, ZSTD_c_checksumFlag, 1);
    if (!ZSTD_isError(code)) {
        code = ZSTD_compress2(session->compress_context, output->data, output->capacity, input, input_size);
    }
    if (ZSTD_isError(code)) {
        vipr_set_zstd_error(error_buffer, error_buffer_size, "compress V4 window", code);
        return VIPR_STATUS_ZSTD_ERROR;
    }
    output->size = code;
    return VIPR_STATUS_OK;
}

static uint64_t candidate_score(uint8_t kind, size_t payload_size, size_t expanded_size,
                                uint16_t instruction_count, uint32_t source_size, uint8_t mode) {
    uint64_t score = payload_size;
    if (mode == 1) { // apply-speed
        if (kind == VIPR_WINDOW_DELTA_RAW || kind == VIPR_WINDOW_DELTA_ZSTD) score += (uint64_t)instruction_count * 32u + source_size / 16u;
        if (kind == VIPR_WINDOW_DELTA_ZSTD || kind == VIPR_WINDOW_REPLACE_ZSTD) score += expanded_size / 64u + 512u;
    } else if (mode == 0) { // balanced
        if (kind == VIPR_WINDOW_DELTA_RAW || kind == VIPR_WINDOW_DELTA_ZSTD) score += (uint64_t)instruction_count * 4u + source_size / 128u;
        if (kind == VIPR_WINDOW_DELTA_ZSTD || kind == VIPR_WINDOW_REPLACE_ZSTD) score += expanded_size / 512u;
    }
    return score;
}

static void set_source_chunks(vipr_window_result *result) {
    if (result->source_size == 0) { result->source_first_chunk = 0; result->source_chunk_count = 0; return; }
    uint64_t first = result->source_offset / VIPR_V4_IDENTITY_CHUNK_SIZE;
    uint64_t end = (result->source_offset + result->source_size + VIPR_V4_IDENTITY_CHUNK_SIZE - 1) / VIPR_V4_IDENTITY_CHUNK_SIZE;
    result->source_first_chunk = (uint32_t)first;
    result->source_chunk_count = (uint16_t)(end - first);
}

static vipr_status borrow_result(vipr_io_session *session) {
    session->window_result_borrowed = 1;
    return VIPR_STATUS_OK;
}

vipr_status vipr_build_window(vipr_io_session *session,
                              uint64_t source_size, uint64_t target_size,
                              uint64_t output_offset, uint32_t output_size,
                              uint32_t window_size, int compression_level, uint8_t optimization_mode,
                              volatile uint32_t *cancel, vipr_window_result *result,
                              char *error_buffer, size_t error_buffer_size) {
    if (session == NULL || result == NULL || session->window_result_borrowed || output_size == 0 || output_size > window_size ||
        output_offset > target_size || output_size > target_size - output_offset || optimization_mode > 2) {
        vipr_set_error(error_buffer, error_buffer_size, "invalid or busy V4 window session");
        return VIPR_STATUS_INVALID_ARGUMENT;
    }
    memset(result, 0, sizeof(*result));
    if (vipr_cancelled(cancel)) return VIPR_STATUS_CANCELLED;

    if (session->window_workspace == NULL) {
        session->window_workspace = vipr_window_workspace_create();
        if (session->window_workspace == NULL) {
            vipr_set_error(error_buffer, error_buffer_size, "allocate V4 window workspace");
            return VIPR_STATUS_MEMORY_LIMIT;
        }
    }
    vipr_window_workspace *workspace = session->window_workspace;
    buffer_reset(&workspace->sparse_delta);
    buffer_reset(&workspace->cdc_delta);
    buffer_reset(&workspace->replacement_zstd);
    buffer_reset(&workspace->delta_zstd);
    ops_reset(&workspace->sparse_ops);
    ops_reset(&workspace->cdc_ops);
    workspace->index.chunk_count = 0;

    if (!vipr_scratch_reserve(&workspace->target, output_size)) {
        vipr_set_error(error_buffer, error_buffer_size, "reserve V4 target window");
        return VIPR_STATUS_MEMORY_LIMIT;
    }
    uint8_t *target = workspace->target.data;
    vipr_status status = vipr_read_at(session, session->patch, output_offset, target, output_size,
                                      error_buffer, error_buffer_size);
    if (status != VIPR_STATUS_OK) return status;
    vipr_blake3_hash(target, output_size, result->digest);

    uint8_t run = 0;
    if (all_same_byte(target, output_size, &run)) {
        result->kind = run == 0 ? VIPR_WINDOW_ZERO : VIPR_WINDOW_RUN;
        result->codec = VIPR_CODEC_NONE;
        result->expanded_size = output_size;
        if (run != 0) {
            workspace->run_byte = run;
            result->payload = &workspace->run_byte;
            result->payload_size = 1;
        }
        return borrow_result(session);
    }

    uint64_t halo = window_size;
    uint64_t source_start = output_offset > halo ? output_offset - halo : 0;
    uint64_t source_end = output_offset + output_size;
    if (source_end < output_offset) source_end = source_size;
    if (source_end < source_size) {
        uint64_t extra = source_size - source_end < halo ? source_size - source_end : halo;
        source_end += extra;
    }
    if (source_end > source_size) source_end = source_size;
    uint64_t source_span64 = source_end >= source_start ? source_end - source_start : 0;
    if (source_span64 > UINT32_MAX) source_span64 = UINT32_MAX;
    uint32_t source_span = (uint32_t)source_span64;
    if (source_span != 0) {
        if (!vipr_scratch_reserve(&workspace->source, source_span)) {
            vipr_set_error(error_buffer, error_buffer_size, "reserve V4 source window");
            return VIPR_STATUS_MEMORY_LIMIT;
        }
        status = vipr_read_at(session, session->source, source_start, workspace->source.data, source_span,
                              error_buffer, error_buffer_size);
        if (status != VIPR_STATUS_OK) return status;
    }
    uint8_t *source = workspace->source.data;

    if (output_offset < source_size && output_size <= source_size - output_offset &&
        output_offset >= source_start && output_offset - source_start <= source_span &&
        output_size <= source_span - (uint32_t)(output_offset - source_start)) {
        const uint8_t *same_source = source + (size_t)(output_offset - source_start);
        if (memcmp(same_source, target, output_size) == 0) {
            result->kind = VIPR_WINDOW_SAME;
            result->codec = VIPR_CODEC_NONE;
            result->source_offset = output_offset;
            result->source_size = output_size;
            set_source_chunks(result);
            return borrow_result(session);
        }
    }

    op_list *selected_ops = NULL;
    byte_buffer *selected_delta = NULL;
    uint16_t instruction_count = 0;

    // Equal-offset sparse changes are common and cheaper than CDC matching.
    if (output_offset <= source_size && output_size <= source_size - output_offset &&
        output_offset >= source_start && output_offset - source_start <= source_span &&
        output_size <= source_span - (uint32_t)(output_offset - source_start)) {
        const uint8_t *same_source = source + (size_t)(output_offset - source_start);
        uint16_t sparse_count = 0;
        if (build_sparse_ops(same_source, target, output_size, output_offset, &workspace->sparse_ops) &&
            workspace->sparse_ops.copied_bytes >= output_size / 8u &&
            encode_delta(&workspace->sparse_ops, target, source_start, output_offset,
                         &workspace->sparse_delta, &sparse_count)) {
            selected_ops = &workspace->sparse_ops;
            selected_delta = &workspace->sparse_delta;
            instruction_count = sparse_count;
        }
    }

    if (source_span >= 1024 && output_size >= 1024) {
        uint32_t average = output_size < (64u << 10) ? 2048u : (output_size < (1u << 20) ? 8192u : 16384u);
        uint32_t minimum = average / 4u;
        uint32_t maximum = average * 4u;
        if (build_chunk_index(source, source_span, minimum, average, maximum, &workspace->index)) {
            uint16_t cdc_count = 0;
            target_matcher matcher = {source, target, &workspace->index, &workspace->cdc_ops, source_start, UINT64_MAX};
            if (for_each_chunk(target, output_size, minimum, average, maximum, match_target_chunk, &matcher) &&
                workspace->cdc_ops.copied_bytes >= output_size / 8u &&
                encode_delta(&workspace->cdc_ops, target, source_start, output_offset,
                             &workspace->cdc_delta, &cdc_count) &&
                (selected_delta == NULL || workspace->cdc_delta.size < selected_delta->size)) {
                selected_ops = &workspace->cdc_ops;
                selected_delta = &workspace->cdc_delta;
                instruction_count = cdc_count;
            }
        }
    }

    if (selected_ops != NULL && selected_ops->count == 1 &&
        selected_ops->items[0].kind == OP_COPY && selected_ops->items[0].length == output_size) {
        result->kind = VIPR_WINDOW_COPY;
        result->codec = VIPR_CODEC_NONE;
        result->source_offset = selected_ops->items[0].source_offset;
        result->source_size = output_size;
        set_source_chunks(result);
        return borrow_result(session);
    }

    status = compress_payload(session, target, output_size, compression_level,
                              &workspace->replacement_zstd, error_buffer, error_buffer_size);
    if (status != VIPR_STATUS_OK) return status;
    if (selected_delta != NULL) {
        status = compress_payload(session, selected_delta->data, selected_delta->size, compression_level,
                                  &workspace->delta_zstd, error_buffer, error_buffer_size);
        if (status != VIPR_STATUS_OK) return status;
    }

    uint8_t selected_kind = VIPR_WINDOW_REPLACE_RAW;
    uint8_t selected_codec = VIPR_CODEC_NONE;
    uint8_t *selected_data = target;
    size_t selected_size = output_size;
    size_t selected_expanded = output_size;
    uint16_t selected_instructions = 0;
    uint64_t best_score = candidate_score(selected_kind, selected_size, selected_expanded, 0, 0, optimization_mode);

    uint64_t score = candidate_score(VIPR_WINDOW_REPLACE_ZSTD, workspace->replacement_zstd.size,
                                     output_size, 0, 0, optimization_mode);
    if (score < best_score) {
        selected_kind = VIPR_WINDOW_REPLACE_ZSTD;
        selected_codec = VIPR_CODEC_ZSTD;
        selected_data = workspace->replacement_zstd.data;
        selected_size = workspace->replacement_zstd.size;
        best_score = score;
    }
    if (selected_delta != NULL) {
        score = candidate_score(VIPR_WINDOW_DELTA_RAW, selected_delta->size, selected_delta->size,
                                instruction_count, source_span, optimization_mode);
        if (score < best_score) {
            selected_kind = VIPR_WINDOW_DELTA_RAW;
            selected_codec = VIPR_CODEC_NONE;
            selected_data = selected_delta->data;
            selected_size = selected_delta->size;
            selected_expanded = selected_delta->size;
            selected_instructions = instruction_count;
            best_score = score;
        }
        score = candidate_score(VIPR_WINDOW_DELTA_ZSTD, workspace->delta_zstd.size, selected_delta->size,
                                instruction_count, source_span, optimization_mode);
        if (score < best_score) {
            selected_kind = VIPR_WINDOW_DELTA_ZSTD;
            selected_codec = VIPR_CODEC_ZSTD;
            selected_data = workspace->delta_zstd.data;
            selected_size = workspace->delta_zstd.size;
            selected_expanded = selected_delta->size;
            selected_instructions = instruction_count;
        }
    }

    if (selected_size > UINT32_MAX || selected_expanded > UINT32_MAX) {
        vipr_set_error(error_buffer, error_buffer_size, "V4 window payload exceeds format limits");
        return VIPR_STATUS_MEMORY_LIMIT;
    }
    result->payload = selected_data;
    result->payload_size = (uint32_t)selected_size;
    result->expanded_size = (uint32_t)selected_expanded;
    result->kind = selected_kind;
    result->codec = selected_codec;
    result->instruction_count = selected_instructions;
    if (selected_kind == VIPR_WINDOW_DELTA_RAW || selected_kind == VIPR_WINDOW_DELTA_ZSTD) {
        result->source_offset = source_start;
        result->source_size = source_span;
        set_source_chunks(result);
    }
    return borrow_result(session);
}

vipr_status vipr_write_window_payload(vipr_io_session *session,
                                      const vipr_window_result *result,
                                      uint64_t output_offset,
                                      char *error_buffer, size_t error_buffer_size) {
    if (session == NULL || result == NULL || !session->window_result_borrowed ||
        (result->payload_size != 0 && result->payload == NULL)) {
        vipr_set_error(error_buffer, error_buffer_size, "invalid borrowed V4 window result");
        return VIPR_STATUS_INVALID_ARGUMENT;
    }
    if (result->payload_size == 0) return VIPR_STATUS_OK;
    return vipr_write_at(session, session->output, output_offset, result->payload, result->payload_size,
                         error_buffer, error_buffer_size);
}

void vipr_window_result_release(vipr_io_session *session, vipr_window_result *result) {
    if (result != NULL) memset(result, 0, sizeof(*result));
    if (session != NULL) session->window_result_borrowed = 0;
}
