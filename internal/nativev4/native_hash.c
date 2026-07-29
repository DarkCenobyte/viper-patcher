#include "native_internal.h"

#include <stdlib.h>
#include <string.h>

static vipr_handle select_read_handle(vipr_io_session *session, int use_patch_handle) {
    return use_patch_handle ? session->patch : session->source;
}

vipr_status vipr_hash_bytes(const uint8_t *data, size_t size, uint8_t out[32]) {
    if ((data == NULL && size != 0) || out == NULL) return VIPR_STATUS_INVALID_ARGUMENT;
    vipr_blake3_hash(data, size, out);
    return VIPR_STATUS_OK;
}

static void update_u64_le(vipr_blake3_hasher *hasher, uint64_t value) {
    uint8_t encoded[8];
    for (size_t i = 0; i < 8; ++i) encoded[i] = (uint8_t)(value >> (i * 8));
    vipr_blake3_update(hasher, encoded, sizeof(encoded));
}

vipr_status vipr_tree_root(uint64_t file_size, uint64_t chunk_size, const uint8_t *digests,
                           uint32_t digest_count, uint8_t out[32]) {
    static const uint8_t domain[] = "VIPR-BLAKE3-TREE-V1\0";
    if (out == NULL || chunk_size == 0 || (digest_count != 0 && digests == NULL)) return VIPR_STATUS_INVALID_ARGUMENT;
    uint64_t expected = file_size == 0 ? 0 : (file_size + chunk_size - 1) / chunk_size;
    if (expected != digest_count) return VIPR_STATUS_INVALID_ARGUMENT;
    vipr_blake3_hasher hasher;
    vipr_blake3_init(&hasher);
    vipr_blake3_update(&hasher, domain, sizeof(domain) - 1);
    update_u64_le(&hasher, file_size);
    update_u64_le(&hasher, chunk_size);
    update_u64_le(&hasher, digest_count);
    if (digest_count != 0) vipr_blake3_update(&hasher, digests, (size_t)digest_count * 32u);
    vipr_blake3_finalize(&hasher, out);
    return VIPR_STATUS_OK;
}

vipr_status vipr_hash_file_tree(vipr_io_session *session, int use_patch_handle, uint64_t file_size,
                                uint64_t chunk_size, uint8_t *digests, uint32_t digest_count,
                                uint8_t root[32], volatile uint32_t *cancel,
                                char *error_buffer, size_t error_buffer_size) {
    if (session == NULL || chunk_size == 0 || root == NULL || (digest_count != 0 && digests == NULL)) return VIPR_STATUS_INVALID_ARGUMENT;
    uint64_t expected = file_size == 0 ? 0 : (file_size + chunk_size - 1) / chunk_size;
    if (expected != digest_count || chunk_size > SIZE_MAX) return VIPR_STATUS_INVALID_ARGUMENT;
    uint8_t *buffer = digest_count == 0 ? NULL : (uint8_t *)malloc((size_t)chunk_size);
    if (digest_count != 0 && buffer == NULL) { vipr_set_error(error_buffer, error_buffer_size, "allocate BLAKE3 chunk buffer"); return VIPR_STATUS_MEMORY_LIMIT; }
    vipr_handle handle = select_read_handle(session, use_patch_handle);
    for (uint32_t index = 0; index < digest_count; ++index) {
        if (vipr_cancelled(cancel)) { free(buffer); return VIPR_STATUS_CANCELLED; }
        uint64_t offset = (uint64_t)index * chunk_size;
        uint64_t remaining = file_size - offset;
        size_t length = (size_t)(remaining < chunk_size ? remaining : chunk_size);
        vipr_status status = vipr_read_at(handle, offset, buffer, length, error_buffer, error_buffer_size);
        if (status != VIPR_STATUS_OK) { free(buffer); return status; }
        vipr_blake3_hash(buffer, length, digests + (size_t)index * 32u);
    }
    free(buffer);
    return vipr_tree_root(file_size, chunk_size, digests, digest_count, root);
}

vipr_status vipr_hash_file_standard(vipr_io_session *session, int use_patch_handle, uint64_t file_size,
                                    uint8_t out[32], volatile uint32_t *cancel,
                                    char *error_buffer, size_t error_buffer_size) {
    if (session == NULL || out == NULL) return VIPR_STATUS_INVALID_ARGUMENT;
    const size_t buffer_size = 1u << 20;
    uint8_t *buffer = (uint8_t *)malloc(buffer_size);
    if (buffer == NULL) { vipr_set_error(error_buffer, error_buffer_size, "allocate BLAKE3 I/O buffer"); return VIPR_STATUS_MEMORY_LIMIT; }
    vipr_handle handle = select_read_handle(session, use_patch_handle);
    vipr_blake3_hasher hasher;
    vipr_blake3_init(&hasher);
    uint64_t offset = 0;
    while (offset < file_size) {
        if (vipr_cancelled(cancel)) { free(buffer); return VIPR_STATUS_CANCELLED; }
        uint64_t remaining = file_size - offset;
        size_t length = (size_t)(remaining < buffer_size ? remaining : buffer_size);
        vipr_status status = vipr_read_at(handle, offset, buffer, length, error_buffer, error_buffer_size);
        if (status != VIPR_STATUS_OK) { free(buffer); return status; }
        vipr_blake3_update(&hasher, buffer, length);
        offset += length;
    }
    vipr_blake3_finalize(&hasher, out);
    free(buffer);
    return VIPR_STATUS_OK;
}
