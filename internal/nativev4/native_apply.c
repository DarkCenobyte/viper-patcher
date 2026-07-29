#include "native_internal.h"

#include <stdlib.h>
#include <string.h>
#ifdef _WIN32
#include <windows.h>
#else
#include <sched.h>
#endif

static void vipr_yield_cpu(void) {
#ifdef _WIN32
    Sleep(0);
#else
    sched_yield();
#endif
}

static int digest_equal(const uint8_t left[32], const uint8_t right[32]) {
    uint8_t diff = 0;
    for (size_t i = 0; i < 32; ++i) diff |= left[i] ^ right[i];
    return diff == 0;
}

static vipr_status check_cancel(volatile uint32_t *cancel) {
    return cancel != NULL && *cancel != 0 ? VIPR_STATUS_CANCELLED : VIPR_STATUS_OK;
}

static vipr_status hash_source_chunk(vipr_io_session *session,
                                     uint32_t index,
                                     uint64_t source_file_size,
                                     const uint8_t *expected_digests,
                                     uint32_t digest_count,
                                     volatile uint32_t *states,
                                     volatile uint32_t *cancel,
                                     vipr_group_result *result,
                                     char *error_buffer,
                                     size_t error_buffer_size) {
    if (expected_digests == NULL || states == NULL) return VIPR_STATUS_OK;
    if (index >= digest_count) {
        vipr_set_error(error_buffer, error_buffer_size, "source chunk index exceeds digest table");
        return VIPR_STATUS_INVALID_WINDOW;
    }
    volatile uint32_t *state = &states[index];
    for (;;) {
        uint32_t value = __atomic_load_n(state, __ATOMIC_ACQUIRE);
        if (value == 2) return VIPR_STATUS_OK;
        if (value == 3) return VIPR_STATUS_SOURCE_MISMATCH;
        if (value == 0) {
            uint32_t expected = 0;
            if (__atomic_compare_exchange_n(state, &expected, 1, 0, __ATOMIC_ACQ_REL, __ATOMIC_ACQUIRE)) break;
            continue;
        }
        if (check_cancel(cancel) != VIPR_STATUS_OK) return VIPR_STATUS_CANCELLED;
        vipr_yield_cpu();
    }

    const uint64_t offset = (uint64_t)index * VIPR_V4_IDENTITY_CHUNK_SIZE;
    if (offset >= source_file_size) {
        __atomic_store_n(state, 3, __ATOMIC_RELEASE);
        vipr_set_error(error_buffer, error_buffer_size, "source chunk begins beyond source size");
        return VIPR_STATUS_SOURCE_MISMATCH;
    }
    uint64_t remaining = source_file_size - offset;
    size_t size = remaining > VIPR_V4_IDENTITY_CHUNK_SIZE ? VIPR_V4_IDENTITY_CHUNK_SIZE : (size_t)remaining;
    uint8_t *buffer = (uint8_t *)malloc(size == 0 ? 1 : size);
    if (buffer == NULL) {
        __atomic_store_n(state, 0, __ATOMIC_RELEASE);
        vipr_set_error(error_buffer, error_buffer_size, "allocate source verification buffer");
        return VIPR_STATUS_MEMORY_LIMIT;
    }
    vipr_status status = vipr_read_at(session->source, offset, buffer, size, error_buffer, error_buffer_size);
    if (status == VIPR_STATUS_OK) {
        uint8_t actual[32];
        vipr_blake3_hash(buffer, size, actual);
        if (!digest_equal(actual, expected_digests + (size_t)index * 32)) {
            status = VIPR_STATUS_SOURCE_MISMATCH;
            vipr_set_error(error_buffer, error_buffer_size, "source chunk digest mismatch");
        }
    }
    free(buffer);
    if (status == VIPR_STATUS_OK) {
        if (result != NULL) result->bytes_read_source += size;
        __atomic_store_n(state, 2, __ATOMIC_RELEASE);
    } else if (status == VIPR_STATUS_SOURCE_MISMATCH) {
        __atomic_store_n(state, 3, __ATOMIC_RELEASE);
    } else {
        __atomic_store_n(state, 0, __ATOMIC_RELEASE);
    }
    return status;
}

