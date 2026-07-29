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
    int32_t *buckets;
    uint32_t bucket_count;
} chunk_index;

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

static int ops_reserve(op_list *list) {
    if (list->count < list->capacity) return 1;
    size_t next_capacity = list->capacity == 0 ? 64 : list->capacity * 2;
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

static uint64_t splitmix64(uint64_t value) {
    value += 0x9e3779b97f4a7c15ULL;
    value = (value ^ (value >> 30)) * 0xbf58476d1ce4e5b9ULL;
    value = (value ^ (value >> 27)) * 0x94d049bb133111ebULL;
    return value ^ (value >> 31);
}

static uint64_t gear_value(uint8_t value) { return splitmix64((uint64_t)value + 0x243f6a8885a308d3ULL); }

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
        gear = (gear << 1) + gear_value(data[index]);
        uint32_t length = index + 1 - start;
        if (length < minimum) continue;
        if (length < maximum && (gear & (average - 1u)) != 0) continue;
        if (!callback(start, length, context)) return 0;
        start = index + 1; gear = 0;
    }
    if (start < size && !callback(start, size - start, context)) return 0;
    return 1;
}

typedef struct { const uint8_t *data; chunk_index *index; uint32_t next; } index_builder;

static int count_chunk(uint32_t offset, uint32_t length, void *context) {
    (void)offset; (void)length; (*(uint32_t *)context)++; return 1;
}

static int add_index_chunk(uint32_t offset, uint32_t length, void *context) {
    index_builder *builder = (index_builder *)context;
    indexed_chunk *chunk = &builder->index->chunks[builder->next++];
    chunk->hash = vipr_hash64(builder->data + offset, length);
    chunk->offset = offset; chunk->length = length;
    uint32_t bucket = (uint32_t)chunk->hash & (builder->index->bucket_count - 1u);
    chunk->next = builder->index->buckets[bucket];
    builder->index->buckets[bucket] = (int32_t)(builder->next - 1);
    return 1;
}

static int build_chunk_index(const uint8_t *data, uint32_t size, uint32_t minimum, uint32_t average, uint32_t maximum,
                             chunk_index *index) {
    memset(index, 0, sizeof(*index));
    uint32_t count = 0;
    if (!for_each_chunk(data, size, minimum, average, maximum, count_chunk, &count)) return 0;
    index->chunk_count = count;
    index->bucket_count = next_power_of_two(count * 2u + 1u);
    index->chunks = count == 0 ? NULL : (indexed_chunk *)calloc(count, sizeof(indexed_chunk));
    index->buckets = (int32_t *)malloc((size_t)index->bucket_count * sizeof(int32_t));
    if ((count != 0 && index->chunks == NULL) || index->buckets == NULL) {
        free(index->chunks);
        free(index->buckets);
        memset(index, 0, sizeof(*index));
        return 0;
    }
    for (uint32_t i = 0; i < index->bucket_count; ++i) index->buckets[i] = -1;
    index_builder builder = {data, index, 0};
    if (!for_each_chunk(data, size, minimum, average, maximum, add_index_chunk, &builder)) {
        free(index->chunks);
        free(index->buckets);
        memset(index, 0, sizeof(*index));
        return 0;
    }
    return 1;
}

static void free_chunk_index(chunk_index *index) { free(index->chunks); free(index->buckets); memset(index, 0, sizeof(*index)); }

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

