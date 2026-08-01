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

// Decode the stable 88-byte V4 wire descriptor instead of sharing a C struct
// array with Go. This keeps the cgo boundary independent of ABI padding and
// uint64 alignment on 32-bit targets.
static uint16_t load_le16(const uint8_t *value) {
    return (uint16_t)value[0] | ((uint16_t)value[1] << 8);
}

static uint32_t load_le32(const uint8_t *value) {
    return (uint32_t)value[0] |
           ((uint32_t)value[1] << 8) |
           ((uint32_t)value[2] << 16) |
           ((uint32_t)value[3] << 24);
}

static uint64_t load_le64(const uint8_t *value) {
    return (uint64_t)load_le32(value) | ((uint64_t)load_le32(value + 4) << 32);
}

static vipr_status decode_window_descriptor(const uint8_t *encoded,
                                            vipr_window *window,
                                            char *error_buffer,
                                            size_t error_buffer_size) {
    if (encoded == NULL || window == NULL) {
        vipr_set_error(error_buffer, error_buffer_size, "missing V4 window descriptor");
        return VIPR_STATUS_INVALID_ARGUMENT;
    }
    if (load_le32(encoded + 84) != 0) {
        vipr_set_error(error_buffer, error_buffer_size, "V4 window descriptor reserved field is non-zero");
        return VIPR_STATUS_INVALID_WINDOW;
    }
    memset(window, 0, sizeof(*window));
    window->output_offset = load_le64(encoded + 0);
    window->output_size = load_le32(encoded + 8);
    window->kind = encoded[12];
    window->codec = encoded[13];
    window->flags = load_le16(encoded + 14);
    window->payload_offset = load_le64(encoded + 16);
    window->payload_size = load_le32(encoded + 24);
    window->expanded_size = load_le32(encoded + 28);
    window->source_offset = load_le64(encoded + 32);
    window->source_size = load_le32(encoded + 40);
    window->source_first_chunk = load_le32(encoded + 44);
    window->source_chunk_count = load_le16(encoded + 48);
    window->instruction_count = load_le16(encoded + 50);
    memcpy(window->digest, encoded + 52, sizeof(window->digest));
    return VIPR_STATUS_OK;
}

static int digest_equal(const uint8_t left[32], const uint8_t right[32]) {
    uint8_t diff = 0;
    for (size_t i = 0; i < 32; ++i) diff |= left[i] ^ right[i];
    return diff == 0;
}

static vipr_status check_cancel(volatile uint32_t *cancel) {
    return vipr_cancelled(cancel) ? VIPR_STATUS_CANCELLED : VIPR_STATUS_OK;
}