static vipr_status verify_source_range(vipr_io_session *session,
                                       const vipr_window *window,
                                       uint64_t source_file_size,
                                       const uint8_t *source_chunk_digests,
                                       uint32_t source_chunk_count,
                                       volatile uint32_t *source_chunk_states,
                                       volatile uint32_t *cancel,
                                       vipr_group_result *result,
                                       char *error_buffer,
                                       size_t error_buffer_size) {
    if (source_chunk_digests == NULL || source_chunk_states == NULL || window->source_chunk_count == 0) return VIPR_STATUS_OK;
    uint64_t end = (uint64_t)window->source_first_chunk + window->source_chunk_count;
    if (end > source_chunk_count) {
        vipr_set_error(error_buffer, error_buffer_size, "window source chunk range exceeds digest table");
        return VIPR_STATUS_INVALID_WINDOW;
    }
    for (uint32_t index = window->source_first_chunk; index < (uint32_t)end; ++index) {
        vipr_status status = hash_source_chunk(session, index, source_file_size, source_chunk_digests,
                                              source_chunk_count, source_chunk_states, cancel, result,
                                              error_buffer, error_buffer_size);
        if (status != VIPR_STATUS_OK) return status;
    }
    return VIPR_STATUS_OK;
}

static int read_u16(const uint8_t **cursor, const uint8_t *end, uint16_t *value) {
    if ((size_t)(end - *cursor) < 2) return 0;
    *value = (uint16_t)(*cursor)[0] | ((uint16_t)(*cursor)[1] << 8);
    *cursor += 2;
    return 1;
}

static int read_i16(const uint8_t **cursor, const uint8_t *end, int16_t *value) {
    uint16_t encoded;
    if (!read_u16(cursor, end, &encoded)) return 0;
    *value = (int16_t)encoded;
    return 1;
}

static int read_varint(const uint8_t **cursor, const uint8_t *end, uint64_t *value) {
    size_t position = 0;
    size_t size = (size_t)(end - *cursor);
    if (!vipr_read_uvarint(*cursor, size, &position, value)) return 0;
    *cursor += position;
    return 1;
}

static vipr_status read_copy(vipr_io_session *session,
                             uint64_t source_offset,
                             uint64_t source_file_size,
                             uint8_t *output,
                             uint32_t output_size,
                             uint32_t *produced,
                             uint64_t length,
                             volatile uint32_t *cancel,
                             vipr_group_result *result,
                             char *error_buffer,
                             size_t error_buffer_size) {
    if (length == 0 || source_offset > source_file_size || length > source_file_size - source_offset ||
        length > output_size - *produced) {
        vipr_set_error(error_buffer, error_buffer_size, "COPY instruction exceeds source or output bounds");
        return VIPR_STATUS_INVALID_WINDOW;
    }
    uint64_t copied = 0;
    while (copied < length) {
        if (check_cancel(cancel) != VIPR_STATUS_OK) return VIPR_STATUS_CANCELLED;
        size_t part = (size_t)(length - copied);
        if (part > (1u << 20)) part = 1u << 20;
        vipr_status status = vipr_read_at(session->source, source_offset + copied, output + *produced, part, error_buffer, error_buffer_size);
        if (status != VIPR_STATUS_OK) return status;
        *produced += (uint32_t)part;
        copied += part;
        if (result != NULL) result->bytes_read_source += part;
    }
    return VIPR_STATUS_OK;
}

