#ifndef VIPR_NATIVE_V4_H
#define VIPR_NATIVE_V4_H

#include <stddef.h>
#include <stdint.h>

#include "v4_format_generated.h"

#ifdef __cplusplus
extern "C" {
#endif

typedef enum {
    VIPR_STATUS_OK = 0,
    VIPR_STATUS_CANCELLED = 1,
    VIPR_STATUS_INVALID_ARGUMENT = 2,
    VIPR_STATUS_INVALID_WINDOW = 3,
    VIPR_STATUS_SOURCE_MISMATCH = 4,
    VIPR_STATUS_OUTPUT_MISMATCH = 5,
    VIPR_STATUS_READ_ERROR = 6,
    VIPR_STATUS_WRITE_ERROR = 7,
    VIPR_STATUS_ZSTD_ERROR = 8,
    VIPR_STATUS_MEMORY_LIMIT = 9,
    VIPR_STATUS_UNSUPPORTED = 10,
    VIPR_STATUS_INTERNAL = 11
} vipr_status;

typedef struct vipr_io_session vipr_io_session;

typedef struct {
    uint64_t output_offset;
    uint32_t output_size;
    uint8_t kind;
    uint8_t codec;
    uint16_t flags;
    uint64_t payload_offset;
    uint32_t payload_size;
    uint32_t expanded_size;
    uint64_t source_offset;
    uint32_t source_size;
    uint32_t source_first_chunk;
    uint16_t source_chunk_count;
    uint16_t instruction_count;
    uint8_t digest[32];
} vipr_window;

typedef struct {
    uint8_t kind;
    uint8_t codec;
    uint16_t flags;
    uint32_t payload_size;
    uint32_t expanded_size;
    uint64_t source_offset;
    uint32_t source_size;
    uint32_t source_first_chunk;
    uint16_t source_chunk_count;
    uint16_t instruction_count;
    uint8_t digest[32];
    uint8_t *payload;
} vipr_window_result;

typedef struct {
    uint64_t bytes_read_patch;
    uint64_t bytes_read_source;
    uint64_t bytes_written;
    uint32_t windows_completed;
    uint32_t reserved;
} vipr_group_result;

const char *vipr_status_name(vipr_status status);
const char *vipr_zstd_version(void);

vipr_io_session *vipr_session_create(uintptr_t source_handle, uintptr_t patch_handle, uintptr_t output_handle,
                                     int need_source, int need_patch, int need_output, int io_profile,
                                     char *error_buffer, size_t error_buffer_size);
void vipr_session_free(vipr_io_session *session);

vipr_status vipr_hash_bytes(const uint8_t *data, size_t size, uint8_t out[32]);
vipr_status vipr_tree_root(uint64_t file_size, uint64_t chunk_size, const uint8_t *digests,
                           uint32_t digest_count, uint8_t out[32]);
vipr_status vipr_hash_file_tree(vipr_io_session *session, int use_patch_handle, uint64_t file_size,
                                uint64_t chunk_size, uint8_t *digests, uint32_t digest_count,
                                uint8_t root[32], volatile uint32_t *cancel,
                                char *error_buffer, size_t error_buffer_size);
vipr_status vipr_hash_file_standard(vipr_io_session *session, int use_patch_handle, uint64_t file_size,
                                    uint8_t out[32], volatile uint32_t *cancel,
                                    char *error_buffer, size_t error_buffer_size);

vipr_status vipr_build_window(vipr_io_session *session,
                              uint64_t source_size, uint64_t target_size,
                              uint64_t output_offset, uint32_t output_size,
                              uint32_t window_size, int compression_level, uint8_t optimization_mode,
                              volatile uint32_t *cancel, vipr_window_result *result,
                              char *error_buffer, size_t error_buffer_size);
void vipr_window_result_free(vipr_window_result *result);

vipr_status vipr_apply_group(vipr_io_session *session,
                             const uint8_t *encoded_windows, uint32_t window_count,
                             uint64_t group_offset, uint32_t group_size,
                             uint64_t source_file_size,
                             const uint8_t *source_chunk_digests, uint32_t source_chunk_count,
                             volatile uint32_t *source_chunk_states,
                             const uint8_t expected_group_digest[32],
                             volatile uint32_t *cancel,
                             vipr_group_result *result,
                             char *error_buffer, size_t error_buffer_size);

vipr_status vipr_apply_changed_window(vipr_io_session *session, const uint8_t *encoded_window,
                                      uint64_t source_file_size,
                                      const uint8_t *source_chunk_digests, uint32_t source_chunk_count,
                                      volatile uint32_t *source_chunk_states,
                                      volatile uint32_t *cancel,
                                      vipr_group_result *result,
                                      char *error_buffer, size_t error_buffer_size);

vipr_status vipr_set_file_size(vipr_io_session *session, uint64_t size,
                               char *error_buffer, size_t error_buffer_size);
vipr_status vipr_flush_output(vipr_io_session *session, char *error_buffer, size_t error_buffer_size);

// Attempts an OS copy-on-write clone from the source handle into the already
// opened output handle. VIPR_STATUS_UNSUPPORTED is a normal capability miss.
vipr_status vipr_clone_output(vipr_io_session *session, uint64_t size,
                              char *error_buffer, size_t error_buffer_size);

#ifdef __cplusplus
}
#endif

#endif