static vipr_status hash_source_chunk(vipr_io_session *session,
                                     uint32_t index,
                                     uint64_t source_file_size,
                                     const uint8_t *expected_digests,
                                     uint32_t digest_count,
                                     volatile uint32_t *states,
                                     const uint8_t *source_cache,
                                     uint64_t source_cache_size,
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
    const uint8_t *buffer = NULL;
    vipr_status status = VIPR_STATUS_OK;
    if (source_cache != NULL && offset <= source_cache_size && size <= source_cache_size - offset) {
        buffer = source_cache + (size_t)offset;
    } else {
        session->verification_cache_valid = 0;
        if (!vipr_scratch_reserve(&session->verification_buffer, size == 0 ? 1 : size)) {
            __atomic_store_n(state, 0, __ATOMIC_RELEASE);
            vipr_set_error(error_buffer, error_buffer_size, "reserve source verification buffer");
            return VIPR_STATUS_MEMORY_LIMIT;
        }
        buffer = session->verification_buffer.data;
        status = vipr_read_at(session, session->source, offset, (void *)buffer, size, error_buffer, error_buffer_size);
        if (status == VIPR_STATUS_OK && result != NULL) result->bytes_read_source += size;
    }
    if (status == VIPR_STATUS_OK) {
        uint8_t actual[32];
        vipr_blake3_hash(buffer, size, actual);
        if (!digest_equal(actual, expected_digests + (size_t)index * 32)) {
            status = VIPR_STATUS_SOURCE_MISMATCH;
            vipr_set_error(error_buffer, error_buffer_size, "source chunk digest mismatch");
        }
        if (status == VIPR_STATUS_OK && source_cache == NULL) {
            session->verification_cache_offset = offset;
            session->verification_cache_size = size;
            session->verification_cache_valid = 1;
        }
    }
    if (status == VIPR_STATUS_OK) {
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
                                       const uint8_t *source_cache,
                                       uint64_t source_cache_size,
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

    uint32_t preferred = UINT32_MAX;
    uint64_t preferred_offset = window->kind == VIPR_WINDOW_SAME
                                    ? window->output_offset
                                    : window->source_offset;
    uint64_t preferred64 = preferred_offset / VIPR_V4_IDENTITY_CHUNK_SIZE;
    if (preferred64 >= window->source_first_chunk && preferred64 < end) {
        preferred = (uint32_t)preferred64;
    }

    for (uint32_t index = window->source_first_chunk; index < (uint32_t)end; ++index) {
        if (index == preferred) continue;
        vipr_status status = hash_source_chunk(session, index, source_file_size, source_chunk_digests,
                                              source_chunk_count, source_chunk_states,
                                              source_cache, source_cache_size, cancel, result,
                                              error_buffer, error_buffer_size);
        if (status != VIPR_STATUS_OK) return status;
    }
    if (preferred != UINT32_MAX) {
        vipr_status status = hash_source_chunk(session, preferred, source_file_size,
                                              source_chunk_digests, source_chunk_count,
                                              source_chunk_states, source_cache,
                                              source_cache_size, cancel, result,
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
                             const vipr_window *window,
                             uint64_t source_offset,
                             uint64_t source_file_size,
                             uint8_t *output,
                             uint32_t output_size,
                             uint32_t *produced,
                             uint64_t length,
                             const uint8_t *source_cache,
                             uint64_t source_cache_size,
                             volatile uint32_t *cancel,
                             vipr_group_result *result,
                             char *error_buffer,
                             size_t error_buffer_size) {
    if (window == NULL || produced == NULL || *produced > output_size || length == 0 ||
        source_offset > source_file_size || length > source_file_size - source_offset ||
        length > output_size - *produced) {
        vipr_set_error(error_buffer, error_buffer_size, "COPY instruction exceeds file or output bounds");
        return VIPR_STATUS_INVALID_WINDOW;
    }
    if (source_offset < window->source_offset) {
        vipr_set_error(error_buffer, error_buffer_size, "COPY instruction begins before declared source span");
        return VIPR_STATUS_INVALID_WINDOW;
    }
    uint64_t relative = source_offset - window->source_offset;
    if (relative > window->source_size || length > (uint64_t)window->source_size - relative) {
        vipr_set_error(error_buffer, error_buffer_size, "COPY instruction exceeds declared source span");
        return VIPR_STATUS_INVALID_WINDOW;
    }
    if (source_cache != NULL && source_offset <= source_cache_size &&
        length <= source_cache_size - source_offset) {
        memcpy(output + *produced, source_cache + (size_t)source_offset, (size_t)length);
        *produced += (uint32_t)length;
        return VIPR_STATUS_OK;
    }
    if (session != NULL && session->verification_cache_valid &&
        source_offset >= session->verification_cache_offset) {
        uint64_t relative = source_offset - session->verification_cache_offset;
        uint64_t cached_size = (uint64_t)session->verification_cache_size;
        if (relative < cached_size) {
            uint64_t cached = cached_size - relative;
            if (cached > length) cached = length;
            memcpy(output + *produced,
                   session->verification_buffer.data + (size_t)relative,
                   (size_t)cached);
            *produced += (uint32_t)cached;
            source_offset += cached;
            length -= cached;
            if (length == 0) return VIPR_STATUS_OK;
        }
    }
    uint64_t copied = 0;
    while (copied < length) {
        if (check_cancel(cancel) != VIPR_STATUS_OK) return VIPR_STATUS_CANCELLED;
        size_t part = (size_t)(length - copied);
        if (part > (1u << 20)) part = 1u << 20;
        vipr_status status = vipr_read_at(session, session->source, source_offset + copied, output + *produced, part, error_buffer, error_buffer_size);
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
                                const uint8_t *source_cache,
                                uint64_t source_cache_size,
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
            vipr_status status = read_copy(session, window, window->source_offset + local_offset, source_file_size, output,
                                           window->output_size, &produced, copy_length,
                                           source_cache, source_cache_size, cancel, result,
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
            vipr_status status = read_copy(session, window, source_offset, source_file_size, output, window->output_size,
                                           &produced, length, source_cache, source_cache_size,
                                           cancel, result, error_buffer, error_buffer_size);
            if (status != VIPR_STATUS_OK) return status;
            previous_copy_end = source_offset + length;
        }
    }
malformed:
    vipr_set_error(error_buffer, error_buffer_size, "malformed V4 instruction stream");
    return VIPR_STATUS_INVALID_WINDOW;
}

static uint64_t max_zstd_payload_size(uint32_t expanded_size) {
    uint64_t size = expanded_size;
    return size + size / 128u + 4096u;
}

static int window_log_for_size(uint64_t size) {
    int log = 10;
    uint64_t capacity = 1u << 10;
    while (capacity < size && log < 30) {
        capacity <<= 1;
        log++;
    }
    return log;
}

static vipr_status read_patch_payload(vipr_io_session *session,
                                      const vipr_window *window,
                                      vipr_scratch_buffer *buffer,
                                      volatile uint32_t *cancel,
                                      vipr_group_result *result,
                                      char *error_buffer, size_t error_buffer_size) {
    if (window->payload_size == 0) return VIPR_STATUS_OK;
    if (check_cancel(cancel) != VIPR_STATUS_OK) return VIPR_STATUS_CANCELLED;
    if (window->codec == VIPR_CODEC_ZSTD &&
        (window->expanded_size == 0 || window->payload_size > max_zstd_payload_size(window->expanded_size))) {
        vipr_set_error(error_buffer, error_buffer_size, "V4 zstd payload exceeds bounded compressed size");
        return VIPR_STATUS_INVALID_WINDOW;
    }
    if (!vipr_scratch_reserve(buffer, window->payload_size)) {
        vipr_set_error(error_buffer, error_buffer_size, "reserve V4 payload buffer");
        return VIPR_STATUS_MEMORY_LIMIT;
    }
    vipr_status status = vipr_read_at(session, session->patch, window->payload_offset,
                                      buffer->data, window->payload_size,
                                      error_buffer, error_buffer_size);
    if (status == VIPR_STATUS_OK && result != NULL) result->bytes_read_patch += window->payload_size;
    return status;
}

static vipr_status decompress_payload(vipr_io_session *session,
                                      const vipr_window *window,
                                      const uint8_t *compressed,
                                      uint8_t *output, size_t output_size,
                                      char *error_buffer, size_t error_buffer_size) {
    if (window->codec != VIPR_CODEC_ZSTD || window->expanded_size != output_size) {
        vipr_set_error(error_buffer, error_buffer_size, "invalid V4 zstd payload metadata");
        return VIPR_STATUS_INVALID_WINDOW;
    }
    if (window->payload_size > max_zstd_payload_size(window->expanded_size)) {
        vipr_set_error(error_buffer, error_buffer_size, "V4 zstd payload exceeds compressed size limit");
        return VIPR_STATUS_INVALID_WINDOW;
    }
    ZSTD_frameHeader frame_header;
    size_t header_status = ZSTD_getFrameHeader(&frame_header, compressed, window->payload_size);
    if (ZSTD_isError(header_status) || header_status != 0) {
        vipr_set_error(error_buffer, error_buffer_size, "invalid or incomplete V4 zstd frame header");
        return VIPR_STATUS_INVALID_WINDOW;
    }
    if (frame_header.dictID != 0) {
        vipr_set_error(error_buffer, error_buffer_size, "V4 zstd dictionaries are not supported");
        return VIPR_STATUS_INVALID_WINDOW;
    }
    if (frame_header.frameContentSize != ZSTD_CONTENTSIZE_UNKNOWN &&
        frame_header.frameContentSize != output_size) {
        vipr_set_error(error_buffer, error_buffer_size, "V4 zstd frame content size is inconsistent");
        return VIPR_STATUS_INVALID_WINDOW;
    }
    uint64_t max_window = output_size < (1u << 10) ? (1u << 10) : output_size;
    if (frame_header.windowSize > max_window) {
        vipr_set_error(error_buffer, error_buffer_size, "V4 zstd frame window exceeds expanded payload");
        return VIPR_STATUS_MEMORY_LIMIT;
    }
    size_t frame_size = ZSTD_findFrameCompressedSize(compressed, window->payload_size);
    if (ZSTD_isError(frame_size) || frame_size != window->payload_size) {
        vipr_set_error(error_buffer, error_buffer_size, "V4 zstd payload is not exactly one frame");
        return VIPR_STATUS_INVALID_WINDOW;
    }
    if (session->decompress_context == NULL) {
        session->decompress_context = ZSTD_createDCtx();
        if (session->decompress_context == NULL) {
            vipr_set_error(error_buffer, error_buffer_size, "allocate V4 decompression context");
            return VIPR_STATUS_MEMORY_LIMIT;
        }
    }
    size_t reset = ZSTD_DCtx_reset(session->decompress_context, ZSTD_reset_session_only);
    if (ZSTD_isError(reset)) {
        vipr_set_zstd_error(error_buffer, error_buffer_size, "reset V4 decoder", reset);
        return VIPR_STATUS_ZSTD_ERROR;
    }
    size_t parameter = ZSTD_DCtx_setParameter(session->decompress_context, ZSTD_d_windowLogMax,
                                              window_log_for_size(max_window));
    if (ZSTD_isError(parameter)) {
        vipr_set_zstd_error(error_buffer, error_buffer_size, "bound V4 decoder window", parameter);
        return VIPR_STATUS_ZSTD_ERROR;
    }
    parameter = ZSTD_DCtx_setParameter(session->decompress_context, ZSTD_d_format, ZSTD_f_zstd1);
    if (ZSTD_isError(parameter)) {
        vipr_set_zstd_error(error_buffer, error_buffer_size, "configure V4 decoder format", parameter);
        return VIPR_STATUS_ZSTD_ERROR;
    }
    size_t decoded = ZSTD_decompressDCtx(session->decompress_context, output, output_size,
                                         compressed, window->payload_size);
    if (ZSTD_isError(decoded) || decoded != output_size) {
        vipr_set_zstd_error(error_buffer, error_buffer_size, "decompress V4 payload", decoded);
        return VIPR_STATUS_ZSTD_ERROR;
    }
    return VIPR_STATUS_OK;
}

static vipr_status materialize_window(vipr_io_session *session,
                                      const vipr_window *window,
                                      uint64_t source_file_size,
                                      const uint8_t *source_chunk_digests,
                                      uint32_t source_chunk_count,
                                      volatile uint32_t *source_chunk_states,
                                      const uint8_t *source_cache,
                                      uint64_t source_cache_size,
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
                                             source_chunk_count, source_chunk_states,
                                             source_cache, source_cache_size, cancel, result,
                                             error_buffer, error_buffer_size);
    if (status != VIPR_STATUS_OK) return status;

    switch (window->kind) {
    case VIPR_WINDOW_SAME:
        status = read_copy(session, window, window->output_offset, source_file_size, output, window->output_size,
                           &(uint32_t){0}, window->output_size, source_cache, source_cache_size,
                           cancel, result, error_buffer, error_buffer_size);
        break;
    case VIPR_WINDOW_COPY:
        status = read_copy(session, window, window->source_offset, source_file_size, output, window->output_size,
                           &(uint32_t){0}, window->output_size, source_cache, source_cache_size,
                           cancel, result, error_buffer, error_buffer_size);
        break;
    case VIPR_WINDOW_ZERO:
        memset(output, 0, window->output_size);
        break;
    case VIPR_WINDOW_RUN:
        if (window->payload_size != 1 || window->codec != VIPR_CODEC_NONE) {
            vipr_set_error(error_buffer, error_buffer_size, "invalid RUN window payload");
            return VIPR_STATUS_INVALID_WINDOW;
        }
        status = read_patch_payload(session, window, &session->payload_buffer, cancel, result,
                                    error_buffer, error_buffer_size);
        if (status == VIPR_STATUS_OK) memset(output, session->payload_buffer.data[0], window->output_size);
        break;
    case VIPR_WINDOW_REPLACE_RAW:
        if (window->codec != VIPR_CODEC_NONE || window->payload_size != window->output_size) {
            vipr_set_error(error_buffer, error_buffer_size, "invalid raw replacement metadata");
            return VIPR_STATUS_INVALID_WINDOW;
        }
        if (check_cancel(cancel) != VIPR_STATUS_OK) return VIPR_STATUS_CANCELLED;
        status = vipr_read_at(session, session->patch, window->payload_offset, output, window->output_size,
                              error_buffer, error_buffer_size);
        if (status == VIPR_STATUS_OK && result != NULL) result->bytes_read_patch += window->payload_size;
        break;
    case VIPR_WINDOW_REPLACE_ZSTD:
        status = read_patch_payload(session, window, &session->payload_buffer, cancel, result,
                                    error_buffer, error_buffer_size);
        if (status == VIPR_STATUS_OK) {
            status = decompress_payload(session, window, session->payload_buffer.data,
                                        output, window->output_size, error_buffer, error_buffer_size);
        }
        break;
    case VIPR_WINDOW_DELTA_RAW:
        if (window->codec != VIPR_CODEC_NONE || window->expanded_size != window->payload_size) {
            vipr_set_error(error_buffer, error_buffer_size, "invalid raw delta metadata");
            return VIPR_STATUS_INVALID_WINDOW;
        }
        status = read_patch_payload(session, window, &session->payload_buffer, cancel, result,
                                    error_buffer, error_buffer_size);
        if (status == VIPR_STATUS_OK) {
            status = decode_delta(session, window, session->payload_buffer.data, window->payload_size,
                                  source_file_size, source_cache, source_cache_size, output,
                                  cancel, result, error_buffer, error_buffer_size);
        }
        break;
    case VIPR_WINDOW_DELTA_ZSTD:
        status = read_patch_payload(session, window, &session->payload_buffer, cancel, result,
                                    error_buffer, error_buffer_size);
        if (status == VIPR_STATUS_OK) {
            if (!vipr_scratch_reserve(&session->expanded_buffer, window->expanded_size)) {
                vipr_set_error(error_buffer, error_buffer_size, "reserve V4 expanded delta buffer");
                return VIPR_STATUS_MEMORY_LIMIT;
            }
            status = decompress_payload(session, window, session->payload_buffer.data,
                                        session->expanded_buffer.data, window->expanded_size,
                                        error_buffer, error_buffer_size);
        }
        if (status == VIPR_STATUS_OK) {
            status = decode_delta(session, window, session->expanded_buffer.data, window->expanded_size,
                                  source_file_size, source_cache, source_cache_size, output,
                                  cancel, result, error_buffer, error_buffer_size);
        }
        break;
    default:
        vipr_set_error(error_buffer, error_buffer_size, "unsupported V4 window kind");
        return VIPR_STATUS_INVALID_WINDOW;
    }
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

static vipr_status try_apply_verified_same_group(
    vipr_io_session *session,
    const uint8_t *encoded_windows, uint32_t window_count,
    uint64_t group_offset, uint32_t group_size,
    uint64_t source_file_size,
    const uint8_t *source_chunk_digests, uint32_t source_chunk_count,
    volatile uint32_t *source_chunk_states,
    const uint8_t *source_cache, uint64_t source_cache_size,
    const uint8_t expected_group_digest[32],
    volatile uint32_t *cancel,
    vipr_group_result *result,
    int *handled,
    char *error_buffer, size_t error_buffer_size) {
    *handled = 0;
    if (source_chunk_digests == NULL || source_chunk_states == NULL ||
        group_offset % VIPR_V4_IDENTITY_CHUNK_SIZE != 0 ||
        group_offset >= source_file_size) {
        return VIPR_STATUS_OK;
    }

    uint64_t chunk_index64 = group_offset / VIPR_V4_IDENTITY_CHUNK_SIZE;
    if (chunk_index64 >= source_chunk_count) return VIPR_STATUS_OK;
    uint64_t remaining = source_file_size - group_offset;
    size_t canonical_size = remaining > VIPR_V4_IDENTITY_CHUNK_SIZE
                                ? VIPR_V4_IDENTITY_CHUNK_SIZE
                                : (size_t)remaining;
    if ((size_t)group_size != canonical_size) return VIPR_STATUS_OK;

    for (uint32_t index = 0; index < window_count; ++index) {
        const uint8_t *encoded = encoded_windows + (size_t)index * VIPR_V4_WINDOW_DESCRIPTOR_SIZE;
        if (encoded[12] != VIPR_WINDOW_SAME) return VIPR_STATUS_OK;
    }

    *handled = 1;
    uint64_t expected_offset = group_offset;
    for (uint32_t index = 0; index < window_count; ++index) {
        vipr_window decoded_window;
        vipr_status status = decode_window_descriptor(
            encoded_windows + (size_t)index * VIPR_V4_WINDOW_DESCRIPTOR_SIZE,
            &decoded_window, error_buffer, error_buffer_size);
        if (status != VIPR_STATUS_OK) return status;
        if (decoded_window.kind != VIPR_WINDOW_SAME ||
            decoded_window.output_offset != expected_offset ||
            decoded_window.output_offset < group_offset ||
            decoded_window.output_size > group_size -
                (uint32_t)(decoded_window.output_offset - group_offset)) {
            vipr_set_error(error_buffer, error_buffer_size,
                           "V4 SAME group windows are not contiguous");
            return VIPR_STATUS_INVALID_WINDOW;
        }
        status = verify_source_range(session, &decoded_window, source_file_size,
                                     source_chunk_digests, source_chunk_count,
                                     source_chunk_states, source_cache,
                                     source_cache_size, cancel, result,
                                     error_buffer, error_buffer_size);
        if (status != VIPR_STATUS_OK) return status;
        expected_offset += decoded_window.output_size;
    }
    if (expected_offset != group_offset + group_size) {
        vipr_set_error(error_buffer, error_buffer_size,
                       "V4 SAME group does not cover its output range");
        return VIPR_STATUS_INVALID_WINDOW;
    }

    // Only bypass normal materialization when this session owns the exact
    // bytes it just authenticated. If another worker verified the chunk, the
    // cache is absent and the regular read/hash/write path remains mandatory.
    if (!session->verification_cache_valid ||
        session->verification_cache_offset != group_offset ||
        session->verification_cache_size != canonical_size) {
        *handled = 0;
        return VIPR_STATUS_OK;
    }

    const uint8_t *source_digest = source_chunk_digests + (size_t)chunk_index64 * 32u;
    if (!digest_equal(source_digest, expected_group_digest)) {
        vipr_set_error(error_buffer, error_buffer_size,
                       "V4 SAME group source and output digests disagree");
        return VIPR_STATUS_OUTPUT_MISMATCH;
    }

    vipr_status status = vipr_write_at(session, session->output, group_offset,
                                       session->verification_buffer.data,
                                       canonical_size,
                                       error_buffer, error_buffer_size);
    if (status == VIPR_STATUS_OK && result != NULL) {
        result->bytes_written += canonical_size;
        result->windows_completed += window_count;
        result->reserved |= VIPR_GROUP_RESULT_DIRECT_SAME;
    }
    return status;
}

vipr_status vipr_apply_group(vipr_io_session *session,
                             const uint8_t *encoded_windows, uint32_t window_count,
                             uint64_t group_offset, uint32_t group_size,
                             uint64_t source_file_size,
                             const uint8_t *source_chunk_digests, uint32_t source_chunk_count,
                             volatile uint32_t *source_chunk_states,
                             const uint8_t *source_cache, uint64_t source_cache_size,
                             const uint8_t expected_group_digest[32],
                             volatile uint32_t *cancel,
                             vipr_group_result *result,
                             char *error_buffer, size_t error_buffer_size) {
    if (session == NULL || encoded_windows == NULL || window_count == 0 || group_size == 0 || expected_group_digest == NULL) {
        vipr_set_error(error_buffer, error_buffer_size, "invalid V4 group arguments");
        return VIPR_STATUS_INVALID_ARGUMENT;
    }
    if (result != NULL) memset(result, 0, sizeof(*result));

    int handled = 0;
    vipr_status status = try_apply_verified_same_group(
        session, encoded_windows, window_count, group_offset, group_size,
        source_file_size, source_chunk_digests, source_chunk_count,
        source_chunk_states, source_cache, source_cache_size,
        expected_group_digest, cancel, result, &handled,
        error_buffer, error_buffer_size);
    if (status != VIPR_STATUS_OK || handled) return status;

    if (!vipr_scratch_reserve(&session->group_buffer, group_size)) {
        vipr_set_error(error_buffer, error_buffer_size, "reserve V4 output group");
        return VIPR_STATUS_MEMORY_LIMIT;
    }
    uint8_t *buffer = session->group_buffer.data;
    status = VIPR_STATUS_OK;
    uint64_t expected_offset = group_offset;
    for (uint32_t index = 0; index < window_count; ++index) {
        vipr_window decoded_window;
        status = decode_window_descriptor(encoded_windows + (size_t)index * VIPR_V4_WINDOW_DESCRIPTOR_SIZE,
                                          &decoded_window, error_buffer, error_buffer_size);
        if (status != VIPR_STATUS_OK) break;
        const vipr_window *window = &decoded_window;
        if (window->output_offset != expected_offset || window->output_offset < group_offset ||
            window->output_size > group_size - (uint32_t)(window->output_offset - group_offset)) {
            status = VIPR_STATUS_INVALID_WINDOW;
            vipr_set_error(error_buffer, error_buffer_size, "V4 group windows are not contiguous");
            break;
        }
        status = materialize_window(session, window, source_file_size, source_chunk_digests,
                                    source_chunk_count, source_chunk_states,
                                    source_cache, source_cache_size,
                                    buffer + (size_t)(window->output_offset - group_offset), 0, cancel, result,
                                    error_buffer, error_buffer_size);
        if (status != VIPR_STATUS_OK) break;
        expected_offset += window->output_size;
    }
    if (status == VIPR_STATUS_OK && expected_offset != group_offset + group_size) {
        vipr_set_error(error_buffer, error_buffer_size, "V4 group does not cover its declared output range");
        status = VIPR_STATUS_INVALID_WINDOW;
    }
    if (status == VIPR_STATUS_OK) {
        uint8_t digest[32];
        vipr_blake3_hash(buffer, group_size, digest);
        if (!digest_equal(digest, expected_group_digest)) {
            vipr_set_error(error_buffer, error_buffer_size, "V4 output group digest mismatch");
            status = VIPR_STATUS_OUTPUT_MISMATCH;
        }
    }
    if (status == VIPR_STATUS_OK) {
        status = vipr_write_at(session, session->output, group_offset, buffer, group_size,
                               error_buffer, error_buffer_size);
        if (status == VIPR_STATUS_OK && result != NULL) result->bytes_written += group_size;
    }
    return status;
}

vipr_status vipr_apply_changed_window(vipr_io_session *session, const uint8_t *encoded_window,
                                      uint64_t source_file_size,
                                      const uint8_t *source_chunk_digests, uint32_t source_chunk_count,
                                      volatile uint32_t *source_chunk_states,
                                      const uint8_t *source_cache, uint64_t source_cache_size,
                                      volatile uint32_t *cancel,
                                      vipr_group_result *result,
                                      char *error_buffer, size_t error_buffer_size) {
    if (session == NULL || encoded_window == NULL) {
        vipr_set_error(error_buffer, error_buffer_size, "invalid V4 changed-window arguments");
        return VIPR_STATUS_INVALID_ARGUMENT;
    }
    vipr_window decoded_window;
    vipr_status status = decode_window_descriptor(encoded_window, &decoded_window,
                                                  error_buffer, error_buffer_size);
    if (status != VIPR_STATUS_OK) return status;
    const vipr_window *window = &decoded_window;
    if (result != NULL) memset(result, 0, sizeof(*result));
    if (!vipr_scratch_reserve(&session->group_buffer, window->output_size)) {
        vipr_set_error(error_buffer, error_buffer_size, "reserve V4 changed-window output");
        return VIPR_STATUS_MEMORY_LIMIT;
    }
    uint8_t *buffer = session->group_buffer.data;
    status = materialize_window(session, window, source_file_size, source_chunk_digests,
                                source_chunk_count, source_chunk_states,
                                source_cache, source_cache_size, buffer, 1, cancel, result,
                                error_buffer, error_buffer_size);
    if (status == VIPR_STATUS_OK && window->kind != VIPR_WINDOW_SAME) {
        status = vipr_write_at(session, session->output, window->output_offset, buffer, window->output_size,
                               error_buffer, error_buffer_size);
        if (status == VIPR_STATUS_OK && result != NULL) result->bytes_written += window->output_size;
    }
    return status;
}