static vipr_status decode_delta(vipr_io_session *session,
                                const vipr_window *window,
                                const uint8_t *stream,
                                size_t stream_size,
                                uint64_t source_file_size,
                                uint8_t *output,
                                volatile uint32_t *cancel,
                                vipr_group_result *result,
                                char *error_buffer,
                                size_t error_buffer_size) {
    static const uint8_t instruction_magic[8] = {'V','4','O','P','S','\r','\n',1};
    if (stream_size < 8 || memcmp(stream, instruction_magic, 8) != 0) {
        vipr_set_error(error_buffer, error_buffer_size, "invalid V4 instruction stream magic");
        return VIPR_STATUS_INVALID_WINDOW;
    }
    const uint8_t *cursor = stream + 8;
    const uint8_t *end = stream + stream_size;
    uint32_t produced = 0;
    uint64_t previous_copy_end = window->source_offset;
    uint32_t instruction_count = 0;

    while (cursor < end) {
        if (check_cancel(cancel) != VIPR_STATUS_OK) return VIPR_STATUS_CANCELLED;
        uint8_t opcode = *cursor++;
        instruction_count++;
        uint64_t source_offset = 0;
        uint64_t length = 0;
        switch (opcode) {
        case VIPR_OPCODE_END:
            if (cursor != end || produced != window->output_size ||
                (window->instruction_count != 0 && instruction_count - 1 != window->instruction_count)) {
                vipr_set_error(error_buffer, error_buffer_size, "invalid V4 instruction stream termination");
                return VIPR_STATUS_INVALID_WINDOW;
            }
            return VIPR_STATUS_OK;
        case VIPR_OPCODE_COPY_SAME_SHORT: {
            if (cursor >= end) goto malformed;
            length = (uint64_t)*cursor++ + 1;
            source_offset = window->output_offset + produced;
            break;
        }
        case VIPR_OPCODE_COPY_LOCAL_SHORT: {
            uint16_t offset16, length16;
            if (!read_u16(&cursor, end, &offset16) || !read_u16(&cursor, end, &length16)) goto malformed;
            source_offset = window->source_offset + offset16;
            length = (uint64_t)length16;
            break;
        }
        case VIPR_OPCODE_COPY_LOCAL_LONG: {
            uint64_t offset;
            if (!read_varint(&cursor, end, &offset) || !read_varint(&cursor, end, &length)) goto malformed;
            if (offset > UINT64_MAX - window->source_offset) goto malformed;
            source_offset = window->source_offset + offset;
            break;
        }
        case VIPR_OPCODE_COPY_DELTA_SHORT: {
            int16_t delta;
            uint16_t length16;
            if (!read_i16(&cursor, end, &delta) || !read_u16(&cursor, end, &length16)) goto malformed;
            if (delta < 0 && previous_copy_end < (uint64_t)(-(int32_t)delta)) goto malformed;
            source_offset = delta < 0 ? previous_copy_end - (uint64_t)(-(int32_t)delta) : previous_copy_end + (uint64_t)delta;
            length = (uint64_t)length16;
            break;
        }
        case VIPR_OPCODE_COPY_ABSOLUTE:
            if (!read_varint(&cursor, end, &source_offset) || !read_varint(&cursor, end, &length)) goto malformed;
            break;
        case VIPR_OPCODE_ADD_SHORT: {
            if (cursor >= end) goto malformed;
            length = (uint64_t)*cursor++;
            if (length == 0 || length > (uint64_t)(end - cursor) || length > window->output_size - produced) goto malformed;
            memcpy(output + produced, cursor, (size_t)length);
            cursor += length;
            produced += (uint32_t)length;
            continue;
        }
        case VIPR_OPCODE_ADD_LONG:
            if (!read_varint(&cursor, end, &length) || length == 0 || length > (uint64_t)(end - cursor) || length > window->output_size - produced) goto malformed;
            memcpy(output + produced, cursor, (size_t)length);
            cursor += length;
            produced += (uint32_t)length;
            continue;
        case VIPR_OPCODE_RUN_BYTE: {
            if (cursor >= end) goto malformed;
            uint8_t value = *cursor++;
            if (!read_varint(&cursor, end, &length) || length == 0 || length > window->output_size - produced) goto malformed;
            memset(output + produced, value, (size_t)length);
            produced += (uint32_t)length;
            continue;
        }
        case VIPR_OPCODE_ZERO:
            if (!read_varint(&cursor, end, &length) || length == 0 || length > window->output_size - produced) goto malformed;
            memset(output + produced, 0, (size_t)length);
            produced += (uint32_t)length;
            continue;
        case VIPR_OPCODE_COPY_ADD_PAIR: {
            uint64_t local_offset, copy_length;
            if (!read_varint(&cursor, end, &local_offset) || !read_varint(&cursor, end, &copy_length) || cursor >= end) goto malformed;
            uint64_t add_length = (uint64_t)*cursor++;
            if (add_length == 0 || local_offset > UINT64_MAX - window->source_offset) goto malformed;
            vipr_status status = read_copy(session, window->source_offset + local_offset, source_file_size, output,
                                           window->output_size, &produced, copy_length, cancel, result,
                                           error_buffer, error_buffer_size);
            if (status != VIPR_STATUS_OK) return status;
            if (add_length > (uint64_t)(end - cursor) || add_length > window->output_size - produced) goto malformed;
            memcpy(output + produced, cursor, (size_t)add_length);
            cursor += add_length;
            produced += (uint32_t)add_length;
            previous_copy_end = window->source_offset + local_offset + copy_length;
            continue;
        }
        default:
            vipr_set_error(error_buffer, error_buffer_size, "unsupported V4 instruction opcode");
            return VIPR_STATUS_INVALID_WINDOW;
        }
        {
            vipr_status status = read_copy(session, source_offset, source_file_size, output, window->output_size,
                                           &produced, length, cancel, result, error_buffer, error_buffer_size);
            if (status != VIPR_STATUS_OK) return status;
            previous_copy_end = source_offset + length;
        }
    }
malformed:
    vipr_set_error(error_buffer, error_buffer_size, "malformed V4 instruction stream");
    return VIPR_STATUS_INVALID_WINDOW;
}

