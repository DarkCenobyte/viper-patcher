#ifndef VIPR_NATIVE_V4_INTERNAL_H
#define VIPR_NATIVE_V4_INTERNAL_H

#define ZSTD_STATIC_LINKING_ONLY
#include <zstd.h>

#include <stddef.h>
#include <stdint.h>

#include "native.h"
#include "blake3_backend.h"

#ifdef _WIN32
#define WIN32_LEAN_AND_MEAN
#include <windows.h>
typedef HANDLE vipr_handle;
#else
#include <sys/types.h>
typedef int vipr_handle;
#endif

typedef struct {
    uint8_t *data;
    size_t capacity;
} vipr_scratch_buffer;

typedef struct vipr_window_workspace vipr_window_workspace;

struct vipr_io_session {
    vipr_handle source;
    vipr_handle patch;
    vipr_handle output;
    int owns_source;
    int owns_patch;
    int owns_output;
    int io_profile;
#ifdef _WIN32
    HANDLE io_event;
#endif
    ZSTD_CCtx *compress_context;
    ZSTD_DCtx *decompress_context;
    vipr_scratch_buffer group_buffer;
    vipr_scratch_buffer payload_buffer;
    vipr_scratch_buffer expanded_buffer;
    vipr_scratch_buffer verification_buffer;
    uint64_t verification_cache_offset;
    size_t verification_cache_size;
    int verification_cache_valid;
    vipr_window_workspace *window_workspace;
    int window_result_borrowed;
};

void vipr_set_error(char *buffer, size_t size, const char *message);
void vipr_set_system_error(char *buffer, size_t size, const char *operation);
void vipr_set_zstd_error(char *buffer, size_t size, const char *operation, size_t code);
int vipr_cancelled(const volatile uint32_t *cancel);

int vipr_scratch_reserve(vipr_scratch_buffer *buffer, size_t size);
void vipr_scratch_free(vipr_scratch_buffer *buffer);

vipr_window_workspace *vipr_window_workspace_create(void);
void vipr_window_workspace_free(vipr_window_workspace *workspace);

vipr_status vipr_read_at(vipr_io_session *session, vipr_handle handle, uint64_t offset, void *buffer, size_t size,
                         char *error_buffer, size_t error_buffer_size);
vipr_status vipr_write_at(vipr_io_session *session, vipr_handle handle, uint64_t offset, const void *buffer, size_t size,
                          char *error_buffer, size_t error_buffer_size);
vipr_status vipr_resize(vipr_io_session *session, vipr_handle handle, uint64_t size, int preallocate,
                        char *error_buffer, size_t error_buffer_size);
vipr_status vipr_flush(vipr_handle handle, char *error_buffer, size_t error_buffer_size);

uint64_t vipr_hash64(const uint8_t *data, size_t size);
size_t vipr_write_uvarint(uint8_t *output, uint64_t value);
int vipr_read_uvarint(const uint8_t *input, size_t input_size, size_t *cursor, uint64_t *value);

#endif