static vipr_status compress_payload(const uint8_t *input, size_t input_size, int level,
                                    uint8_t **output, size_t *output_size,
                                    char *error_buffer, size_t error_buffer_size) {
    size_t bound = ZSTD_compressBound(input_size);
    uint8_t *buffer = (uint8_t *)malloc(bound == 0 ? 1 : bound);
    if (buffer == NULL) return VIPR_STATUS_MEMORY_LIMIT;
    ZSTD_CCtx *context = ZSTD_createCCtx();
    if (context == NULL) { free(buffer); return VIPR_STATUS_MEMORY_LIMIT; }
    size_t code = ZSTD_CCtx_setParameter(context, ZSTD_c_compressionLevel, level);
    if (!ZSTD_isError(code)) code = ZSTD_CCtx_setParameter(context, ZSTD_c_checksumFlag, 1);
    if (!ZSTD_isError(code)) code = ZSTD_compress2(context, buffer, bound, input, input_size);
    ZSTD_freeCCtx(context);
    if (ZSTD_isError(code)) { vipr_set_zstd_error(error_buffer, error_buffer_size, "compress V4 window", code); free(buffer); return VIPR_STATUS_ZSTD_ERROR; }
    *output = buffer; *output_size = code; return VIPR_STATUS_OK;
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

vipr_status vipr_build_window(vipr_io_session *session,
                              uint64_t source_size, uint64_t target_size,
                              uint64_t output_offset, uint32_t output_size,
                              uint32_t window_size, int compression_level, uint8_t optimization_mode,
                              volatile uint32_t *cancel, vipr_window_result *result,
                              char *error_buffer, size_t error_buffer_size) {
    if (session == NULL || result == NULL || output_size == 0 || output_size > window_size ||
        output_offset > target_size || output_size > target_size - output_offset || optimization_mode > 2) return VIPR_STATUS_INVALID_ARGUMENT;
    memset(result, 0, sizeof(*result));
    if (vipr_cancelled(cancel)) return VIPR_STATUS_CANCELLED;
    uint8_t *target = (uint8_t *)malloc(output_size);
    if (target == NULL) return VIPR_STATUS_MEMORY_LIMIT;
    vipr_status status = vipr_read_at(session->patch, output_offset, target, output_size, error_buffer, error_buffer_size);
    if (status != VIPR_STATUS_OK) { free(target); return status; }
    vipr_blake3_hash(target, output_size, result->digest);

    uint8_t run = 0;
    if (all_same_byte(target, output_size, &run)) {
        result->kind = run == 0 ? VIPR_WINDOW_ZERO : VIPR_WINDOW_RUN;
        result->codec = VIPR_CODEC_NONE;
        result->expanded_size = output_size;
        if (run != 0) {
            result->payload = (uint8_t *)malloc(1);
            if (result->payload == NULL) { free(target); return VIPR_STATUS_MEMORY_LIMIT; }
            result->payload[0] = run; result->payload_size = 1;
        }
        free(target); return VIPR_STATUS_OK;
    }

    uint8_t *same = NULL;
    if (output_offset < source_size && output_size <= source_size - output_offset) {
        same = (uint8_t *)malloc(output_size);
        if (same == NULL) { free(target); return VIPR_STATUS_MEMORY_LIMIT; }
        status = vipr_read_at(session->source, output_offset, same, output_size, error_buffer, error_buffer_size);
        if (status != VIPR_STATUS_OK) { free(same); free(target); return status; }
        if (memcmp(same, target, output_size) == 0) {
            result->kind = VIPR_WINDOW_SAME; result->codec = VIPR_CODEC_NONE;
            result->source_offset = output_offset; result->source_size = output_size; set_source_chunks(result);
            free(same); free(target); return VIPR_STATUS_OK;
        }
    }
    free(same);

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
    uint8_t *source = source_span == 0 ? NULL : (uint8_t *)malloc(source_span);
    if (source_span != 0 && source == NULL) { free(target); return VIPR_STATUS_MEMORY_LIMIT; }
    if (source_span != 0) {
        status = vipr_read_at(session->source, source_start, source, source_span, error_buffer, error_buffer_size);
        if (status != VIPR_STATUS_OK) { free(source); free(target); return status; }
    }

    op_list ops = {0};
    byte_buffer delta = {0};
    uint16_t instruction_count = 0;
    int have_delta = 0;

    // Equal-offset sparse changes are common in binary updates and should not
    // be forced through content-defined matching. This path turns long equal
    // runs into COPY and only stores the changed bytes.
    if (output_offset <= source_size && output_size <= source_size - output_offset &&
        output_offset >= source_start && output_offset - source_start <= source_span &&
        output_size <= source_span - (uint32_t)(output_offset - source_start)) {
        op_list sparse_ops = {0};
        byte_buffer sparse_delta = {0};
        uint16_t sparse_count = 0;
        const uint8_t *same_source = source + (size_t)(output_offset - source_start);
        if (build_sparse_ops(same_source, target, output_size, output_offset, &sparse_ops) &&
            sparse_ops.copied_bytes >= output_size / 8u &&
            encode_delta(&sparse_ops, target, source_start, output_offset, &sparse_delta, &sparse_count)) {
            ops = sparse_ops;
            delta = sparse_delta;
            instruction_count = sparse_count;
            have_delta = 1;
        } else {
            free(sparse_ops.items);
            free(sparse_delta.data);
        }
    }

    if (source_span >= 1024 && output_size >= 1024) {
        uint32_t average = output_size < (64u << 10) ? 2048u : (output_size < (1u << 20) ? 8192u : 16384u);
        uint32_t minimum = average / 4u;
        uint32_t maximum = average * 4u;
        chunk_index index;
        if (build_chunk_index(source, source_span, minimum, average, maximum, &index)) {
            op_list cdc_ops = {0};
            byte_buffer cdc_delta = {0};
            uint16_t cdc_count = 0;
            target_matcher matcher = {source, target, &index, &cdc_ops, source_start, UINT64_MAX};
            if (for_each_chunk(target, output_size, minimum, average, maximum, match_target_chunk, &matcher) &&
                cdc_ops.copied_bytes >= output_size / 8u && encode_delta(&cdc_ops, target, source_start, output_offset, &cdc_delta, &cdc_count)) {
                if (!have_delta || cdc_delta.size < delta.size) {
                    free(ops.items); free(delta.data);
                    ops = cdc_ops; delta = cdc_delta; instruction_count = cdc_count; have_delta = 1;
                } else {
                    free(cdc_ops.items); free(cdc_delta.data);
                }
            } else {
                free(cdc_ops.items); free(cdc_delta.data);
            }
            free_chunk_index(&index);
        }
    }
    if (have_delta && ops.count == 1 && ops.items[0].kind == OP_COPY && ops.items[0].length == output_size) {
        result->kind = VIPR_WINDOW_COPY; result->codec = VIPR_CODEC_NONE;
        result->source_offset = ops.items[0].source_offset; result->source_size = output_size; set_source_chunks(result);
        free(ops.items); free(delta.data); free(source); free(target); return VIPR_STATUS_OK;
    }

    uint8_t *replacement_zstd = NULL; size_t replacement_zstd_size = 0;
    status = compress_payload(target, output_size, compression_level, &replacement_zstd, &replacement_zstd_size, error_buffer, error_buffer_size);
    if (status != VIPR_STATUS_OK) { free(ops.items); free(delta.data); free(source); free(target); return status; }

    uint8_t *delta_zstd = NULL; size_t delta_zstd_size = 0;
    if (have_delta) {
        status = compress_payload(delta.data, delta.size, compression_level, &delta_zstd, &delta_zstd_size, error_buffer, error_buffer_size);
        if (status != VIPR_STATUS_OK) { free(replacement_zstd); free(ops.items); free(delta.data); free(source); free(target); return status; }
    }

    uint8_t selected_kind = VIPR_WINDOW_REPLACE_RAW;
    uint8_t selected_codec = VIPR_CODEC_NONE;
    uint8_t *selected_data = target;
    size_t selected_size = output_size;
    size_t selected_expanded = output_size;
    uint16_t selected_instructions = 0;
    uint64_t best_score = candidate_score(selected_kind, selected_size, selected_expanded, 0, 0, optimization_mode);

    uint64_t score = candidate_score(VIPR_WINDOW_REPLACE_ZSTD, replacement_zstd_size, output_size, 0, 0, optimization_mode);
    if (score < best_score) { selected_kind = VIPR_WINDOW_REPLACE_ZSTD; selected_codec = VIPR_CODEC_ZSTD; selected_data = replacement_zstd; selected_size = replacement_zstd_size; best_score = score; }
    if (have_delta) {
        score = candidate_score(VIPR_WINDOW_DELTA_RAW, delta.size, delta.size, instruction_count, source_span, optimization_mode);
        if (score < best_score) { selected_kind = VIPR_WINDOW_DELTA_RAW; selected_codec = VIPR_CODEC_NONE; selected_data = delta.data; selected_size = delta.size; selected_expanded = delta.size; selected_instructions = instruction_count; best_score = score; }
        score = candidate_score(VIPR_WINDOW_DELTA_ZSTD, delta_zstd_size, delta.size, instruction_count, source_span, optimization_mode);
        if (score < best_score) { selected_kind = VIPR_WINDOW_DELTA_ZSTD; selected_codec = VIPR_CODEC_ZSTD; selected_data = delta_zstd; selected_size = delta_zstd_size; selected_expanded = delta.size; selected_instructions = instruction_count; }
    }

    result->payload = (uint8_t *)malloc(selected_size == 0 ? 1 : selected_size);
    if (result->payload == NULL) { free(delta_zstd); free(replacement_zstd); free(ops.items); free(delta.data); free(source); free(target); return VIPR_STATUS_MEMORY_LIMIT; }
    memcpy(result->payload, selected_data, selected_size);
    result->payload_size = (uint32_t)selected_size;
    result->expanded_size = (uint32_t)selected_expanded;
    result->kind = selected_kind; result->codec = selected_codec; result->instruction_count = selected_instructions;
    if (selected_kind == VIPR_WINDOW_DELTA_RAW || selected_kind == VIPR_WINDOW_DELTA_ZSTD) {
        result->source_offset = source_start; result->source_size = source_span; set_source_chunks(result);
    }

    free(delta_zstd); free(replacement_zstd); free(ops.items); free(delta.data); free(source); free(target);
    return VIPR_STATUS_OK;
}

void vipr_window_result_free(vipr_window_result *result) {
    if (result == NULL) return;
    free(result->payload);
    memset(result, 0, sizeof(*result));
}