static vipr_status load_payload(vipr_io_session *session,
                                const vipr_window *window,
                                uint8_t **payload,
                                size_t *payload_size,
                                volatile uint32_t *cancel,
                                vipr_group_result *result,
                                char *error_buffer,
                                size_t error_buffer_size) {
    *payload = NULL;
    *payload_size = 0;
    if (window->payload_size == 0) return VIPR_STATUS_OK;
    if (check_cancel(cancel) != VIPR_STATUS_OK) return VIPR_STATUS_CANCELLED;
    uint8_t *compressed = (uint8_t *)malloc(window->payload_size);
    if (compressed == NULL) {
        vipr_set_error(error_buffer, error_buffer_size, "allocate V4 payload buffer");
        return VIPR_STATUS_MEMORY_LIMIT;
    }
    vipr_status status = vipr_read_at(session->patch, window->payload_offset, compressed, window->payload_size, error_buffer, error_buffer_size);
    if (status != VIPR_STATUS_OK) {
        free(compressed);
        return status;
    }
    if (result != NULL) result->bytes_read_patch += window->payload_size;
    if (window->codec == VIPR_CODEC_NONE) {
        *payload = compressed;
        *payload_size = window->payload_size;
        return VIPR_STATUS_OK;
    }
    if (window->codec != VIPR_CODEC_ZSTD || window->expanded_size == 0) {
        free(compressed);
        vipr_set_error(error_buffer, error_buffer_size, "invalid V4 payload codec metadata");
        return VIPR_STATUS_INVALID_WINDOW;
    }
    uint8_t *expanded = (uint8_t *)malloc(window->expanded_size);
    if (expanded == NULL) {
        free(compressed);
        vipr_set_error(error_buffer, error_buffer_size, "allocate V4 expanded payload buffer");
        return VIPR_STATUS_MEMORY_LIMIT;
    }
    size_t decoded = ZSTD_decompress(expanded, window->expanded_size, compressed, window->payload_size);
    free(compressed);
    if (ZSTD_isError(decoded) || decoded != window->expanded_size) {
        free(expanded);
        vipr_set_zstd_error(error_buffer, error_buffer_size, "decompress V4 payload", decoded);
        return VIPR_STATUS_ZSTD_ERROR;
    }
    *payload = expanded;
    *payload_size = decoded;
    return VIPR_STATUS_OK;
}

static vipr_status materialize_window(vipr_io_session *session,
                                      const vipr_window *window,
                                      uint64_t source_file_size,
                                      const uint8_t *source_chunk_digests,
                                      uint32_t source_chunk_count,
                                      volatile uint32_t *source_chunk_states,
                                      uint8_t *output,
                                      int verify_window_digest,
                                      volatile uint32_t *cancel,
                                      vipr_group_result *result,
                                      char *error_buffer,
                                      size_t error_buffer_size) {
    if (window == NULL || output == NULL || window->output_size == 0) {
        vipr_set_error(error_buffer, error_buffer_size, "invalid V4 window destination");
        return VIPR_STATUS_INVALID_ARGUMENT;
    }
    vipr_status status = verify_source_range(session, window, source_file_size, source_chunk_digests,
                                             source_chunk_count, source_chunk_states, cancel, result,
                                             error_buffer, error_buffer_size);
    if (status != VIPR_STATUS_OK) return status;

    uint8_t *payload = NULL;
    size_t payload_size = 0;
    switch (window->kind) {
    case VIPR_WINDOW_SAME:
        status = read_copy(session, window->output_offset, source_file_size, output, window->output_size,
                           &(uint32_t){0}, window->output_size, cancel, result, error_buffer, error_buffer_size);
        break;
    case VIPR_WINDOW_COPY:
        status = read_copy(session, window->source_offset, source_file_size, output, window->output_size,
                           &(uint32_t){0}, window->output_size, cancel, result, error_buffer, error_buffer_size);
        break;
    case VIPR_WINDOW_ZERO:
        memset(output, 0, window->output_size);
        break;
    case VIPR_WINDOW_RUN:
        if (window->payload_size != 1 || window->codec != VIPR_CODEC_NONE) {
            status = VIPR_STATUS_INVALID_WINDOW;
            vipr_set_error(error_buffer, error_buffer_size, "invalid RUN window payload");
            break;
        }
        status = load_payload(session, window, &payload, &payload_size, cancel, result, error_buffer, error_buffer_size);
        if (status == VIPR_STATUS_OK) memset(output, payload[0], window->output_size);
        break;
    case VIPR_WINDOW_REPLACE_RAW:
    case VIPR_WINDOW_REPLACE_ZSTD:
        status = load_payload(session, window, &payload, &payload_size, cancel, result, error_buffer, error_buffer_size);
        if (status == VIPR_STATUS_OK) {
            if (payload_size != window->output_size) {
                status = VIPR_STATUS_INVALID_WINDOW;
                vipr_set_error(error_buffer, error_buffer_size, "replacement payload size mismatch");
            } else {
                memcpy(output, payload, payload_size);
            }
        }
        break;
    case VIPR_WINDOW_DELTA_RAW:
    case VIPR_WINDOW_DELTA_ZSTD:
        status = load_payload(session, window, &payload, &payload_size, cancel, result, error_buffer, error_buffer_size);
        if (status == VIPR_STATUS_OK) {
            status = decode_delta(session, window, payload, payload_size, source_file_size, output, cancel,
                                  result, error_buffer, error_buffer_size);
        }
        break;
    default:
        status = VIPR_STATUS_INVALID_WINDOW;
        vipr_set_error(error_buffer, error_buffer_size, "unsupported V4 window kind");
        break;
    }
    free(payload);
    if (status != VIPR_STATUS_OK) return status;
    if (verify_window_digest) {
        uint8_t digest[32];
        vipr_blake3_hash(output, window->output_size, digest);
        if (!digest_equal(digest, window->digest)) {
            vipr_set_error(error_buffer, error_buffer_size, "V4 window output digest mismatch");
            return VIPR_STATUS_OUTPUT_MISMATCH;
        }
    }
    if (result != NULL) result->windows_completed++;
    return VIPR_STATUS_OK;
}

vipr_status vipr_apply_group(vipr_io_session *session,
                             const vipr_window *windows, uint32_t window_count,
                             uint64_t group_offset, uint32_t group_size,
                             uint64_t source_file_size,
                             const uint8_t *source_chunk_digests, uint32_t source_chunk_count,
                             volatile uint32_t *source_chunk_states,
                             const uint8_t expected_group_digest[32],
                             volatile uint32_t *cancel,
                             vipr_group_result *result,
                             char *error_buffer, size_t error_buffer_size) {
    if (session == NULL || windows == NULL || window_count == 0 || group_size == 0 || expected_group_digest == NULL) {
        vipr_set_error(error_buffer, error_buffer_size, "invalid V4 group arguments");
        return VIPR_STATUS_INVALID_ARGUMENT;
    }
    if (result != NULL) memset(result, 0, sizeof(*result));
    uint8_t *buffer = (uint8_t *)malloc(group_size);
    if (buffer == NULL) {
        vipr_set_error(error_buffer, error_buffer_size, "allocate V4 output group");
        return VIPR_STATUS_MEMORY_LIMIT;
    }
    memset(buffer, 0, group_size);
    vipr_status status = VIPR_STATUS_OK;
    uint64_t expected_offset = group_offset;
    for (uint32_t index = 0; index < window_count; ++index) {
        const vipr_window *window = &windows[index];
        if (window->output_offset != expected_offset || window->output_offset < group_offset ||
            window->output_size > group_size - (uint32_t)(window->output_offset - group_offset)) {
            status = VIPR_STATUS_INVALID_WINDOW;
            vipr_set_error(error_buffer, error_buffer_size, "V4 group windows are not contiguous");
            break;
        }
        status = materialize_window(session, window, source_file_size, source_chunk_digests,
                                    source_chunk_count, source_chunk_states,
                                    buffer + (size_t)(window->output_offset - group_offset), 0, cancel, result,
                                    error_buffer, error_buffer_size);
        if (status != VIPR_STATUS_OK) break;
        expected_offset += window->output_size;
    }
    if (status == VIPR_STATUS_OK && expected_offset != group_offset + group_size) {
        status = VIPR_STATUS_INVALID_WINDOW;
        vipr_set_error(error_buffer, error_buffer_size, "V4 group does not cover its declared output range");
    }
    if (status == VIPR_STATUS_OK) {
        uint8_t digest[32];
        vipr_blake3_hash(buffer, group_size, digest);
        if (!digest_equal(digest, expected_group_digest)) {
            status = VIPR_STATUS_OUTPUT_MISMATCH;
            vipr_set_error(error_buffer, error_buffer_size, "V4 output group digest mismatch");
        }
    }
    if (status == VIPR_STATUS_OK) {
        status = vipr_write_at(session->output, group_offset, buffer, group_size, error_buffer, error_buffer_size);
        if (status == VIPR_STATUS_OK && result != NULL) result->bytes_written += group_size;
    }
    free(buffer);
    return status;
}

vipr_status vipr_apply_changed_window(vipr_io_session *session, const vipr_window *window,
                                      uint64_t source_file_size,
                                      const uint8_t *source_chunk_digests, uint32_t source_chunk_count,
                                      volatile uint32_t *source_chunk_states,
                                      volatile uint32_t *cancel,
                                      vipr_group_result *result,
                                      char *error_buffer, size_t error_buffer_size) {
    if (session == NULL || window == NULL) {
        vipr_set_error(error_buffer, error_buffer_size, "invalid V4 changed-window arguments");
        return VIPR_STATUS_INVALID_ARGUMENT;
    }
    if (result != NULL) memset(result, 0, sizeof(*result));
    uint8_t *buffer = (uint8_t *)malloc(window->output_size);
    if (buffer == NULL) {
        vipr_set_error(error_buffer, error_buffer_size, "allocate V4 window output");
        return VIPR_STATUS_MEMORY_LIMIT;
    }
    vipr_status status = materialize_window(session, window, source_file_size, source_chunk_digests,
                                            source_chunk_count, source_chunk_states, buffer, 1, cancel, result,
                                            error_buffer, error_buffer_size);
    if (status == VIPR_STATUS_OK && window->kind != VIPR_WINDOW_SAME) {
        status = vipr_write_at(session->output, window->output_offset, buffer, window->output_size, error_buffer, error_buffer_size);
        if (status == VIPR_STATUS_OK && result != NULL) result->bytes_written += window->output_size;
    }
    free(buffer);
    return status;
}
